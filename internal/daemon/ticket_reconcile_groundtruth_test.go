package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestExtractPRRefs(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []int
	}{
		{"empty", "", nil},
		{
			"hash ref",
			"still need to merge #462 before closing",
			[]int{462},
		},
		{
			"PR word ref",
			"waiting on PR 462 to land",
			[]int{462},
		},
		{
			"github url ref",
			"see https://github.com/victorarias/attn/pull/462 for details",
			[]int{462},
		},
		{
			"dedupes and preserves first-seen order",
			"mentions #17 twice: once here #17, then PR 462, then #17 again",
			[]int{17, 462},
		},
		{
			"garbage guard drops absurdly large numbers",
			"see #123456789",
			nil,
		},
		{
			"mixed patterns ordered by position",
			"first PR 5 is done, then #3, then https://github.com/o/r/pull/9",
			[]int{5, 3, 9},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractPRRefs(tc.text)
			if !equalIntSlices(got, tc.want) {
				t.Errorf("extractPRRefs(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func groundTruthTestPR(number int, state, title string) *protocol.PR {
	return &protocol.PR{
		ID:     "github.com:victorarias/attn#" + strconv.Itoa(number),
		Host:   "github.com",
		Repo:   "victorarias/attn",
		Number: number,
		Title:  title,
		State:  state,
	}
}

func TestReconcileGroundTruthLines(t *testing.T) {
	prs := []*protocol.PR{
		groundTruthTestPR(462, "merged", "Fix the thing"),
		groundTruthTestPR(470, "closed", "Abandoned approach"),
		groundTruthTestPR(480, "open", "Still cooking"),
	}

	t.Run("merged PR is annotated", func(t *testing.T) {
		lines, _ := reconcileGroundTruthLines([]int{462}, "victorarias/attn", prs)
		if len(lines) != 1 {
			t.Fatalf("lines = %v, want 1", lines)
		}
		if !strings.Contains(lines[0], "PR #462 is merged") || !strings.Contains(lines[0], "Fix the thing") {
			t.Fatalf("unexpected line: %q", lines[0])
		}
	})

	t.Run("closed PR is annotated", func(t *testing.T) {
		lines, _ := reconcileGroundTruthLines([]int{470}, "victorarias/attn", prs)
		if len(lines) != 1 || !strings.Contains(lines[0], "PR #470 is closed") {
			t.Fatalf("lines = %v, want one closed annotation", lines)
		}
	})

	t.Run("open PR is silent", func(t *testing.T) {
		lines, _ := reconcileGroundTruthLines([]int{480}, "victorarias/attn", prs)
		if len(lines) != 0 {
			t.Fatalf("lines = %v, want none (open PR)", lines)
		}
	})

	t.Run("untracked PR number is silent", func(t *testing.T) {
		lines, _ := reconcileGroundTruthLines([]int{999}, "victorarias/attn", prs)
		if len(lines) != 0 {
			t.Fatalf("lines = %v, want none (untracked)", lines)
		}
	})

	t.Run("empty repo slug yields nil", func(t *testing.T) {
		if lines, _ := reconcileGroundTruthLines([]int{462}, "", prs); lines != nil {
			t.Fatalf("lines = %v, want nil", lines)
		}
	})

	t.Run("empty pr list yields nil", func(t *testing.T) {
		if lines, _ := reconcileGroundTruthLines([]int{462}, "victorarias/attn", nil); lines != nil {
			t.Fatalf("lines = %v, want nil", lines)
		}
	})

	t.Run("caps at 5 lines", func(t *testing.T) {
		var manyPRs []*protocol.PR
		var refs []int
		for i := 1; i <= 8; i++ {
			manyPRs = append(manyPRs, groundTruthTestPR(i, "merged", "t"))
			refs = append(refs, i)
		}
		lines, lineCap := reconcileGroundTruthLines(refs, "victorarias/attn", manyPRs)
		if len(lines) != groundTruthMaxLines {
			t.Fatalf("lines = %d, want %d (cap)", len(lines), groundTruthMaxLines)
		}
		if !lineCap {
			t.Fatalf("lineCap = false, want true when %d refs exceed the %d-line cap", len(refs), groundTruthMaxLines)
		}
	})
}

// The end-to-end wiring test: a verdict mentioning a PR that the store
// already knows is merged gets a Ground-truth check line appended to the
// posted reconciliation comment, without touching the verdict fields
// themselves (status still doesn't move; What's left / Evidence text is
// unchanged).
func TestReconcileGroundTruthAnnotatesMergedPR(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	repoDir := t.TempDir()
	runGitDaemon(t, repoDir, "init")
	runGitDaemon(t, repoDir, "remote", "add", "origin", "git@github.com:victorarias/attn.git")

	ticketID := "gt-ticket"
	if _, err := d.store.CreateTicket(store.Ticket{
		ID:       ticketID,
		Title:    "Ship the fix",
		Assignee: "sess-dead",
		Status:   store.TicketStatusInReview,
		Cwd:      repoDir,
	}, "chief", time.Now()); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	pr := groundTruthTestPR(462, "merged", "Fix the offset bug")
	d.store.AddPR(pr)

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{
			StructuredOutput: []byte(`{"assessment":"partial","confidence":"medium","whats_left":"merge PR #462 pending","evidence":"last turn was still waiting on CI"}`),
			TotalCostUSD:     0.05,
			NumTurns:         2,
		}, nil
	}

	if err := d.reconcileTaskExecutor(context.Background(), reconcileTask(ticketReconcileInputs{
		TicketID:       ticketID,
		Title:          "Ship the fix",
		Brief:          "Land the fix.",
		StatusAtClaim:  store.TicketStatusInReview,
		SessionID:      "sess-dead",
		Agent:          "codex",
		TranscriptPath: transcript,
		CloseContext:   "found orphaned by the periodic sweep",
	})); err != nil {
		t.Fatalf("reconcileTaskExecutor: %v", err)
	}

	comments := reconcileComments(t, d, ticketID)
	if len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
	comment := comments[0]
	if !strings.Contains(comment, "What's left: merge PR #462 pending") {
		t.Fatalf("verdict text was altered, comment:\n%s", comment)
	}
	if !strings.Contains(comment, "Ground-truth check: PR #462 is merged") {
		t.Fatalf("missing ground-truth annotation, comment:\n%s", comment)
	}
	if !strings.Contains(comment, "Fix the offset bug") {
		t.Fatalf("annotation missing PR title, comment:\n%s", comment)
	}

	ticket, _ := d.store.GetTicket(ticketID)
	if ticket.Status != store.TicketStatusInReview {
		t.Fatalf("status = %q, want in_review (annotation never moves the column)", ticket.Status)
	}
}

// A verdict referencing a PR that is still open produces no annotation.
func TestReconcileGroundTruthSilentWhenPROpen(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	repoDir := t.TempDir()
	runGitDaemon(t, repoDir, "init")
	runGitDaemon(t, repoDir, "remote", "add", "origin", "git@github.com:victorarias/attn.git")

	ticketID := "gt-ticket-open"
	if _, err := d.store.CreateTicket(store.Ticket{
		ID:       ticketID,
		Title:    "Ship the fix",
		Assignee: "sess-dead",
		Status:   store.TicketStatusInReview,
		Cwd:      repoDir,
	}, "chief", time.Now()); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	d.store.AddPR(groundTruthTestPR(462, "waiting", "Fix the offset bug"))

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{
			StructuredOutput: []byte(`{"assessment":"partial","confidence":"medium","whats_left":"merge PR #462 pending","evidence":"still waiting"}`),
		}, nil
	}

	if err := d.reconcileTaskExecutor(context.Background(), reconcileTask(ticketReconcileInputs{
		TicketID:       ticketID,
		Title:          "Ship the fix",
		Brief:          "Land the fix.",
		StatusAtClaim:  store.TicketStatusInReview,
		SessionID:      "sess-dead",
		Agent:          "codex",
		TranscriptPath: transcript,
		CloseContext:   "found orphaned by the periodic sweep",
	})); err != nil {
		t.Fatalf("reconcileTaskExecutor: %v", err)
	}

	comments := reconcileComments(t, d, ticketID)
	if len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
	if strings.Contains(comments[0], "Ground-truth check") {
		t.Fatalf("unexpected ground-truth annotation for still-tracked PR, comment:\n%s", comments[0])
	}
}

// runGroundTruthReconcile drives the executor for a fresh daemon whose ticket
// cwd is a temp git repo with a github.com origin, using a verdict whose
// What's left is the given text, and returns the single posted reconcile
// comment. Callers arm d.ticketReconcilePRFetch (or seed tracked PRs) first.
func runGroundTruthReconcile(t *testing.T, d *Daemon, ticketID, whatsLeft string) string {
	t.Helper()

	repoDir := t.TempDir()
	runGitDaemon(t, repoDir, "init")
	runGitDaemon(t, repoDir, "remote", "add", "origin", "git@github.com:victorarias/attn.git")

	if _, err := d.store.CreateTicket(store.Ticket{
		ID:       ticketID,
		Title:    "Ship the fix",
		Assignee: "sess-dead",
		Status:   store.TicketStatusInReview,
		Cwd:      repoDir,
	}, "chief", time.Now()); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	verdict := `{"assessment":"partial","confidence":"medium","whats_left":` +
		strconv.Quote(whatsLeft) + `,"evidence":"from the transcript"}`
	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{StructuredOutput: []byte(verdict)}, nil
	}

	if err := d.reconcileTaskExecutor(context.Background(), reconcileTask(ticketReconcileInputs{
		TicketID:       ticketID,
		Title:          "Ship the fix",
		Brief:          "Land the fix.",
		StatusAtClaim:  store.TicketStatusInReview,
		SessionID:      "sess-dead",
		Agent:          "codex",
		TranscriptPath: transcript,
		CloseContext:   "found orphaned by the periodic sweep",
	})); err != nil {
		t.Fatalf("reconcileTaskExecutor: %v", err)
	}

	comments := reconcileComments(t, d, ticketID)
	if len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
	return comments[0]
}

