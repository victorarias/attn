package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeReadinessSource struct {
	results []*prReadiness
	calls   int
}

func (f *fakeReadinessSource) Fetch(context.Context, prWaitOptions) (*prReadiness, error) {
	index := f.calls
	f.calls++
	if index >= len(f.results) {
		index = len(f.results) - 1
	}
	return f.results[index], nil
}

func TestParseGHPRReadinessRequiresGreenChecksAndCurrentHeadApproval(t *testing.T) {
	head := strings.Repeat("a", 40)
	oldHead := strings.Repeat("b", 40)
	output := `{
      "number":404,"state":"OPEN","isDraft":false,"headRefOid":"` + head + `",
      "statusCheckRollup":[
        {"__typename":"CheckRun","name":"Daemon","status":"COMPLETED","conclusion":"SUCCESS"},
        {"__typename":"CheckRun","name":"Frontend","status":"COMPLETED","conclusion":"SKIPPED"},
        {"__typename":"StatusContext","context":"license","state":"SUCCESS"}
      ],
      "reviews":[
        {"state":"APPROVED","submittedAt":"2026-07-19T10:00:00Z","author":{"login":"figgyster"},"commit":{"oid":"` + oldHead + `"}},
        {"state":"CHANGES_REQUESTED","submittedAt":"2026-07-19T11:00:00Z","author":{"login":"figgyster"},"commit":{"oid":"` + head + `"}},
        {"state":"APPROVED","submittedAt":"2026-07-19T12:00:00Z","author":{"login":"Figgyster"},"commit":{"oid":"` + head + `"}}
      ]
    }`
	readiness, err := parseGHPRReadiness([]byte(output), "figgyster")
	if err != nil {
		t.Fatal(err)
	}
	if !readiness.ready() || readiness.CheckState != checksGreen || readiness.ReviewState != "approved" || len(readiness.Checks) != 3 {
		t.Fatalf("readiness = %#v", readiness)
	}

	oldOnly := strings.Replace(output, `,"commit":{"oid":"`+head+`"}`, `,"commit":{"oid":"`+oldHead+`"}`, 2)
	readiness, err = parseGHPRReadiness([]byte(oldOnly), "figgyster")
	if err != nil {
		t.Fatal(err)
	}
	if readiness.ReviewState != "waiting" || readiness.ready() {
		t.Fatalf("old-head approval satisfied gate: %#v", readiness)
	}
}

func TestParseGHPRReadinessFailsClosedForUnknownCheckState(t *testing.T) {
	output := `{"number":1,"state":"OPEN","headRefOid":"abc","statusCheckRollup":[{"__typename":"CheckRun","name":"CI","status":"COMPLETED","conclusion":"NEW_STATE"}],"reviews":[]}`
	readiness, err := parseGHPRReadiness([]byte(output), "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if readiness.CheckState != checksPending {
		t.Fatalf("readiness = %#v", readiness)
	}
}

func TestParseGHPRReadinessFailsClosedAtGHCollectionLimit(t *testing.T) {
	check := `{"__typename":"CheckRun","name":"CI","status":"COMPLETED","conclusion":"SUCCESS"},`
	checks := strings.TrimSuffix(strings.Repeat(check, 100), ",")
	output := `{"number":1,"state":"OPEN","headRefOid":"abc","statusCheckRollup":[` + checks + `],"reviews":[]}`
	_, err := parseGHPRReadiness([]byte(output), "reviewer")
	if err == nil || !strings.Contains(err.Error(), "without truncation") {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitForPRReadyResetsAcrossHeadChangeAndSuppressesDuplicatePolls(t *testing.T) {
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)
	pendingA := readinessObservation("12", headA, checksPending, "waiting")
	greenA := readinessObservation("12", headA, checksGreen, "waiting")
	waitingB := readinessObservation("12", headB, checksGreen, "waiting")
	readyB := readinessObservation("12", headB, checksGreen, "approved")
	source := &fakeReadinessSource{results: []*prReadiness{pendingA, pendingA, greenA, waitingB, readyB}}
	opts := prWaitOptions{Target: "12", Repo: "owner/repo", Reviewer: "figgyster", Interval: 0}
	var output bytes.Buffer
	got, err := waitForPRReady(context.Background(), source, opts, &output)
	if err != nil {
		t.Fatal(err)
	}
	if got.HeadSHA != headB || !got.ready() || source.calls != 5 {
		t.Fatalf("got=%#v calls=%d", got, source.calls)
	}
	text := output.String()
	if strings.Count(text, "head=aaaaaaaaaaaa state=open") != 2 {
		t.Fatalf("duplicate polls were not suppressed:\n%s", text)
	}
	for _, want := range []string{"head changed aaaaaaaaaaaa -> bbbbbbbbbbbb; reset", "head=bbbbbbbbbbbb state=open", "review=approved reviewer=figgyster"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

func TestWaitForPRReadyFailsOnCurrentHeadCheckFailure(t *testing.T) {
	failed := readinessObservation("12", strings.Repeat("c", 40), checksFailed, "approved")
	source := &fakeReadinessSource{results: []*prReadiness{failed}}
	got, err := waitForPRReady(context.Background(), source, prWaitOptions{}, &bytes.Buffer{})
	if !errors.Is(err, errPRChecksFailed) || got != failed {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestWaitForPRReadyReturnsTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pending := readinessObservation("12", strings.Repeat("d", 40), checksPending, "waiting")
	source := &fakeReadinessSource{results: []*prReadiness{pending}}
	_, err := waitForPRReady(ctx, source, prWaitOptions{}, &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestParsePRWaitArgs(t *testing.T) {
	opts, err := parsePRWaitArgs([]string{"https://github.com/VictorArias/attn/pull/602", "--reviewer", "figgyster", "--interval", "5s"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Target != "https://github.com/victorarias/attn/pull/602" || opts.Reviewer != "figgyster" || opts.Interval != 5*time.Second {
		t.Fatalf("opts = %#v", opts)
	}
	if _, err := parsePRWaitArgs([]string{"602", "--reviewer", "figgyster"}); err == nil || !strings.Contains(err.Error(), "--repo is required") {
		t.Fatalf("missing repo error = %v", err)
	}
	if _, err := parsePRWaitArgs([]string{"602", "--repo", "victorarias/attn"}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing reviewer error = %v", err)
	}
}

func TestExecutePRCommandShowsSubcommandHelp(t *testing.T) {
	var stdout bytes.Buffer
	if code := executePRCommand([]string{"wait-ready", "--help"}, &stdout, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "exit: 0 ready") {
		t.Fatalf("help = %q", stdout.String())
	}
}

func readinessObservation(number, head, checks, review string) *prReadiness {
	return &prReadiness{
		Number: number, State: "open", HeadSHA: head, Checks: []prCheck{{Name: "check:CI", State: checks}},
		CheckState: checks, Reviewer: "figgyster", ReviewState: review,
	}
}
