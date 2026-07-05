package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// presentTestRepo creates a tiny real git repo with one commit, so present.Pin
// (invoked inside the handler) resolves real SHAs rather than failing on a
// nonexistent ref or repo.
func presentTestRepo(t *testing.T) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init")
	run("commit", "--allow-empty", "-m", "init")
	sha = run("rev-parse", "HEAD")
	return dir, sha
}

// presentManifestYAML builds a minimal valid manifest YAML pointing at repoDir,
// with base and head both HEAD (a same-SHA round is fine for these tests).
func presentManifestYAML(title, repoDir string) string {
	return fmt.Sprintf("version: 1\nkind: changes\ntitle: %q\nframe:\n  repo: %q\n  base: HEAD\n  head: HEAD\n", title, repoDir)
}

func callPresentOpen(t *testing.T, d *Daemon, msg *protocol.PresentOpenMessage) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	d.handlePresentOpen(conn, msg)
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode present-open response: %v", err)
	}
	return resp
}

func TestHandlePresentOpen_HappyPath(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	repoDir, _ := presentTestRepo(t)

	resp := callPresentOpen(t, d, &protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: "session-1",
		ManifestYaml:    presentManifestYAML("My Change", repoDir),
	})
	if !resp.Ok || resp.PresentOpenResult == nil {
		t.Fatalf("present open response = %+v, want ok with a result", resp)
	}
	if resp.PresentOpenResult.Seq != 1 {
		t.Errorf("first round seq = %d, want 1", resp.PresentOpenResult.Seq)
	}
	if resp.PresentOpenResult.Title != "My Change" {
		t.Errorf("title = %q, want %q", resp.PresentOpenResult.Title, "My Change")
	}
	if len(resp.PresentOpenResult.BaseSHA) != 40 || len(resp.PresentOpenResult.HeadSHA) != 40 {
		t.Errorf("expected pinned 40-char SHAs, got base=%q head=%q", resp.PresentOpenResult.BaseSHA, resp.PresentOpenResult.HeadSHA)
	}

	stored, err := d.store.GetPresentation(resp.PresentOpenResult.PresentationID)
	if err != nil {
		t.Fatalf("GetPresentation: %v", err)
	}
	if stored.SessionID != "session-1" || stored.Title != "My Change" {
		t.Errorf("stored presentation = %+v, want session-1 / My Change", stored)
	}

	// A second open against the same presentation_id adds round 2, not a new
	// presentation.
	resp2 := callPresentOpen(t, d, &protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: "session-1",
		ManifestYaml:    presentManifestYAML("My Change", repoDir),
		PresentationID:  protocol.Ptr(resp.PresentOpenResult.PresentationID),
	})
	if !resp2.Ok || resp2.PresentOpenResult == nil {
		t.Fatalf("second present open response = %+v, want ok", resp2)
	}
	if resp2.PresentOpenResult.PresentationID != resp.PresentOpenResult.PresentationID {
		t.Error("second open on an explicit presentation_id created a different presentation")
	}
	if resp2.PresentOpenResult.Seq != 2 {
		t.Errorf("second round seq = %d, want 2", resp2.PresentOpenResult.Seq)
	}
}

func TestHandlePresentOpen_BadYAML(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	resp := callPresentOpen(t, d, &protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: "session-1",
		ManifestYaml:    "not: valid: manifest: yaml: [",
	})
	if resp.Ok {
		t.Fatalf("present open with malformed YAML returned ok: %+v", resp)
	}
}

func TestHandlePresentOpen_UnknownPresentationID(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	repoDir, _ := presentTestRepo(t)

	resp := callPresentOpen(t, d, &protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: "session-1",
		ManifestYaml:    presentManifestYAML("My Change", repoDir),
		PresentationID:  protocol.Ptr("no-such-presentation"),
	})
	if resp.Ok {
		t.Fatalf("present open against an unknown presentation_id returned ok: %+v", resp)
	}
}

func TestHandlePresentOpen_WrongSessionRejected(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	repoDir, _ := presentTestRepo(t)

	opened := callPresentOpen(t, d, &protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: "session-a",
		ManifestYaml:    presentManifestYAML("My Change", repoDir),
	})
	if !opened.Ok || opened.PresentOpenResult == nil {
		t.Fatalf("setup present open response = %+v, want ok", opened)
	}

	resp := callPresentOpen(t, d, &protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: "session-b",
		ManifestYaml:    presentManifestYAML("My Change", repoDir),
		PresentationID:  protocol.Ptr(opened.PresentOpenResult.PresentationID),
	})
	if resp.Ok {
		t.Fatalf("present open from a different session onto another's presentation returned ok: %+v", resp)
	}
}