// A referenced PR absent from the tracked open set gets one targeted lookup;
// merged=true produces the annotation. This is the production shape of the
// original bug: merged PRs vanish from the is:open poller sweep, so absence +
// lookup is the only path that fires against live data.
func TestReconcileGroundTruthLooksUpUntrackedMergedRef(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	var gotRepo string
	var gotNumber int
	d.ticketReconcilePRFetch = func(repo string, number int) (string, bool, string, error) {
		gotRepo, gotNumber = repo, number
		return "closed", true, "Fix the offset bug", nil
	}

	comment := runGroundTruthReconcile(t, d, "gt-untracked-merged", "merge PR #462 pending")
	if !strings.Contains(comment, "Ground-truth check: PR #462 is merged") ||
		!strings.Contains(comment, "Fix the offset bug") {
		t.Fatalf("missing merged annotation, comment:\n%s", comment)
	}
	if gotRepo != "victorarias/attn" || gotNumber != 462 {
		t.Fatalf("fetcher called with (%q, %d), want (victorarias/attn, 462)", gotRepo, gotNumber)
	}
}

// closed-but-not-merged also annotates, as "closed".
func TestReconcileGroundTruthLooksUpUntrackedClosedRef(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ticketReconcilePRFetch = func(repo string, number int) (string, bool, string, error) {
		return "closed", false, "Abandoned approach", nil
	}

	comment := runGroundTruthReconcile(t, d, "gt-untracked-closed", "close out #470")
	if !strings.Contains(comment, "Ground-truth check: PR #470 is closed") {
		t.Fatalf("missing closed annotation, comment:\n%s", comment)
	}
}

