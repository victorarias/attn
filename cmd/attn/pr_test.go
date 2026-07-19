package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// snapshotPayload wraps GraphQL fragments in the envelope gh api graphql emits.
func snapshotPayload(head, checks, reviews, comments string) []byte {
	if checks == "" {
		checks = `{"__typename":"CheckRun","name":"CI","status":"COMPLETED","conclusion":"SUCCESS"}`
	}
	return fmt.Appendf(nil, `{"data":{"repository":{"pullRequest":{
      "number":404,"state":"OPEN","isDraft":false,"headRefOid":%q,
      "commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{
        "pageInfo":{"hasNextPage":false},"nodes":[%s]}}}}]},
      "reviews":{"nodes":[%s]},
      "comments":{"nodes":[%s]}
    }}}}`, head, checks, reviews, comments)
}

// reviewNode builds one review. GitHub attaches every inline comment to a
// freshly submitted review, including a reply to a long-dormant thread, and
// wraps a standalone inline comment in a review with no body of its own.
func reviewNode(id, state, body, at, author, oid, inline string) string {
	return fmt.Sprintf(`{"id":%q,"state":%q,"bodyText":%q,"submittedAt":%q,
	  "author":{"__typename":"User","login":%q},"commit":{"oid":%q},
	  "comments":{"pageInfo":{"hasNextPage":false},"nodes":[%s]}}`,
		id, state, body, at, author, oid, inline)
}

func inlineNode(id, at, author string) string {
	return fmt.Sprintf(`{"id":%q,"createdAt":%q,"author":{"__typename":"User","login":%q}}`, id, at, author)
}

