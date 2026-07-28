package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// waitTuple runs a first-ever wait — no remembered position — and unpacks the
// result, for the tests that are about the outcome rather than the memory.
func waitTuple(ctx context.Context, source prReadinessSource, opts prWaitOptions, progress io.Writer) (*prReadiness, prOutcome, []prOutcome, error) {
	result, err := waitForPRActionable(ctx, source, opts, prWaitCursor{}, progress)
	return result.Observation, result.Outcome, result.Events, err
}

// snapshotPayload wraps GraphQL fragments in the envelope gh api graphql emits.
func snapshotPayload(head, checks, reviews, comments string) []byte {
	return snapshotPayloadWithRequests(head, checks, reviews, comments, "")
}

// snapshotPayloadWithRequests additionally sets the PR's requested reviewers,
// used to prove a stale verdict is suppressed while a re-review is pending.
func snapshotPayloadWithRequests(head, checks, reviews, comments, requests string) []byte {
	if checks == "" {
		checks = `{"__typename":"CheckRun","name":"CI","status":"COMPLETED","conclusion":"SUCCESS"}`
	}
	return fmt.Appendf(nil, `{"data":{"repository":{"pullRequest":{
      "number":404,"state":"OPEN","isDraft":false,"headRefOid":%q,
      "reviewRequests":{"nodes":[%s]},
      "commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{
        "pageInfo":{"hasNextPage":false},"nodes":[%s]}}}}]},
      "reviews":{"nodes":[%s]},
      "comments":{"nodes":[%s]}
    }}}}`, head, requests, checks, reviews, comments)
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

// reReviewObservation builds a readiness carrying the review-baseline fields the
// waiter uses to distinguish a stale verdict from a fresh one.
func reReviewObservation(head, checks, review string, requested bool, submitted, latest time.Time) *prReadiness {
	obs := readinessObservation("12", head, checks, review)
	obs.ReviewerRequested = requested
	obs.ReviewSubmittedAt = submitted
	obs.LatestReviewAt = latest
	return obs
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

// Bot comments are collected and tagged rather than dropped: they are their own
// event with their own exit code, so the caller decides whether to care.
// `--ignore-author` is the only thing that removes a comment outright.
func TestParsePRSnapshotTagsBotCommentsAndDropsIgnoredAuthors(t *testing.T) {
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
		kind := "human"
		if comment.Bot {
			kind = "bot"
		}
		got = append(got, comment.ID+":"+comment.Kind+":"+comment.Author+":"+kind)
	}
	// Sorted oldest-first; the ignored author is gone and the bot is tagged.
	want := []string{
		"c1:issue:victorarias:human",
		"c2:issue:chatgpt-codex-connector:bot",
		"r1:review:figgyster:human",
		"t1:inline:figgyster:human",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("comments = %v, want %v", got, want)
	}
	if len(humanPRComments(readiness.Comments)) != 3 || len(botPRComments(readiness.Comments)) != 1 {
		t.Fatalf("split = %d human / %d bot", len(humanPRComments(readiness.Comments)), len(botPRComments(readiness.Comments)))
	}
	// A COMMENTED review carries no verdict and must not move the review state.
	if readiness.ReviewState != "waiting" {
		t.Fatalf("COMMENTED review changed review state: %#v", readiness)
	}
	// A prose review is reported as a remark, so it has to carry its prose: a
	// header with no body sends the caller back to GitHub for the one thing the
	// event was about.
	for _, comment := range readiness.Comments {
		if comment.ID == "r1" && comment.Body != "a real review remark" {
			t.Fatalf("review comment lost its body: %#v", comment)
		}
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
	fresh := unseenPRComments(readiness.Comments, seen, time.Time{})
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
	got, outcome, _, err := waitTuple(context.Background(), source, opts, &output)
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

	got, outcome, _, err := waitTuple(context.Background(), source, prWaitOptions{Reviewer: "figgyster"}, &bytes.Buffer{})
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

	got, outcome, _, err := waitTuple(context.Background(), source, prWaitOptions{Reviewer: "figgyster"}, &bytes.Buffer{})
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
	_, outcome, _, err := waitTuple(context.Background(), &fakeReadinessSource{results: []*prReadiness{failed}}, prWaitOptions{}, &bytes.Buffer{})
	if err != nil || outcome != outcomeChecksFailed || outcome.exitCode() != prWaitExitChecksFailed {
		t.Fatalf("outcome=%s err=%v", outcome, err)
	}

	closed := readinessObservation("12", strings.Repeat("c", 40), checksGreen, "waiting")
	closed.State = "merged"
	_, outcome, _, err = waitTuple(context.Background(), &fakeReadinessSource{results: []*prReadiness{closed}}, prWaitOptions{}, &bytes.Buffer{})
	if err != nil || outcome != outcomeClosed || outcome.exitCode() != prWaitExitError {
		t.Fatalf("outcome=%s err=%v", outcome, err)
	}
}

func TestWaitForPRActionableReturnsTimeoutOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pending := readinessObservation("12", strings.Repeat("d", 40), checksPending, "waiting")
	source := &fakeReadinessSource{results: []*prReadiness{pending}}

	_, outcome, _, err := waitTuple(ctx, source, prWaitOptions{Interval: time.Hour}, &bytes.Buffer{})
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
	if code := reportPROutcome(prWaitResult{Observation: result, Outcome: outcomeApproved, Events: []prOutcome{outcomeApproved}}, prWaitOptions{}, &plain); code != prWaitExitApproved {
		t.Fatalf("exit code = %d", code)
	}
	if got := plain.String(); !strings.HasPrefix(got, "approved: figgyster approved eeeeeeeeeeee") {
		t.Fatalf("plain output = %q", got)
	}

	result.Comments = []prComment{{ID: "c1", Author: "victorarias", Kind: "issue"}}
	var encoded bytes.Buffer
	code := reportPROutcome(prWaitResult{Observation: result, Outcome: outcomeChangesRequested, Events: []prOutcome{outcomeChangesRequested}}, prWaitOptions{JSON: true}, &encoded)
	if code != prWaitExitChangesRequested {
		t.Fatalf("exit code = %d", code)
	}
	var payload struct {
		Outcome  string      `json:"outcome"`
		Events   []string    `json:"events"`
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
	// Comments ride along whatever event won: the waiter has already reduced them
	// to what arrived during the wait, so withholding them here would only make
	// the caller re-query for news this poll already had.
	if len(payload.Comments) != 1 || payload.Comments[0].Author != "victorarias" {
		t.Fatalf("comments = %#v, want the fresh comment reported beside the verdict", payload.Comments)
	}
	if strings.Join(payload.Events, ",") != "changes_requested" {
		t.Fatalf("events = %v", payload.Events)
	}
}

// The regression that motivated the rewrite: figgyster approves with prose, and
// the approval used to be reported as "a new comment" — exit 4, not 0 — because
// the review body was collected as commentary and the comment branch returned
// before the verdict branch was ever evaluated.
func TestWaitForPRActionableApprovalWithBodyIsOneEvent(t *testing.T) {
	head := strings.Repeat("f", 40)
	at := mustTime(t, "2026-07-26T10:00:00Z")
	reviews := reviewNode("r1", "APPROVED", "Approved. The witness is the proof I wanted.", "2026-07-26T10:00:00Z", "figgyster", head, "")
	opts := prWaitOptions{Reviewer: "figgyster", Interval: 0}

	observation, err := parsePRSnapshot(snapshotPayload(head, "", reviews, ""), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Comments) != 0 {
		t.Fatalf("the verdict body was collected as a comment: %#v", observation.Comments)
	}
	if observation.ReviewState != "approved" || !observation.ReviewSubmittedAt.Equal(at) {
		t.Fatalf("verdict not read: %#v", observation)
	}

	// Drive it through the waiter with the verdict arriving during the wait, so
	// the baseline cannot be what hides the phantom comment.
	waiting, err := parsePRSnapshot(snapshotPayload(head, "", "", ""), opts)
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeReadinessSource{results: []*prReadiness{waiting, observation}}
	_, outcome, events, err := waitTuple(context.Background(), source, opts, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != outcomeApproved || outcome.exitCode() != prWaitExitApproved {
		t.Fatalf("outcome = %s (exit %d), want approved", outcome, outcome.exitCode())
	}
	if strings.Join(outcomeStrings(events), ",") != "approved" {
		t.Fatalf("events = %v, want the verdict alone", outcomeStrings(events))
	}

	// A COMMENTED review from the same reviewer stays commentary: it is how they
	// say something without answering.
	remark := reviewNode("r2", "COMMENTED", "one question before I approve", "2026-07-26T10:05:00Z", "figgyster", head, "")
	commented, err := parsePRSnapshot(snapshotPayload(head, "", remark, ""), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(commented.Comments) != 1 || commented.Comments[0].Kind != "review" {
		t.Fatalf("a COMMENTED review must stay a comment: %#v", commented.Comments)
	}
}

// One poll can hold several events. The exit code names the highest ranked, and
// the rest must still be reported — a caller that gets `comment` alongside an
// approval should not have to re-query to learn it can merge once answered.
func TestWaitForPRActionableRanksConcurrentEventsAndReportsAll(t *testing.T) {
	head := strings.Repeat("g", 40)
	opts := prWaitOptions{Reviewer: "figgyster", Interval: 0}
	human := prComment{ID: "c1", Author: "victorarias", Kind: "issue", CreatedAt: time.Unix(2, 0)}
	bot := prComment{ID: "c2", Author: "react-doctor", Kind: "issue", Bot: true, CreatedAt: time.Unix(3, 0)}

	first := readinessObservation("12", head, checksPending, "waiting")
	second := readinessObservation("12", head, checksGreen, "approved")
	second.Comments = []prComment{human, bot}
	source := &fakeReadinessSource{results: []*prReadiness{first, second}}

	var output bytes.Buffer
	_, outcome, events, err := waitTuple(context.Background(), source, opts, &output)
	if err != nil {
		t.Fatal(err)
	}
	// Human comment outranks both the bot's and the approval: someone has to read
	// it before the approval means anything. The bot ranks below the approval —
	// nobody is waiting on a doctor report, and it comments on nearly every push.
	if outcome != outcomeComment {
		t.Fatalf("outcome = %s, want the human comment to win", outcome)
	}
	if got := strings.Join(outcomeStrings(events), ","); got != "comment,approved,bot_comment" {
		t.Fatalf("events = %s, want all three ranked", got)
	}

	var plain bytes.Buffer
	if code := reportPROutcome(prWaitResult{Observation: second, Outcome: outcome, Events: events}, opts, &plain); code != prWaitExitComment {
		t.Fatalf("exit code = %d, want %d", code, prWaitExitComment)
	}
	text := plain.String()
	for _, want := range []string{"comment: 1 new comment from victorarias", "also approved:", "also bot_comment: 1 new comment from react-doctor"} {
		if !strings.Contains(text, want) {
			t.Fatalf("plain output missing %q:\n%s", want, text)
		}
	}
}

// A failing check outranks any comment. The comment check used to return before
// the check state was ever looked at, so a poll that saw both reported exit 4
// while its own payload said the checks had failed.
func TestWaitForPRActionableChecksFailedOutranksComment(t *testing.T) {
	head := strings.Repeat("h", 40)
	opts := prWaitOptions{Reviewer: "figgyster", Interval: 0}
	first := readinessObservation("12", head, checksPending, "waiting")
	second := readinessObservation("12", head, checksFailed, "waiting")
	second.Comments = []prComment{{ID: "c1", Author: "victorarias", Kind: "issue", CreatedAt: time.Unix(2, 0)}}

	_, outcome, events, err := waitTuple(context.Background(),
		&fakeReadinessSource{results: []*prReadiness{first, second}}, opts, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != outcomeChecksFailed || outcome.exitCode() != prWaitExitChecksFailed {
		t.Fatalf("outcome = %s, want checks_failed to outrank the comment", outcome)
	}
	if got := strings.Join(outcomeStrings(events), ","); got != "checks_failed,comment" {
		t.Fatalf("events = %s", got)
	}
}

// A bot comment is its own event with its own exit code, so a caller can wake on
// a doctor report without treating it as a human waiting for an answer.
func TestWaitForPRActionableBotCommentIsItsOwnEvent(t *testing.T) {
	head := strings.Repeat("i", 40)
	opts := prWaitOptions{Reviewer: "figgyster", Interval: 0}
	first := readinessObservation("12", head, checksPending, "waiting")
	second := readinessObservation("12", head, checksPending, "waiting")
	second.Comments = []prComment{{ID: "c1", Author: "react-doctor", Kind: "issue", Bot: true, CreatedAt: time.Unix(2, 0)}}

	_, outcome, events, err := waitTuple(context.Background(),
		&fakeReadinessSource{results: []*prReadiness{first, second}}, opts, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != outcomeBotComment || outcome.exitCode() != prWaitExitBotComment {
		t.Fatalf("outcome = %s (exit %d), want bot_comment", outcome, outcome.exitCode())
	}
	if got := strings.Join(outcomeStrings(events), ","); got != "bot_comment" {
		t.Fatalf("events = %s", got)
	}

	// --ignore-author silences a bot exactly as it silences a human.
	ignoring := prWaitOptions{Reviewer: "figgyster", Interval: 0, IgnoreAuthors: []string{"react-doctor"}}
	snapshot, err := parsePRSnapshot(snapshotPayload(head, "",
		"", `{"id":"c1","createdAt":"2026-07-26T10:00:00Z","author":{"__typename":"Bot","login":"react-doctor"}}`), ignoring)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Comments) != 0 {
		t.Fatalf("ignored bot still collected: %#v", snapshot.Comments)
	}
}

// The fixture is a real `gh api graphql` response for the pull request that
// exposed the bug — figgyster's approval carrying prose, plus a react-doctor
// comment — with `state` flipped to OPEN so the closed event does not short the
// wait. Hand-built fragments prove the logic; this proves the field names and
// author typenames it keys on are GitHub's.
func TestParsePRSnapshotAgainstRealApprovalPayload(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("testdata", "pr-approval-with-body.json"))
	if err != nil {
		t.Fatal(err)
	}
	opts := prWaitOptions{Reviewer: "figgyster", Interval: 0}
	observation, err := parsePRSnapshot(payload, opts)
	if err != nil {
		t.Fatal(err)
	}
	if observation.ReviewState != "approved" || observation.CheckState != checksGreen || !observation.ready() {
		t.Fatalf("real approval not read as ready: %#v", observation)
	}
	if len(humanPRComments(observation.Comments)) != 0 {
		t.Fatalf("verdict prose collected as a human comment: %#v", humanPRComments(observation.Comments))
	}
	bots := botPRComments(observation.Comments)
	if len(bots) != 1 || bots[0].Author != "github-actions" || !bots[0].Bot {
		t.Fatalf("bot comment = %#v, want the react-doctor post tagged as a bot", bots)
	}

	// End to end: the bot comment arrives during the wait alongside the approval,
	// and the approval is what the exit code reports.
	first, err := parsePRSnapshot(payload, opts)
	if err != nil {
		t.Fatal(err)
	}
	first.ReviewState, first.ReviewSubmittedAt = "waiting", time.Time{}
	_, outcome, events, err := waitTuple(context.Background(),
		&fakeReadinessSource{results: []*prReadiness{first, observation}}, opts, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != outcomeApproved || outcome.exitCode() != prWaitExitApproved {
		t.Fatalf("outcome = %s (exit %d), want approved", outcome, outcome.exitCode())
	}
	if got := strings.Join(outcomeStrings(events), ","); got != "approved" {
		t.Fatalf("events = %s; the bot comment was in the baseline, so only the verdict is news", got)
	}
}

func outcomeStrings(events []prOutcome) []string {
	result := make([]string, 0, len(events))
	for _, event := range events {
		result = append(result, string(event))
	}
	return result
}

// A re-review request marks any pre-existing verdict as stale: parse must
// surface that the reviewer is re-requested and when their newest review landed,
// so the waiter can tell a fresh verdict from the one it started against.
func TestParsePRSnapshotCapturesReviewRequestAndBaseline(t *testing.T) {
	head := strings.Repeat("a", 40)
	reviews := reviewNode("r1", "CHANGES_REQUESTED", "please fix", "2026-07-19T10:00:00Z", "figgyster", head, "")
	requests := `{"requestedReviewer":{"__typename":"User","login":"figgyster"}},
	             {"requestedReviewer":{"__typename":"Team","slug":"platform"}}`

	readiness, err := parsePRSnapshot(snapshotPayloadWithRequests(head, "", reviews, "", requests), prWaitOptions{Reviewer: "figgyster"})
	if err != nil {
		t.Fatal(err)
	}
	if !readiness.ReviewerRequested {
		t.Fatalf("re-requested reviewer not detected: %#v", readiness)
	}
	if readiness.ReviewState != "changes_requested" {
		t.Fatalf("review state = %q", readiness.ReviewState)
	}
	want := mustTime(t, "2026-07-19T10:00:00Z")
	if !readiness.ReviewSubmittedAt.Equal(want) || !readiness.LatestReviewAt.Equal(want) {
		t.Fatalf("timings = submitted %v latest %v", readiness.ReviewSubmittedAt, readiness.LatestReviewAt)
	}

	// A request for a different reviewer must not mark ours as re-requested.
	other := `{"requestedReviewer":{"__typename":"User","login":"someone-else"}}`
	readiness, err = parsePRSnapshot(snapshotPayloadWithRequests(head, "", reviews, "", other), prWaitOptions{Reviewer: "figgyster"})
	if err != nil {
		t.Fatal(err)
	}
	if readiness.ReviewerRequested {
		t.Fatalf("unrelated request marked reviewer re-requested: %#v", readiness)
	}
}

// The #633 regression: figgyster's CHANGES_REQUESTED was addressed and figgyster
// re-requested, but wait-ready exited 3 at once on the stale verdict. With the
// reviewer re-requested, the pre-baseline verdict must not end the wait; a review
// submitted after the baseline does.
func TestWaitForPRActionableIgnoresStaleVerdictWhileReReviewPending(t *testing.T) {
	head := strings.Repeat("c", 40)
	baselineAt := mustTime(t, "2026-07-19T10:00:00Z")
	freshAt := mustTime(t, "2026-07-19T12:00:00Z")

	stale := reReviewObservation(head, checksGreen, "changes_requested", true, baselineAt, baselineAt)
	stillStale := reReviewObservation(head, checksGreen, "changes_requested", true, baselineAt, baselineAt)
	// The re-review lands as an approval; GitHub clears the request as it does.
	approved := reReviewObservation(head, checksGreen, "approved", false, freshAt, freshAt)
	source := &fakeReadinessSource{results: []*prReadiness{stale, stillStale, approved}}
	opts := prWaitOptions{Reviewer: "figgyster", Interval: 0}

	var output bytes.Buffer
	got, outcome, _, err := waitTuple(context.Background(), source, opts, &output)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != outcomeApproved || outcome.exitCode() != prWaitExitApproved || got != approved {
		t.Fatalf("got=%#v outcome=%s", got, outcome)
	}
	if source.calls != 3 {
		t.Fatalf("returned after %d polls; the stale verdict must not end the wait", source.calls)
	}
	// The wait must not look stuck: the held verdict is annotated on the line and
	// explained once, and the note fires exactly once.
	text := output.String()
	if !strings.Contains(text, "review=changes_requested reviewer=figgyster re-requested=true") {
		t.Fatalf("re-request annotation missing from progress:\n%s", text)
	}
	if n := strings.Count(text, "predates the pending re-review request"); n != 1 {
		t.Fatalf("stale-verdict note fired %d times, want 1:\n%s", n, text)
	}
}

// A re-review that again requests changes is a fresh verdict and returns 3, but
// only once it is newer than the baseline recorded at wait start.
func TestWaitForPRActionableReturnsOnFreshChangesRequestedAfterReReview(t *testing.T) {
	head := strings.Repeat("c", 40)
	baselineAt := mustTime(t, "2026-07-19T10:00:00Z")
	freshAt := mustTime(t, "2026-07-19T12:00:00Z")

	stale := reReviewObservation(head, checksGreen, "changes_requested", true, baselineAt, baselineAt)
	fresh := reReviewObservation(head, checksGreen, "changes_requested", true, freshAt, freshAt)
	source := &fakeReadinessSource{results: []*prReadiness{stale, fresh}}

	got, outcome, _, err := waitTuple(context.Background(), source, prWaitOptions{Reviewer: "figgyster", Interval: 0}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != outcomeChangesRequested || outcome.exitCode() != prWaitExitChangesRequested || got != fresh {
		t.Fatalf("got=%#v outcome=%s", got, outcome)
	}
	if source.calls != 2 {
		t.Fatalf("returned after %d polls; expected the stale verdict skipped and the fresh one to return", source.calls)
	}
}

// Not re-requested: an existing verdict is the current state and returns at once.
// Approval is a state, not an event, so an already-approved PR still returns 0.
func TestWaitForPRActionableReturnsImmediatelyWithoutReReview(t *testing.T) {
	head := strings.Repeat("c", 40)
	at := mustTime(t, "2026-07-19T10:00:00Z")

	approved := reReviewObservation(head, checksGreen, "approved", false, at, at)
	source := &fakeReadinessSource{results: []*prReadiness{approved}}
	got, outcome, _, err := waitTuple(context.Background(), source, prWaitOptions{Reviewer: "figgyster", Interval: 0}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != outcomeApproved || got != approved || source.calls != 1 {
		t.Fatalf("approval not returned immediately: outcome=%s calls=%d", outcome, source.calls)
	}

	changes := reReviewObservation(head, checksGreen, "changes_requested", false, at, at)
	source = &fakeReadinessSource{results: []*prReadiness{changes}}
	got, outcome, _, err = waitTuple(context.Background(), source, prWaitOptions{Reviewer: "figgyster", Interval: 0}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != outcomeChangesRequested || got != changes || source.calls != 1 {
		t.Fatalf("changes_requested not returned immediately: outcome=%s calls=%d", outcome, source.calls)
	}
}

// A caller that has to go read the remark on GitHub is making the follow-up query
// this command exists to remove, so the words themselves are part of the report.
func TestReportPROutcomePrintsWhatWasSaid(t *testing.T) {
	head := strings.Repeat("a", 40)
	result := readinessObservation("12", head, checksFailed, "changes_requested")
	result.URL = "https://github.com/victorarias/attn/pull/12"
	result.ReviewBody = "The witness is missing."
	result.Checks = []prCheck{
		{Name: "CI", State: checksFailed, URL: "https://github.com/victorarias/attn/runs/1"},
		{Name: "Lint", State: checksGreen},
	}
	result.Comments = []prComment{
		{ID: "c1", Author: "victorarias", Kind: "review", Body: "This drops the guard.", Location: "cmd/attn/pr.go:42"},
		{ID: "c2", Author: "react-doctor", Kind: "issue", Bot: true, Body: "One finding."},
	}

	var plain bytes.Buffer
	events := []prOutcome{outcomeChecksFailed, outcomeChangesRequested, outcomeComment, outcomeBotComment}
	reportPROutcome(prWaitResult{Observation: result, Outcome: outcomeChecksFailed, Events: events}, prWaitOptions{Reviewer: "figgyster"}, &plain)

	for _, want := range []string{
		"CI https://github.com/victorarias/attn/runs/1",
		"--- figgyster ---",
		"  The witness is missing.",
		"--- victorarias on cmd/attn/pr.go:42 ---",
		"  This drops the guard.",
		"--- react-doctor ---",
		"  One finding.",
		"https://github.com/victorarias/attn/pull/12",
	} {
		if !strings.Contains(plain.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, plain.String())
		}
	}
	if strings.Contains(plain.String(), "Lint https") {
		t.Fatalf("a passing check was printed as a failure:\n%s", plain.String())
	}

	var encoded bytes.Buffer
	reportPROutcome(prWaitResult{Observation: result, Outcome: outcomeChangesRequested, Events: events}, prWaitOptions{JSON: true, Reviewer: "figgyster"}, &encoded)
	var payload struct {
		URL    string `json:"url"`
		Checks struct {
			Failed []prCheck `json:"failed"`
		} `json:"checks"`
		Review struct {
			Body string `json:"body"`
		} `json:"review"`
		Comments []prComment `json:"comments"`
	}
	if err := json.Unmarshal(encoded.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.URL == "" || payload.Review.Body != "The witness is missing." ||
		len(payload.Checks.Failed) != 1 || payload.Checks.Failed[0].Name != "CI" ||
		len(payload.Comments) != 2 || payload.Comments[0].Location != "cmd/attn/pr.go:42" ||
		payload.Comments[0].Body != "This drops the guard." {
		t.Fatalf("payload = %#v", payload)
	}
}

// The gap between two waits is where events used to disappear: the caller answers
// a comment, waits again, and the wait baselines from a state that already
// includes whatever landed while it was working.
func TestWaitForPRActionableResumesFromCursorAcrossCalls(t *testing.T) {
	head := strings.Repeat("b", 40)
	first := prComment{ID: "c1", Author: "victorarias", Kind: "issue", CreatedAt: time.Unix(1, 0)}
	gap := prComment{ID: "c2", Author: "victorarias", Kind: "issue", CreatedAt: time.Unix(2, 0)}
	opts := prWaitOptions{Reviewer: "figgyster", Interval: 0}

	start := readinessObservation("12", head, checksPending, "waiting")
	start.Comments = []prComment{first}
	arrived := readinessObservation("12", head, checksPending, "waiting")
	arrived.Comments = []prComment{first, gap}

	// The first wait times out having seen only the pre-existing comment.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	timedOut, err := waitForPRActionable(ctx, &fakeReadinessSource{results: []*prReadiness{start}}, opts, prWaitCursor{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if timedOut.Outcome != outcomeTimeout {
		t.Fatalf("outcome = %s", timedOut.Outcome)
	}
	// Even reporting nothing, the wait must carry its baseline forward, or the
	// next call rebuilds one from a later state and swallows the gap.
	if strings.Join(timedOut.Cursor.CommentIDs, ",") != "c1" {
		t.Fatalf("cursor = %#v, want the baseline comment recorded", timedOut.Cursor)
	}

	// The comment landed while the caller was between waits: the second wait must
	// report it on its first poll rather than absorb it into a fresh baseline.
	second, err := waitForPRActionable(context.Background(),
		&fakeReadinessSource{results: []*prReadiness{arrived}}, opts, timedOut.Cursor, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Outcome != outcomeComment || len(second.Observation.Comments) != 1 ||
		second.Observation.Comments[0].ID != "c2" {
		t.Fatalf("outcome=%s comments=%#v", second.Outcome, second.Observation.Comments)
	}
	if strings.Join(second.Cursor.CommentIDs, ",") != "c1,c2" {
		t.Fatalf("cursor = %#v, want the reported comment added", second.Cursor)
	}

	// And a third wait on the same state has nothing new to say.
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	third, err := waitForPRActionable(ctx, &fakeReadinessSource{results: []*prReadiness{arrived}}, opts, second.Cursor, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if third.Outcome != outcomeTimeout {
		t.Fatalf("a comment already reported was reported again: outcome = %s", third.Outcome)
	}
}

// A failing check stays true until someone pushes, so re-reporting it would make
// every subsequent wait return instantly with nothing new.
func TestWaitForPRActionableSuppressesAlreadyReportedFailure(t *testing.T) {
	head := strings.Repeat("c", 40)
	opts := prWaitOptions{Reviewer: "figgyster", Interval: 0}
	failing := readinessObservation("12", head, checksFailed, "waiting")
	failing.Checks = []prCheck{{Name: "CI", State: checksFailed}}

	first, err := waitForPRActionable(context.Background(), &fakeReadinessSource{results: []*prReadiness{failing}}, opts, prWaitCursor{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Outcome != outcomeChecksFailed || first.Cursor.FailureHead != head ||
		strings.Join(first.Cursor.FailureChecks, ",") != "CI" {
		t.Fatalf("outcome=%s cursor=%#v", first.Outcome, first.Cursor)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	again, err := waitForPRActionable(ctx, &fakeReadinessSource{results: []*prReadiness{failing}}, opts, first.Cursor, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if again.Outcome != outcomeTimeout {
		t.Fatalf("the same failure returned twice: outcome = %s", again.Outcome)
	}

	// A different failing check on the same commit is a different failure.
	worse := readinessObservation("12", head, checksFailed, "waiting")
	worse.Checks = []prCheck{{Name: "CI", State: checksFailed}, {Name: "Lint", State: checksFailed}}
	changed, err := waitForPRActionable(context.Background(), &fakeReadinessSource{results: []*prReadiness{worse}}, opts, first.Cursor, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Outcome != outcomeChecksFailed {
		t.Fatalf("a new failing check was suppressed: outcome = %s", changed.Outcome)
	}

	// So is the same failure after a push.
	pushed := readinessObservation("12", strings.Repeat("d", 40), checksFailed, "waiting")
	pushed.Checks = []prCheck{{Name: "CI", State: checksFailed}}
	repushed, err := waitForPRActionable(context.Background(), &fakeReadinessSource{results: []*prReadiness{pushed}}, opts, first.Cursor, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if repushed.Outcome != outcomeChecksFailed {
		t.Fatalf("the failure on a new head was suppressed: outcome = %s", repushed.Outcome)
	}
}

// --since is the escape hatch: it replays by arrival time, so a caller does not
// need to know any comment ID to recover a window.
func TestWaitForPRActionableSinceReplaysByTime(t *testing.T) {
	head := strings.Repeat("e", 40)
	old := prComment{ID: "c1", Author: "victorarias", Kind: "issue", CreatedAt: time.Unix(100, 0)}
	recent := prComment{ID: "c2", Author: "victorarias", Kind: "issue", CreatedAt: time.Unix(300, 0)}
	observation := readinessObservation("12", head, checksPending, "waiting")
	observation.Comments = []prComment{old, recent}

	opts := prWaitOptions{Reviewer: "figgyster", Interval: 0, Since: time.Unix(200, 0)}
	// The cursor claims both comments were already reported; --since overrides it.
	result, err := waitForPRActionable(context.Background(), &fakeReadinessSource{results: []*prReadiness{observation}}, opts,
		prWaitCursor{CommentIDs: []string{"c1", "c2"}}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != outcomeComment || len(result.Observation.Comments) != 1 ||
		result.Observation.Comments[0].ID != "c2" {
		t.Fatalf("outcome=%s comments=%#v", result.Outcome, result.Observation.Comments)
	}
}

// snapshotSource drives the waiter through the real snapshot parser, so a test
// about which comments become events exercises the same path a live wait does.
type snapshotSource struct {
	payloads [][]byte
	calls    int
}

func (s *snapshotSource) Fetch(_ context.Context, opts prWaitOptions) (*prReadiness, error) {
	index := s.calls
	s.calls++
	if index >= len(s.payloads) {
		index = len(s.payloads) - 1
	}
	return parsePRSnapshot(s.payloads[index], opts)
}

// The scenario the self-suppression exists for. A wait resumes from where the
// previous one stopped, so a comment the caller posts between two waits is
// genuinely new to the cursor and the start-of-wait baseline cannot catch it:
// answer a reviewer, wait again, and the wait returns instantly quoting you back
// at yourself.
func TestWaitForPRActionableDoesNotWakeOnTheCallersOwnComment(t *testing.T) {
	head := strings.Repeat("a", 40)
	mine := `{"id":"c1","createdAt":"2026-07-26T10:00:00Z","bodyText":"answering the review",
	          "author":{"__typename":"User","login":"victorarias"}}`
	theirs := `{"id":"c2","createdAt":"2026-07-26T10:05:00Z","bodyText":"one more thing",
	            "author":{"__typename":"User","login":"figgyster"}}`
	// Login comparison is case-insensitive: GitHub renders a login in whatever
	// case it was registered with, and `gh api user` and a comment author need not
	// agree on it.
	opts := prWaitOptions{Reviewer: "figgyster", Interval: time.Millisecond, SelfLogin: "VictorArias"}
	// A non-empty cursor is what makes this a resumed wait rather than a first
	// one, so nothing is baselined away and c1 arrives as unseen.
	resumed := prWaitCursor{VerdictAt: mustTime(t, "2026-07-26T09:00:00Z")}
	// The two waits below are supposed to return an event on their first poll.
	// Given a deadline, one that stops returning fails the assertion below; given
	// context.Background() it would poll a fixed snapshot forever and hang the
	// package instead.
	bounded := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 10*time.Second)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	own, err := waitForPRActionable(ctx, &snapshotSource{payloads: [][]byte{snapshotPayload(head, "", "", mine)}},
		opts, resumed, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if own.Outcome != outcomeTimeout {
		t.Fatalf("the caller's own comment ended the wait: outcome=%s comments=%#v", own.Outcome, own.Observation.Comments)
	}

	// Someone else's comment on the same poll is still the update being waited for.
	otherCtx, cancelOther := bounded()
	defer cancelOther()
	other, err := waitForPRActionable(otherCtx,
		&snapshotSource{payloads: [][]byte{snapshotPayload(head, "", "", mine+","+theirs)}},
		opts, resumed, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if other.Outcome != outcomeComment || len(other.Observation.Comments) != 1 ||
		other.Observation.Comments[0].ID != "c2" {
		t.Fatalf("outcome=%s comments=%#v, want only figgyster's comment", other.Outcome, other.Observation.Comments)
	}

	// --include-self leaves SelfLogin unresolved, which is also what an
	// unreachable `gh api user` falls back to: both report every author.
	opts.SelfLogin = ""
	includedCtx, cancelIncluded := bounded()
	defer cancelIncluded()
	included, err := waitForPRActionable(includedCtx,
		&snapshotSource{payloads: [][]byte{snapshotPayload(head, "", "", mine)}},
		opts, resumed, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if included.Outcome != outcomeComment || len(included.Observation.Comments) != 1 ||
		included.Observation.Comments[0].ID != "c1" {
		t.Fatalf("outcome=%s comments=%#v, want the caller's own comment reported", included.Outcome, included.Observation.Comments)
	}
}

// The identity lookup costs a `gh` call, so it must not happen when the caller
// has opted out, and it must never be able to fail a wait.
func TestResolvePRSelfLoginIsSkippableAndFailureTolerant(t *testing.T) {
	original := ghSelfLogin
	t.Cleanup(func() { ghSelfLogin = original })

	calls := 0
	ghSelfLogin = func(context.Context, string) (string, error) {
		calls++
		return "", errors.New("gh: not authenticated")
	}
	var stderr bytes.Buffer
	if login := resolvePRSelfLogin(context.Background(), prWaitOptions{}, &stderr); login != "" {
		t.Fatalf("login = %q, want an unresolvable login to report everyone", login)
	}
	if calls != 1 || !strings.Contains(stderr.String(), "could not resolve") {
		t.Fatalf("calls=%d stderr=%q", calls, stderr.String())
	}

	ghSelfLogin = func(_ context.Context, host string) (string, error) {
		calls++
		if host != "ghe.example.com" {
			t.Fatalf("host = %q, want the lookup scoped to the pull request's host", host)
		}
		return "victorarias", nil
	}
	if login := resolvePRSelfLogin(context.Background(), prWaitOptions{Host: "ghe.example.com"}, io.Discard); login != "victorarias" {
		t.Fatalf("login = %q", login)
	}

	ghSelfLogin = func(context.Context, string) (string, error) {
		t.Error("--include-self asked GitHub who we are")
		return "", nil
	}
	if login := resolvePRSelfLogin(context.Background(), prWaitOptions{IncludeSelf: true}, io.Discard); login != "" {
		t.Fatalf("login = %q under --include-self", login)
	}
}

// --timeout is a promise about when the command returns, and the identity lookup
// happens before the first poll. Given a clock of its own it would spend up to
// prSelfLoginTimeout on top of the caller's budget, so `--timeout 1s` against a
// stalled GitHub would take sixteen seconds to come back.
func TestResolvePRSelfLoginCannotOutlastTheWaitDeadline(t *testing.T) {
	original := ghSelfLogin
	t.Cleanup(func() { ghSelfLogin = original })

	// A GitHub that never answers. Only the context can end this call, so the
	// deadline the lookup runs under is the one that decides when it returns.
	ghSelfLogin = func(ctx context.Context, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}

	budget := 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	start := time.Now()
	login := resolvePRSelfLogin(ctx, prWaitOptions{}, io.Discard)
	elapsed := time.Since(start)

	if login != "" {
		t.Fatalf("login = %q, want a stalled lookup to report everyone", login)
	}
	// The bound is deliberately loose: the point is prSelfLoginTimeout not being
	// spent, not the exact millisecond the short budget expires on.
	if elapsed >= prSelfLoginTimeout {
		t.Fatalf("lookup took %s with a %s budget: it borrowed its own clock", elapsed, budget)
	}
}

func TestPRWaitCursorRoundTripsOnDisk(t *testing.T) {
	dir := t.TempDir()
	opts := prWaitOptions{Owner: "victorarias", Name: "attn", Number: 679}
	now := time.Unix(1_700_000_000, 0)

	if cursor, err := loadPRWaitCursor(dir, opts); err != nil || !cursor.empty() {
		t.Fatalf("first call must start empty: cursor=%#v err=%v", cursor, err)
	}

	saved := prWaitCursor{CommentIDs: []string{"c1"}, VerdictAt: now, FailureHead: "abc", FailureChecks: []string{"CI"}}
	if err := savePRWaitCursor(dir, opts, saved, now); err != nil {
		t.Fatal(err)
	}
	// Host defaults so a --repo owner/name target and a github.com URL agree.
	path := filepath.Join(dir, "github.com", "victorarias", "attn", "679.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cursor not at %s: %v", path, err)
	}
	loaded, err := loadPRWaitCursor(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(loaded.CommentIDs, ",") != "c1" || !loaded.VerdictAt.Equal(now) ||
		loaded.FailureHead != "abc" || !loaded.sameFailure("abc", []prCheck{{Name: "CI", State: checksFailed}}) {
		t.Fatalf("loaded = %#v", loaded)
	}

	// A cursor with nothing recorded in a field says nothing about it, rather than
	// claiming a verdict at the zero time.
	encoded, err := json.Marshal(prWaitCursor{CommentIDs: []string{"c1"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "0001-01-01") {
		t.Fatalf("cursor = %s", encoded)
	}

	// A cursor is an optimization over re-baselining: a corrupt one must not stop
	// the caller from waiting.
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cursor, err := loadPRWaitCursor(dir, opts); err == nil || !cursor.empty() {
		t.Fatalf("corrupt cursor must report the problem and read as empty: cursor=%#v err=%v", cursor, err)
	}

	// Cursors nobody has waited on in a month are dropped, and the directory has
	// no other owner to clean it.
	stale := filepath.Join(dir, "github.com", "victorarias", "attn", "1.json")
	if err := os.WriteFile(stale, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-2 * prCursorMaxAge)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := savePRWaitCursor(dir, opts, saved, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale cursor survived: %v", err)
	}
}

// An empty directory disables the memory, which is what any code path that has no
// business resolving a data dir passes.
func TestPRWaitCursorWithoutDirectoryIsInert(t *testing.T) {
	if err := savePRWaitCursor("", prWaitOptions{Number: 1}, prWaitCursor{CommentIDs: []string{"c1"}}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if cursor, err := loadPRWaitCursor("", prWaitOptions{Number: 1}); err != nil || !cursor.empty() {
		t.Fatalf("cursor=%#v err=%v", cursor, err)
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

	opts, err = parsePRWaitArgs([]string{"602", "--repo", "victorarias/attn", "--reviewer", "figgyster",
		"--reset", "--since", "2026-07-26T10:00:00Z", "--include-self"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Reset || !opts.IncludeSelf || !opts.Since.Equal(mustTime(t, "2026-07-26T10:00:00Z")) {
		t.Fatalf("opts = %#v", opts)
	}
	if _, err := parsePRWaitArgs([]string{"602", "--repo", "victorarias/attn", "--reviewer", "figgyster", "--since", "yesterday"}); err == nil ||
		!strings.Contains(err.Error(), "RFC3339") {
		t.Fatalf("--since error = %v", err)
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
}

func readinessObservation(number, head, checks, review string) *prReadiness {
	return &prReadiness{
		Number: number, State: "open", HeadSHA: head, Checks: []prCheck{{Name: "check:CI", State: checks}},
		CheckState: checks, Reviewer: "figgyster", ReviewState: review,
	}
}