// A lookup error degrades to silence — the annotation only fires on positive
// knowledge.
func TestReconcileGroundTruthUntrackedFetchErrorSilent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ticketReconcilePRFetch = func(repo string, number int) (string, bool, string, error) {
		return "", false, "", errors.New("boom")
	}

	comment := runGroundTruthReconcile(t, d, "gt-untracked-err", "merge PR #462 pending")
	if strings.Contains(comment, "Ground-truth check") {
		t.Fatalf("unexpected annotation on fetch error, comment:\n%s", comment)
	}
}

// A still-open lookup result is silent.
func TestReconcileGroundTruthUntrackedOpenSilent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ticketReconcilePRFetch = func(repo string, number int) (string, bool, string, error) {
		return "open", false, "Still cooking", nil
	}

	comment := runGroundTruthReconcile(t, d, "gt-untracked-open", "merge PR #462 pending")
	if strings.Contains(comment, "Ground-truth check") {
		t.Fatalf("unexpected annotation for open PR, comment:\n%s", comment)
	}
}

// No fetch seam and no registered GitHub client: the lookup leg is skipped
// entirely and the comment posts without annotations.
func TestReconcileGroundTruthUntrackedNoClientSilent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	comment := runGroundTruthReconcile(t, d, "gt-untracked-noclient", "merge PR #462 pending")
	if strings.Contains(comment, "Ground-truth check") {
		t.Fatalf("unexpected annotation without a GitHub client, comment:\n%s", comment)
	}
}