func TestParsePRSnapshotRequiresGreenChecksAndCurrentHeadApproval(t *testing.T) {
	head := strings.Repeat("a", 40)
	oldHead := strings.Repeat("b", 40)
	checks := `{"__typename":"CheckRun","name":"Daemon","status":"COMPLETED","conclusion":"SUCCESS"},
	           {"__typename":"CheckRun","name":"Frontend","status":"COMPLETED","conclusion":"SKIPPED"},
	           {"__typename":"StatusContext","context":"license","state":"SUCCESS"}`
	reviews := strings.Join([]string{
		reviewNode("r1", "APPROVED", "", "2026-07-19T10:00:00Z", "figgyster", oldHead, ""),
		reviewNode("r2", "CHANGES_REQUESTED", "", "2026-07-19T11:00:00Z", "figgyster", head, ""),
		reviewNode("r3", "APPROVED", "", "2026-07-19T12:00:00Z", "Figgyster", head, ""),
	}, ",")

	opts := prWaitOptions{Reviewer: "figgyster"}
	readiness, err := parsePRSnapshot(snapshotPayload(head, checks, reviews, ""), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !readiness.ready() || readiness.CheckState != checksGreen || readiness.ReviewState != "approved" || len(readiness.Checks) != 3 {
		t.Fatalf("readiness = %#v", readiness)
	}

	// The same reviews bound to a superseded commit must not satisfy the gate.
	staleReviews := strings.ReplaceAll(reviews, `"oid":"`+head+`"`, `"oid":"`+oldHead+`"`)
	readiness, err = parsePRSnapshot(snapshotPayload(head, checks, staleReviews, ""), opts)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.ReviewState != "waiting" || readiness.ready() {
		t.Fatalf("old-head approval satisfied gate: %#v", readiness)
	}
}

func TestParsePRSnapshotCollectsHumanCommentsAndDropsBots(t *testing.T) {
	head := strings.Repeat("a", 40)
	reviews := reviewNode("r1", "COMMENTED", "a real review remark", "2026-07-19T10:00:00Z", "figgyster", head,
		inlineNode("t1", "2026-07-19T11:00:00Z", "figgyster"))
	comments := `{"id":"c1","createdAt":"2026-07-19T09:00:00Z","author":{"__typename":"User","login":"victorarias"}},
	             {"id":"c2","createdAt":"2026-07-19T09:30:00Z","author":{"__typename":"Bot","login":"chatgpt-codex-connector"}},
	             {"id":"c3","createdAt":"2026-07-19T09:45:00Z","author":{"__typename":"User","login":"noisy-human"}}`

	opts := prWaitOptions{Reviewer: "figgyster", IgnoreAuthors: []string{"NOISY-HUMAN"}}
	readiness, err := parsePRSnapshot(snapshotPayload(head, "", reviews, comments), opts)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, comment := range readiness.Comments {
		got = append(got, comment.ID+":"+comment.Kind+":"+comment.Author)
	}
	// Sorted oldest-first; the bot and the ignored author are gone.
	want := []string{"c1:issue:victorarias", "r1:review:figgyster", "t1:inline:figgyster"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("comments = %v, want %v", got, want)
	}
	// A COMMENTED review carries no verdict and must not move the review state.
	if readiness.ReviewState != "waiting" {
		t.Fatalf("COMMENTED review changed review state: %#v", readiness)
	}
}

// GitHub wraps a standalone inline comment in a bodyless COMMENTED review.
// Reporting both made one remark read as "2 new comments".
func TestParsePRSnapshotDoesNotDoubleCountInlineCommentWrapper(t *testing.T) {
	head := strings.Repeat("a", 40)
	wrapper := reviewNode("r1", "COMMENTED", "", "2026-07-19T19:33:19Z", "victorarias", head,
		inlineNode("t1", "2026-07-19T19:33:19Z", "victorarias"))

	readiness, err := parsePRSnapshot(snapshotPayload(head, "", wrapper, ""), prWaitOptions{Reviewer: "figgyster"})
	if err != nil {
		t.Fatal(err)
	}
	if len(readiness.Comments) != 1 || readiness.Comments[0].ID != "t1" || readiness.Comments[0].Kind != "inline" {
		t.Fatalf("comments = %#v, want the inline comment alone", readiness.Comments)
	}
}

// A reply to a thread older than any newest-N slice of reviewThreads must still
// be seen. GitHub attaches the reply to a freshly submitted review, so sourcing
// inline comments from reviews rather than threads makes thread age irrelevant.
func TestParsePRSnapshotSeesReplyOnLongDormantThread(t *testing.T) {
	head := strings.Repeat("a", 40)
	nodes := make([]string, 0, 130)
	// 128 old reviews, each opening a thread, would push the first thread far
	// outside a 100-thread window.
	for i := range 128 {
		nodes = append(nodes, reviewNode(
			fmt.Sprintf("old-r%d", i), "COMMENTED", "", "2026-01-01T00:00:00Z", "victorarias", head,
			inlineNode(fmt.Sprintf("old-t%d", i), "2026-01-01T00:00:00Z", "victorarias")))
	}
	// The reply lands on thread old-t0, but arrives as the newest review.
	nodes = append(nodes, reviewNode("reply-r", "COMMENTED", "", "2026-07-19T20:00:00Z", "figgyster", head,
		inlineNode("reply-t", "2026-07-19T20:00:00Z", "figgyster")))

	readiness, err := parsePRSnapshot(snapshotPayload(head, "", strings.Join(nodes, ","), ""), prWaitOptions{Reviewer: "figgyster"})
	if err != nil {
		t.Fatal(err)
	}

	var baseline []prComment
	for _, comment := range readiness.Comments {
		if comment.ID != "reply-t" {
			baseline = append(baseline, comment)
		}
	}
	seen := map[string]bool{}
	for _, comment := range baseline {
		seen[comment.ID] = true
	}
	fresh := unseenPRComments(readiness.Comments, seen)
	if len(fresh) != 1 || fresh[0].ID != "reply-t" {
		t.Fatalf("reply on a dormant thread was missed: fresh = %#v", fresh)
	}
}

func TestParsePRSnapshotFailsClosedOnReviewCommentTruncation(t *testing.T) {
	head := strings.Repeat("a", 40)
	review := reviewNode("r1", "COMMENTED", "", "2026-07-19T10:00:00Z", "victorarias", head,
		inlineNode("t1", "2026-07-19T10:00:00Z", "victorarias"))
	truncated := strings.Replace(review, `"pageInfo":{"hasNextPage":false}`, `"pageInfo":{"hasNextPage":true}`, 1)

	if _, err := parsePRSnapshot(snapshotPayload(head, "", truncated, ""), prWaitOptions{Reviewer: "figgyster"}); err == nil ||
		!strings.Contains(err.Error(), "without truncation") {
		t.Fatalf("err = %v", err)
	}
}

func TestParsePRSnapshotFailsClosedForUnknownCheckState(t *testing.T) {
	checks := `{"__typename":"CheckRun","name":"CI","status":"COMPLETED","conclusion":"NEW_STATE"}`
	readiness, err := parsePRSnapshot(snapshotPayload("abc", checks, "", ""), prWaitOptions{Reviewer: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if readiness.CheckState != checksPending {
		t.Fatalf("readiness = %#v", readiness)
	}
}

func TestParsePRSnapshotFailsClosedOnCheckTruncation(t *testing.T) {
	payload := snapshotPayload("abc", "", "", "")
	truncated := bytes.Replace(payload, []byte(`"hasNextPage":false`), []byte(`"hasNextPage":true`), 1)
	if _, err := parsePRSnapshot(truncated, prWaitOptions{Reviewer: "r"}); err == nil ||
		!strings.Contains(err.Error(), "without truncation") {
		t.Fatalf("err = %v", err)
	}
}

func TestParsePRSnapshotSurfacesGraphQLErrors(t *testing.T) {
	body := []byte(`{"data":{"repository":null},"errors":[{"message":"Could not resolve to a Repository"}]}`)
	if _, err := parsePRSnapshot(body, prWaitOptions{Reviewer: "r"}); err == nil ||
		!strings.Contains(err.Error(), "Could not resolve") {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitForPRActionableResetsAcrossHeadChangeAndSuppressesDuplicatePolls(t *testing.T) {
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)
	pendingA := readinessObservation("12", headA, checksPending, "waiting")
	greenA := readinessObservation("12", headA, checksGreen, "waiting")
	waitingB := readinessObservation("12", headB, checksGreen, "waiting")
	readyB := readinessObservation("12", headB, checksGreen, "approved")
	source := &fakeReadinessSource{results: []*prReadiness{pendingA, pendingA, greenA, waitingB, readyB}}
	opts := prWaitOptions{Number: 12, Owner: "owner", Name: "repo", Reviewer: "figgyster", Interval: 0}

	var output bytes.Buffer
	got, outcome, err := waitForPRActionable(context.Background(), source, opts, &output)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != outcomeApproved || got.HeadSHA != headB || source.calls != 5 {
		t.Fatalf("got=%#v outcome=%s calls=%d", got, outcome, source.calls)
	}
	text := output.String()
	if strings.Count(text, "head=aaaaaaaaaaaa state=open") != 2 {
		t.Fatalf("duplicate polls were not suppressed:\n%s", text)
	}
	for _, want := range []string{"head changed aaaaaaaaaaaa -> bbbbbbbbbbbb; reset", "review=approved reviewer=figgyster"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

// The motivating regression: a reviewer requesting changes used to fall through
// to the poll loop and only surface when the whole timeout expired.
func TestWaitForPRActionableReturnsPromptlyOnChangesRequested(t *testing.T) {
	head := strings.Repeat("c", 40)
	observation := readinessObservation("12", head, checksGreen, "changes_requested")
	source := &fakeReadinessSource{results: []*prReadiness{observation}}

	got, outcome, err := waitForPRActionable(context.Background(), source, prWaitOptions{Reviewer: "figgyster"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != outcomeChangesRequested || outcome.exitCode() != prWaitExitChangesRequested || got != observation {
		t.Fatalf("got=%#v outcome=%s", got, outcome)
	}
	if source.calls != 1 {
		t.Fatalf("polled %d times; changes_requested must return on the first observation", source.calls)
	}
}

func TestWaitForPRActionableBaselinesExistingCommentsAndWakesOnNewOnes(t *testing.T) {
	head := strings.Repeat("d", 40)
	existing := prComment{ID: "c1", Author: "victorarias", Kind: "issue", CreatedAt: time.Unix(1, 0)}
	fresh := prComment{ID: "c2", Author: "victorarias", Kind: "issue", CreatedAt: time.Unix(2, 0)}

	first := readinessObservation("12", head, checksPending, "waiting")
	first.Comments = []prComment{existing}
	second := readinessObservation("12", head, checksPending, "waiting")
	second.Comments = []prComment{existing}
	third := readinessObservation("12", head, checksPending, "waiting")
	third.Comments = []prComment{existing, fresh}
	source := &fakeReadinessSource{results: []*prReadiness{first, second, third}}

	got, outcome, err := waitForPRActionable(context.Background(), source, prWaitOptions{Reviewer: "figgyster"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != outcomeComment || source.calls != 3 {
		t.Fatalf("outcome=%s calls=%d", outcome, source.calls)
	}
	// Only the comment posted during the wait is reported, not the baseline.
	if len(got.Comments) != 1 || got.Comments[0].ID != "c2" {
		t.Fatalf("comments = %#v", got.Comments)
	}
}

func TestWaitForPRActionableReportsCheckFailureAndClosure(t *testing.T) {
	failed := readinessObservation("12", strings.Repeat("c", 40), checksFailed, "approved")
	_, outcome, err := waitForPRActionable(context.Background(), &fakeReadinessSource{results: []*prReadiness{failed}}, prWaitOptions{}, &bytes.Buffer{})
	if err != nil || outcome != outcomeChecksFailed || outcome.exitCode() != prWaitExitChecksFailed {
		t.Fatalf("outcome=%s err=%v", outcome, err)
	}

	closed := readinessObservation("12", strings.Repeat("c", 40), checksGreen, "waiting")
	closed.State = "merged"
	_, outcome, err = waitForPRActionable(context.Background(), &fakeReadinessSource{results: []*prReadiness{closed}}, prWaitOptions{}, &bytes.Buffer{})
	if err != nil || outcome != outcomeClosed || outcome.exitCode() != prWaitExitError {
		t.Fatalf("outcome=%s err=%v", outcome, err)
	}
}

func TestWaitForPRActionableReturnsTimeoutOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pending := readinessObservation("12", strings.Repeat("d", 40), checksPending, "waiting")
	source := &fakeReadinessSource{results: []*prReadiness{pending}}

	_, outcome, err := waitForPRActionable(ctx, source, prWaitOptions{Interval: time.Hour}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != outcomeTimeout || outcome.exitCode() != prWaitExitTimeout {
		t.Fatalf("outcome = %s", outcome)
	}
}

func TestReportPROutcomeWritesPlainTextAndJSON(t *testing.T) {
	head := strings.Repeat("e", 40)
	result := readinessObservation("12", head, checksGreen, "approved")

	var plain bytes.Buffer
	if code := reportPROutcome(result, outcomeApproved, prWaitOptions{}, &plain); code != prWaitExitApproved {
		t.Fatalf("exit code = %d", code)
	}
	if got := plain.String(); !strings.HasPrefix(got, "approved: figgyster approved eeeeeeeeeeee") {
		t.Fatalf("plain output = %q", got)
	}

	// Baseline comments on the observation are not news on a non-comment outcome.
	result.Comments = []prComment{{ID: "c1", Author: "victorarias", Kind: "issue"}}
	var encoded bytes.Buffer
	code := reportPROutcome(result, outcomeChangesRequested, prWaitOptions{JSON: true}, &encoded)
	if code != prWaitExitChangesRequested {
		t.Fatalf("exit code = %d", code)
	}
	var payload struct {
		Outcome  string      `json:"outcome"`
		Head     string      `json:"head"`
		Detail   string      `json:"detail"`
		Comments []prComment `json:"comments"`
		Review   struct {
			Reviewer string `json:"reviewer"`
		} `json:"review"`
	}
	if err := json.Unmarshal(encoded.Bytes(), &payload); err != nil {
		t.Fatalf("json = %q err = %v", encoded.String(), err)
	}
	if payload.Outcome != "changes_requested" || payload.Head != head || payload.Review.Reviewer != "figgyster" ||
		!strings.Contains(payload.Detail, "requested changes") {
		t.Fatalf("payload = %#v", payload)
	}
	if len(payload.Comments) != 0 {
		t.Fatalf("baseline comments reported as new: %#v", payload.Comments)
	}
}

func TestParsePRWaitArgs(t *testing.T) {
	opts, err := parsePRWaitArgs([]string{"https://github.com/VictorArias/attn/pull/602", "--reviewer", "figgyster", "--interval", "5s"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Owner != "victorarias" || opts.Name != "attn" || opts.Number != 602 ||
		opts.Reviewer != "figgyster" || opts.Interval != 5*time.Second {
		t.Fatalf("opts = %#v", opts)
	}

	opts, err = parsePRWaitArgs([]string{"602", "--repo", "ghe.example.com/victorarias/attn", "--reviewer", "figgyster",
		"--ignore-author", "bot-one", "--ignore-author", "bot-two", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Host != "ghe.example.com" || opts.Owner != "victorarias" || opts.Name != "attn" || !opts.JSON ||
		strings.Join(opts.IgnoreAuthors, ",") != "bot-one,bot-two" {
		t.Fatalf("opts = %#v", opts)
	}

	if _, err := parsePRWaitArgs([]string{"602", "--reviewer", "figgyster"}); err == nil || !strings.Contains(err.Error(), "--repo is required") {
		t.Fatalf("missing repo error = %v", err)
	}
	if _, err := parsePRWaitArgs([]string{"602", "--repo", "victorarias/attn"}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing reviewer error = %v", err)
	}
	if _, err := parsePRWaitArgs([]string{"602", "--repo", "attn", "--reviewer", "figgyster"}); err == nil ||
		!strings.Contains(err.Error(), "[host/]owner/repository") {
		t.Fatalf("malformed repo error = %v", err)
	}
}

func TestExecutePRCommandShowsSubcommandHelp(t *testing.T) {
	var stdout bytes.Buffer
	if code := executePRCommand([]string{"wait-ready", "--help"}, &stdout, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "exit: 0 approved") {
		t.Fatalf("help = %q", stdout.String())
	}
}

func readinessObservation(number, head, checks, review string) *prReadiness {
	return &prReadiness{
		Number: number, State: "open", HeadSHA: head, Checks: []prCheck{{Name: "check:CI", State: checks}},
		CheckState: checks, Reviewer: "figgyster", ReviewState: review,
	}
}
