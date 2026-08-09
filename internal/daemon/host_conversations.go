package daemon

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

// Listing the conversations attn has already recorded, so one can be picked up
// again in a new session.
//
// This is a display-only read, and deliberately shallow. The resume itself never
// comes back here: it hands the file's path to the host, which forks it through
// pi's own SessionManager and therefore takes pi's session-format migrations for
// free. If pi changes its format tomorrow, the worst this can do is show a row
// with a blank label — the conversation still resumes.
//
// Nothing here writes. A file listed is a file some session is still appending
// to, possibly right now, which is why `live` is on the row.

const (
	// pastConversationsLimit caps a listing. A row's size is bounded by its own
	// fields rather than estimated: the preview is clipped to 200 runes below,
	// and the rest is a path, a cwd and three numbers — call it 450 bytes at the
	// worst, so 500 rows is ~225 KB in one message, against a WebSocket client
	// buffer of 256 messages. It is also far more rows than a picker can usefully
	// show. A user with more conversations than this gets the newest ones and is
	// told there are more.
	pastConversationsLimit = 500

	// pastConversationHeadBytes is how far into a session file the header scan
	// reads. Receipt (2026-08-09, pi 0.83.0, a real session file on this
	// machine): a session writes its header, a model_change and a
	// thinking_level_change before the first user message — 4 lines, 637 bytes
	// to the end of that message. 256 KB is a tripwire, not a budget: it exists
	// so a corrupt or enormous single line cannot make a picker hang.
	pastConversationHeadBytes = 256 << 10

	// pastConversationPreviewRunes clips the label. A picker row shows one line.
	pastConversationPreviewRunes = 200
)

// pastConversationFile is one candidate, before its head has been read.
type pastConversationFile struct {
	sessionID string
	path      string
	modified  int
	bytes     int
}

// listPastConversations returns the recorded conversations, newest first, and
// whether the list was clipped.
//
// The two-pass shape is the point: every file is stat'ed (cheap) so the sort is
// honest, and only the ones that survive the cap are opened and parsed.
func (d *Daemon) listPastConversations() ([]protocol.PastConversation, bool) {
	return d.listPastConversationsIn(filepath.Join(config.DataDir(), "hosts", "state"))
}

func (d *Daemon) listPastConversationsIn(root string) ([]protocol.PastConversation, bool) {
	files := collectPastConversationFiles(root)
	sort.Slice(files, func(i, j int) bool {
		if files[i].modified != files[j].modified {
			return files[i].modified > files[j].modified
		}
		return files[i].path < files[j].path
	})
	truncated := len(files) > pastConversationsLimit
	if truncated {
		files = files[:pastConversationsLimit]
	}
	conversations := make([]protocol.PastConversation, 0, len(files))
	for _, file := range files {
		cwd, preview := readPastConversationHead(file.path)
		conversations = append(conversations, protocol.PastConversation{
			SessionID: file.sessionID,
			File:      file.path,
			Cwd:       cwd,
			Preview:   preview,
			Modified:  file.modified,
			Bytes:     file.bytes,
			Live:      d.isHostSession(file.sessionID),
		})
	}
	return conversations, truncated
}

// collectPastConversationFiles finds every session file under the host state
// root. The layout is one directory per attn session, so the directory name is
// the session id and a session that forked has several files in it.
func collectPastConversationFiles(root string) []pastConversationFile {
	entries, err := os.ReadDir(root)
	if err != nil {
		// No conversation has ever run here. An empty list is the right answer,
		// not an error the picker has to explain.
		return nil
	}
	var files []pastConversationFile
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		sessionFiles, err := os.ReadDir(filepath.Join(root, sessionID))
		if err != nil {
			continue
		}
		for _, sessionFile := range sessionFiles {
			if sessionFile.IsDir() || !strings.HasSuffix(sessionFile.Name(), ".jsonl") {
				continue
			}
			info, err := sessionFile.Info()
			if err != nil || !isRegular(info) || info.Size() == 0 {
				continue
			}
			files = append(files, pastConversationFile{
				sessionID: sessionID,
				path:      filepath.Join(root, sessionID, sessionFile.Name()),
				modified:  int(info.ModTime().UnixMilli()),
				bytes:     int(info.Size()),
			})
		}
	}
	return files
}

func isRegular(info fs.FileInfo) bool { return info.Mode().IsRegular() }

// readPastConversationHead pulls the two things a row is labelled with: the
// directory the conversation ran in, and the first thing the user said in it.
//
// Everything it cannot find comes back empty. A conversation whose file was
// written by a pi version this daemon cannot parse is still resumable — pi reads
// it — so a parse failure here must degrade the label, never drop the row.
func readPastConversationHead(path string) (cwd string, preview string) {
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(&io.LimitedReader{R: file, N: pastConversationHeadBytes})
	scanner.Buffer(make([]byte, 0, 64<<10), pastConversationHeadBytes)
	for scanner.Scan() {
		var entry struct {
			Type    string `json:"type"`
			Cwd     string `json:"cwd"`
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		switch entry.Type {
		case "session":
			cwd = entry.Cwd
		case "message":
			if entry.Message.Role != "user" {
				continue
			}
			for _, part := range entry.Message.Content {
				if part.Type != "text" {
					continue
				}
				if text := strings.TrimSpace(part.Text); text != "" {
					return cwd, clipPreview(text)
				}
			}
		}
	}
	return cwd, ""
}

// clipPreview flattens a message to one line and cuts it to a label's length.
func clipPreview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= pastConversationPreviewRunes {
		return text
	}
	return string(runes[:pastConversationPreviewRunes]) + "..."
}

// handleListPastConversations answers the list_past_conversations command.
//
// It answers on the asking client alone: a picker is one window's question, and
// nothing about the answer changes what any other window is showing.
func (d *Daemon) handleListPastConversations(client *wsClient, msg *protocol.ListPastConversationsMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdListPastConversations, "list_past_conversations is missing a request id")
		return
	}
	conversations, truncated := d.listPastConversations()
	d.sendToClient(client, &protocol.PastConversationsResultMessage{
		Event:         protocol.EventPastConversationsResult,
		RequestID:     requestID,
		Success:       true,
		Conversations: conversations,
		Truncated:     truncated,
	})
}
