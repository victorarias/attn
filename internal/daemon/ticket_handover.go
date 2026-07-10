package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type stagedHandoverFile struct {
	filename    string
	stagedPath  string
	destination string
	hash        string
	installed   bool
}

type handoverFingerprintFile struct {
	Filename string `json:"filename"`
	Hash     string `json:"hash"`
}

type handoverFingerprintInput struct {
	Version  int                       `json:"version"`
	TicketID string                    `json:"ticket_id"`
	Files    []handoverFingerprintFile `json:"files"`
	State    string                    `json:"state,omitempty"`
	Comment  string                    `json:"comment,omitempty"`
}

// handleTicketHandover is the Unix-socket form used by the CLI and agents.
func (d *Daemon) handleTicketHandover(conn net.Conn, msg *protocol.TicketHandoverMessage) {
	result, err := d.submitTicketHandover(msg, strings.TrimSpace(msg.SourceSessionID), true)
	if err != nil {
		d.sendError(conn, "ticket handover: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TicketHandoverResult: result})
}

// handleTicketHandoverWS is the in-app form. The human is the audited author and
// must name the ticket explicitly.
func (d *Daemon) handleTicketHandoverWS(client *wsClient, msg *protocol.TicketHandoverMessage) {
	requestID := protocol.Deref(msg.RequestID)
	result, err := d.submitTicketHandover(msg, store.TicketAuthorYou, false)
	reply := protocol.TicketHandoverResultMessage{
		Event:     protocol.EventTicketHandoverResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		reply.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, reply)
}

func (d *Daemon) submitTicketHandover(msg *protocol.TicketHandoverMessage, author string, resolveBound bool) (*protocol.TicketHandoverResult, error) {
	if strings.TrimSpace(author) == "" {
		return nil, errors.New("source_session_id is required")
	}
	ticketID := strings.TrimSpace(protocol.Deref(msg.TicketID))
	if ticketID == "" && resolveBound {
		ticket, err := d.store.ActiveTicketForSession(author)
		if err != nil {
			return nil, err
		}
		if ticket == nil {
			return nil, errors.New("no active ticket bound to this session")
		}
		ticketID = ticket.ID
	}
	if ticketID == "" {
		return nil, errors.New("ticket_id is required")
	}
	if ticket, err := d.store.GetTicket(ticketID); err != nil {
		return nil, err
	} else if ticket == nil {
		return nil, fmt.Errorf("ticket not found: %s", ticketID)
	}
	if len(msg.Files) == 0 {
		return nil, errors.New("at least one file is required")
	}

	var status *store.TicketStatus
	if msg.State != nil {
		mapped, ok := ticketStatusFromWorkState(*msg.State)
		if !ok {
			return nil, fmt.Errorf("unknown work state %q", *msg.State)
		}
		status = &mapped
	}
	comment := strings.TrimSpace(protocol.Deref(msg.Comment))

	root, err := d.notebookRoot()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("notebook is not configured")
	}
	dir := notebook.TicketArtifactsDir(root, ticketID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	staged, err := stageTicketHandoverFiles(dir, msg.Files)
	if err != nil {
		return nil, err
	}
	defer removeHandoverStages(staged)

	fingerprint, detail, err := handoverFingerprint(ticketID, staged, msg.State, comment)
	if err != nil {
		return nil, err
	}
	activityComment := handoverActivityComment(staged, comment)

	d.ticketArtifactMu.Lock()
	defer d.ticketArtifactMu.Unlock()

	if err := validateHandoverDestinations(staged); err != nil {
		return nil, err
	}
	if err := installHandoverFiles(staged); err != nil {
		rollbackInstalledHandoverFiles(staged)
		return nil, err
	}
	record, err := d.store.SubmitTicketHandover(ticketID, author, fingerprint, detail, activityComment, status, time.Now())
	if err != nil {
		rollbackInstalledHandoverFiles(staged)
		return nil, err
	}

	artifacts := make([]protocol.TicketArtifact, 0, len(staged))
	changedPaths := make([]string, 0, len(staged))
	selfWrites := make([]notebook.SelfWrite, 0, len(staged))
	for _, file := range staged {
		rel := filepath.ToSlash(filepath.Join("tickets", ticketID, file.filename))
		artifacts = append(artifacts, protocol.TicketArtifact{Filename: file.filename, NotebookPath: rel, Path: file.destination})
		changedPaths = append(changedPaths, rel)
		selfWrites = append(selfWrites, notebook.SelfWrite{Rel: rel, Hash: file.hash})
	}
	d.noteNotebookSelfWrite(selfWrites...)
	d.notifyTicketObservers(ticketID)
	d.broadcastTicketsUpdated()
	d.broadcastNotebookChanged(originAgent, changedPaths...)
	d.broadcastFsChanged(originAgent, changedPaths...)

	return &protocol.TicketHandoverResult{
		TicketID:     ticketID,
		Artifacts:    artifacts,
		Fingerprint:  fingerprint,
		EventSeq:     int(record.EventSeq),
		State:        protocol.TicketStatus(record.Status),
		Deduplicated: record.Deduplicated,
	}, nil
}

func stageTicketHandoverFiles(dir string, files []protocol.TicketHandoverFile) ([]*stagedHandoverFile, error) {
	seen := make(map[string]struct{}, len(files))
	staged := make([]*stagedHandoverFile, 0, len(files))
	for _, input := range files {
		filename := filepath.Base(strings.TrimSpace(input.Filename))
		if filename == "" || filename == "." || filename == ".." || strings.HasPrefix(filename, ".") || filepath.Ext(filename) != ".md" {
			removeHandoverStages(staged)
			return nil, fmt.Errorf("%q is not a visible Markdown filename", input.Filename)
		}
		if _, exists := seen[filename]; exists {
			removeHandoverStages(staged)
			return nil, fmt.Errorf("duplicate destination filename %q", filename)
		}
		seen[filename] = struct{}{}

		source := strings.TrimSpace(input.SourcePath)
		info, err := os.Lstat(source)
		if err != nil {
			removeHandoverStages(staged)
			return nil, fmt.Errorf("read %q: %w", source, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			removeHandoverStages(staged)
			return nil, fmt.Errorf("%q is not a regular file", source)
		}
		in, err := os.Open(source)
		if err != nil {
			removeHandoverStages(staged)
			return nil, err
		}
		stage, err := os.CreateTemp(dir, ".handover-*")
		if err != nil {
			in.Close()
			removeHandoverStages(staged)
			return nil, err
		}
		hasher := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(stage, hasher), in)
		closeInErr := in.Close()
		closeStageErr := stage.Close()
		if copyErr != nil || closeInErr != nil || closeStageErr != nil {
			_ = os.Remove(stage.Name())
			removeHandoverStages(staged)
			return nil, errors.Join(copyErr, closeInErr, closeStageErr)
		}
		staged = append(staged, &stagedHandoverFile{
			filename: filename, stagedPath: stage.Name(), destination: filepath.Join(dir, filename), hash: hex.EncodeToString(hasher.Sum(nil)),
		})
	}
	return staged, nil
}

func handoverFingerprint(ticketID string, files []*stagedHandoverFile, state *protocol.DispatchWorkState, comment string) (string, string, error) {
	payload := handoverFingerprintInput{Version: 1, TicketID: ticketID, Comment: comment}
	if state != nil {
		payload.State = string(*state)
	}
	for _, file := range files {
		payload.Files = append(payload.Files, handoverFingerprintFile{Filename: file.filename, Hash: file.hash})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(sum[:])
	return fingerprint, fingerprint + "\n" + string(encoded), nil
}

func handoverActivityComment(files []*stagedHandoverFile, comment string) string {
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.filename)
	}
	text := "Handed over: " + strings.Join(names, ", ")
	if comment != "" {
		text += "\n\n" + comment
	}
	return text
}

func validateHandoverDestinations(files []*stagedHandoverFile) error {
	for _, file := range files {
		data, err := os.ReadFile(file.destination)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != file.hash {
			return fmt.Errorf("artifact %q already exists with different contents; choose another filename", file.filename)
		}
	}
	return nil
}

func installHandoverFiles(files []*stagedHandoverFile) error {
	for _, file := range files {
		if _, err := os.Stat(file.destination); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Link(file.stagedPath, file.destination); err != nil {
			if errors.Is(err, os.ErrExist) {
				data, readErr := os.ReadFile(file.destination)
				if readErr != nil {
					return readErr
				}
				sum := sha256.Sum256(data)
				if hex.EncodeToString(sum[:]) == file.hash {
					continue
				}
				return fmt.Errorf("artifact %q already exists with different contents; choose another filename", file.filename)
			}
			return err
		}
		file.installed = true
		if err := os.Remove(file.stagedPath); err != nil {
			_ = os.Remove(file.destination)
			file.installed = false
			return err
		}
		file.stagedPath = ""
	}
	return nil
}

func rollbackInstalledHandoverFiles(files []*stagedHandoverFile) {
	for _, file := range files {
		if file.installed {
			data, err := os.ReadFile(file.destination)
			if err != nil {
				continue
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) == file.hash {
				_ = os.Remove(file.destination)
			}
		}
	}
}

func removeHandoverStages(files []*stagedHandoverFile) {
	for _, file := range files {
		if file.stagedPath != "" {
			_ = os.Remove(file.stagedPath)
		}
	}
}