// The lookup leg is capped at groundTruthMaxLookups calls per reconcile.
func TestGroundTruthUntrackedLinesCapsLookups(t *testing.T) {
	calls := 0
	fetch := func(repo string, number int) (string, bool, string, error) {
		calls++
		return "closed", true, "t", nil
	}

	refs := []int{1, 2, 3, 4, 5, 6}
	lines, caps := groundTruthUntrackedLines(context.Background(), refs, nil, "victorarias/attn", fetch)
	if calls != groundTruthMaxLookups {
		t.Fatalf("fetch calls = %d, want %d (cap)", calls, groundTruthMaxLookups)
	}
	if len(lines) != groundTruthMaxLookups {
		t.Fatalf("lines = %d, want %d", len(lines), groundTruthMaxLookups)
	}
	if !caps.lookupCap {
		t.Fatalf("caps.lookupCap = false, want true after hitting the lookup cap")
	}
}

// Tracked refs never consume lookups: the deterministic leg owns them.
func TestGroundTruthUntrackedLinesSkipsTrackedRefs(t *testing.T) {
	calls := 0
	fetch := func(repo string, number int) (string, bool, string, error) {
		calls++
		return "closed", true, "t", nil
	}

	lines, _ := groundTruthUntrackedLines(context.Background(), []int{1, 2, 3},
		map[int]bool{1: true, 2: true}, "victorarias/attn", fetch)
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want 1 (only the untracked ref)", calls)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "PR #3") {
		t.Fatalf("lines = %v, want one line for PR #3", lines)
	}
}

// An expired context stops the lookup leg immediately.
func TestGroundTruthUntrackedLinesRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	fetch := func(repo string, number int) (string, bool, string, error) {
		calls++
		return "closed", true, "t", nil
	}
	lines, caps := groundTruthUntrackedLines(ctx, []int{1, 2}, nil, "victorarias/attn", fetch)
	if len(lines) != 0 {
		t.Fatalf("lines = %v, want none under cancelled ctx", lines)
	}
	if calls != 0 {
		t.Fatalf("fetch calls = %d, want 0 under cancelled ctx", calls)
	}
	if !caps.timeout {
		t.Fatalf("caps.timeout = false, want true under cancelled ctx")
	}
}