func TestHandlePresentSubmitRound_ValidationRejects(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	repoDir, _ := presentTestRepo(t)

	opened := callPresentOpen(t, d, &protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: "session-1",
		ManifestYaml:    presentManifestYAML("My Change", repoDir),
	})
	if !opened.Ok || opened.PresentOpenResult == nil {
		t.Fatalf("setup present open response = %+v, want ok", opened)
	}
	roundID := opened.PresentOpenResult.RoundID

	cases := []struct {
		name    string
		comment protocol.PresentCommentInput
	}{
		{"bad side", protocol.PresentCommentInput{Filepath: "a.txt", LineStart: 1, LineEnd: 1, Side: "sideways", Content: "x"}},
		{"line_start < 1", protocol.PresentCommentInput{Filepath: "a.txt", LineStart: 0, LineEnd: 1, Side: "new", Content: "x"}},
		{"line_end < line_start", protocol.PresentCommentInput{Filepath: "a.txt", LineStart: 5, LineEnd: 1, Side: "new", Content: "x"}},
		{"empty content", protocol.PresentCommentInput{Filepath: "a.txt", LineStart: 1, LineEnd: 1, Side: "new", Content: "   "}},
		{"empty filepath", protocol.PresentCommentInput{Filepath: "   ", LineStart: 1, LineEnd: 1, Side: "new", Content: "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &wsClient{send: make(chan outboundMessage, 4)}
			d.handlePresentSubmitRound(client, &protocol.PresentSubmitRoundMessage{
				Cmd:      protocol.CmdPresentSubmitRound,
				RoundID:  roundID,
				Comments: []protocol.PresentCommentInput{tc.comment},
			})
			var res protocol.PresentSubmitRoundResultMessage
			readTicketResult(t, client.send, &res)
			if res.Success {
				t.Fatalf("submit with invalid comment (%s) returned success", tc.name)
			}
		})
	}

	// The round was never actually submitted by any of the rejected attempts.
	round, err := d.store.GetPresentationRound(opened.PresentOpenResult.PresentationID, 0)
	if err != nil {
		t.Fatalf("GetPresentationRound: %v", err)
	}
	if round.SubmittedAt != nil {
		t.Fatalf("round was marked submitted despite every submission being rejected: %+v", round)
	}
}

func TestHandlePresentSubmitRound_DoubleSubmitRejected(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	repoDir, _ := presentTestRepo(t)

	opened := callPresentOpen(t, d, &protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: "session-1",
		ManifestYaml:    presentManifestYAML("My Change", repoDir),
	})
	if !opened.Ok || opened.PresentOpenResult == nil {
		t.Fatalf("setup present open response = %+v, want ok", opened)
	}
	roundID := opened.PresentOpenResult.RoundID

	submit := func() protocol.PresentSubmitRoundResultMessage {
		client := &wsClient{send: make(chan outboundMessage, 4)}
		d.handlePresentSubmitRound(client, &protocol.PresentSubmitRoundMessage{
			Cmd:     protocol.CmdPresentSubmitRound,
			RoundID: roundID,
			Comments: []protocol.PresentCommentInput{
				{Filepath: "a.txt", LineStart: 1, LineEnd: 1, Side: "new", Content: "looks good"},
			},
		})
		var res protocol.PresentSubmitRoundResultMessage
		readTicketResult(t, client.send, &res)
		return res
	}

	first := submit()
	if !first.Success {
		t.Fatalf("first submit = %+v, want success", first)
	}
	second := submit()
	if second.Success {
		t.Fatalf("second submit of an already-submitted round returned success: %+v", second)
	}
}

func TestHandleGetPresentationRound_CarriesRepoHeadSHA(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	repoDir, headSHA := presentTestRepo(t)

	opened := callPresentOpen(t, d, &protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: "session-1",
		ManifestYaml:    presentManifestYAML("My Change", repoDir),
	})
	if !opened.Ok || opened.PresentOpenResult == nil {
		t.Fatalf("setup present open response = %+v, want ok", opened)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleGetPresentationRound(client, &protocol.GetPresentationRoundMessage{
		Cmd:            protocol.CmdGetPresentationRound,
		PresentationID: opened.PresentOpenResult.PresentationID,
	})
	var res protocol.GetPresentationRoundResultMessage
	readTicketResult(t, client.send, &res)
	if !res.Success {
		t.Fatalf("get_presentation_round = %+v, want success", res)
	}
	if res.RepoHeadSHA == nil || *res.RepoHeadSHA != headSHA {
		t.Errorf("repo_head_sha = %v, want %q", res.RepoHeadSHA, headSHA)
	}
}
