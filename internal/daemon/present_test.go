package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// presentStatsTestRepo creates a base commit with a.txt and img.png (a fake
// binary blob), then a head commit that grows a.txt, adds b.txt, and
// modifies img.png — so tests can assert numstat-derived additions/deletions
// per file, including the binary-file omission case.
func presentStatsTestRepo(t *testing.T) (dir, baseSHA, headSHA string) {
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

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	// A NUL byte marks this blob as binary to git, without needing a real PNG.
	if err := os.WriteFile(filepath.Join(dir, "img.png"), []byte{0x89, 0x00, 0x50, 0x4e}, 0o644); err != nil {
		t.Fatalf("write img.png: %v", err)
	}
	run("add", "a.txt", "img.png")
	run("commit", "-m", "base")
	baseSHA = run("rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line1\nline2\nline3\nline4\n"), 0o644); err != nil {
		t.Fatalf("update a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new file\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "img.png"), []byte{0x89, 0x00, 0x50, 0x4e, 0x01, 0x02}, 0o644); err != nil {
		t.Fatalf("update img.png: %v", err)
	}
	run("add", "a.txt", "b.txt", "img.png")
	run("commit", "-m", "head")
	headSHA = run("rev-parse", "HEAD")

	return dir, baseSHA, headSHA
}

func TestHandleGetPresentationRound_CarriesFileStats(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	repoDir, baseSHA, headSHA := presentStatsTestRepo(t)

	manifestYAML := fmt.Sprintf(
		"version: 1\nkind: changes\ntitle: %q\nframe:\n  repo: %q\n  base: %q\n  head: %q\nfiles:\n  - path: a.txt\n  - path: b.txt\n  - path: img.png\n",
		"Stats Change", repoDir, baseSHA, headSHA,
	)

	opened := callPresentOpen(t, d, &protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: "session-1",
		ManifestYaml:    manifestYAML,
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
	if !res.Success || res.Round == nil {
		t.Fatalf("get_presentation_round = %+v, want success with a round", res)
	}

	byPath := make(map[string]protocol.PresentFile, len(res.Round.Manifest.Files))
	for _, f := range res.Round.Manifest.Files {
		byPath[f.Path] = f
	}

	aTxt, ok := byPath["a.txt"]
	if !ok {
		t.Fatalf("a.txt missing from manifest files: %+v", byPath)
	}
	if aTxt.Additions == nil || *aTxt.Additions != 2 || aTxt.Deletions == nil || *aTxt.Deletions != 0 {
		t.Errorf("a.txt stats = +%v/-%v, want +2/-0", derefIntOrNil(aTxt.Additions), derefIntOrNil(aTxt.Deletions))
	}

	bTxt, ok := byPath["b.txt"]
	if !ok {
		t.Fatalf("b.txt missing from manifest files: %+v", byPath)
	}
	if bTxt.Additions == nil || *bTxt.Additions != 1 || bTxt.Deletions == nil || *bTxt.Deletions != 0 {
		t.Errorf("b.txt stats = +%v/-%v, want +1/-0", derefIntOrNil(bTxt.Additions), derefIntOrNil(bTxt.Deletions))
	}

	imgPNG, ok := byPath["img.png"]
	if !ok {
		t.Fatalf("img.png missing from manifest files: %+v", byPath)
	}
	if imgPNG.Additions != nil || imgPNG.Deletions != nil {
		t.Errorf("img.png (binary) stats = +%v/-%v, want absent", derefIntOrNil(imgPNG.Additions), derefIntOrNil(imgPNG.Deletions))
	}
}

func TestHandleGetPresentationRound_CarriesChangedFiles(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	repoDir, baseSHA, headSHA := presentStatsTestRepo(t)

	// Manifest only names a.txt and img.png — b.txt changed in the round but
	// is not in the tour, so it should still show up in changed_files.
	manifestYAML := fmt.Sprintf(
		"version: 1\nkind: changes\ntitle: %q\nframe:\n  repo: %q\n  base: %q\n  head: %q\nfiles:\n  - path: a.txt\n  - path: img.png\n",
		"Changed Files", repoDir, baseSHA, headSHA,
	)

	opened := callPresentOpen(t, d, &protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: "session-1",
		ManifestYaml:    manifestYAML,
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
	if !res.Success || res.Round == nil {
		t.Fatalf("get_presentation_round = %+v, want success with a round", res)
	}

	byPath := make(map[string]protocol.PresentFile, len(res.Round.ChangedFiles))
	for _, f := range res.Round.ChangedFiles {
		byPath[f.Path] = f
	}

	bTxt, ok := byPath["b.txt"]
	if !ok {
		t.Fatalf("changed_files missing b.txt (not in manifest): %+v", byPath)
	}
	if bTxt.Additions == nil || *bTxt.Additions != 1 || bTxt.Deletions == nil || *bTxt.Deletions != 0 {
		t.Errorf("b.txt stats = +%v/-%v, want +1/-0", derefIntOrNil(bTxt.Additions), derefIntOrNil(bTxt.Deletions))
	}

	imgPNG, ok := byPath["img.png"]
	if !ok {
		t.Fatalf("changed_files missing img.png: %+v", byPath)
	}
	if imgPNG.Additions != nil || imgPNG.Deletions != nil {
		t.Errorf("img.png (binary) stats = +%v/-%v, want absent", derefIntOrNil(imgPNG.Additions), derefIntOrNil(imgPNG.Deletions))
	}

	if _, ok := byPath["a.txt"]; !ok {
		t.Errorf("changed_files missing a.txt (also in manifest): %+v", byPath)
	}
}

func TestHandleGetPresentationRound_ChangedFilesNilOnBogusSHA(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	repoDir, _ := presentTestRepo(t)

	opened := callPresentOpen(t, d, &protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: "session-1",
		ManifestYaml:    presentManifestYAML("Bogus SHA", repoDir),
	})
	if !opened.Ok || opened.PresentOpenResult == nil {
		t.Fatalf("setup present open response = %+v, want ok", opened)
	}

	// Remove the repo out from under the pinned round so its `git diff`
	// fails, without disturbing the round's ability to load otherwise (the
	// manifest and comments come from the store, not the repo on disk).
	if err := os.RemoveAll(repoDir); err != nil {
		t.Fatalf("remove repo dir: %v", err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleGetPresentationRound(client, &protocol.GetPresentationRoundMessage{
		Cmd:            protocol.CmdGetPresentationRound,
		PresentationID: opened.PresentOpenResult.PresentationID,
	})
	var res protocol.GetPresentationRoundResultMessage
	readTicketResult(t, client.send, &res)
	if !res.Success || res.Round == nil {
		t.Fatalf("get_presentation_round = %+v, want success with a round despite bogus SHAs", res)
	}
	if res.Round.ChangedFiles != nil {
		t.Errorf("changed_files = %+v, want nil on git diff failure", res.Round.ChangedFiles)
	}
}

func derefIntOrNil(p *int) string {
	if p == nil {
		return "<nil>"
	}
	return strconv.Itoa(*p)
}

func TestParsePresentNumstat(t *testing.T) {
	input := "2\t0\ta.txt\n1\t0\tb.txt\n-\t-\timg.png\n3\t1\told.txt => new.txt\n4\t2\t{old => new}/renamed.txt\n"

	stats := parsePresentNumstat(input)

	if got, want := stats["a.txt"], [2]int{2, 0}; got != want {
		t.Errorf("a.txt = %v, want %v", got, want)
	}
	if got, want := stats["b.txt"], [2]int{1, 0}; got != want {
		t.Errorf("b.txt = %v, want %v", got, want)
	}
	if _, ok := stats["img.png"]; ok {
		t.Errorf("binary file img.png should be omitted, got %v", stats["img.png"])
	}
	if len(stats) != 2 {
		t.Errorf("expected only 2 non-binary, non-rename entries, got %v", stats)
	}
}
