package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automation"
)

const (
	prWaitExitApproved         = 0
	prWaitExitChecksFailed     = 1
	prWaitExitUsage            = 2
	prWaitExitChangesRequested = 3
	prWaitExitComment          = 4
	prWaitExitError            = 5
	prWaitExitTimeout          = 124

	checksNone    = "none"
	checksPending = "pending"
	checksGreen   = "green"
	checksFailed  = "failed"
)

// prOutcome is the actionable update that ended the wait. The waiter exists to
// return as soon as a human needs to do something, not only when the pull
// request is mergeable.
type prOutcome string

const (
	outcomeApproved         prOutcome = "approved"
	outcomeChecksFailed     prOutcome = "checks_failed"
	outcomeChangesRequested prOutcome = "changes_requested"
	outcomeComment          prOutcome = "comment"
	outcomeClosed           prOutcome = "closed"
	outcomeTimeout          prOutcome = "timeout"
)

func (o prOutcome) exitCode() int {
	switch o {
	case outcomeApproved:
		return prWaitExitApproved
	case outcomeChecksFailed:
		return prWaitExitChecksFailed
	case outcomeChangesRequested:
		return prWaitExitChangesRequested
	case outcomeComment:
		return prWaitExitComment
	case outcomeTimeout:
		return prWaitExitTimeout
	default:
		return prWaitExitError
	}
}

type prCheck struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// prComment is any commentary surface: a standalone PR comment, an inline
// review-thread comment, or a review submitted without a verdict.
type prComment struct {
	ID        string    `json:"-"`
	Author    string    `json:"author"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
}

type prReadiness struct {
	Number, State, HeadSHA, CheckState, Reviewer, ReviewState string
	Draft                                                     bool
	Checks                                                    []prCheck
	Comments                                                  []prComment
	// ReviewerRequested is true when the reviewer is currently in the PR's
	// requested reviewers, i.e. a (re-)review is pending. While it is set, any
	// existing verdict from them is stale context, not their answer.
	ReviewerRequested bool
	// ReviewSubmittedAt is when the review that set ReviewState was submitted;
	// zero when there is no verdict. LatestReviewAt is the newest review the
	// reviewer has submitted in any state on any commit, and serves as the
	// baseline the waiter records at start to recognize a stale verdict.
	ReviewSubmittedAt time.Time
	LatestReviewAt    time.Time
}

func (r *prReadiness) ready() bool {
	return r.State == "open" && !r.Draft && r.CheckState == checksGreen && r.ReviewState == "approved"
}

type prReadinessSource interface {
	Fetch(context.Context, prWaitOptions) (*prReadiness, error)
}

type prWaitOptions struct {
	Host, Owner, Name string
	Number            int
	Reviewer          string
	IgnoreAuthors     []string
	Timeout, Interval time.Duration
	JSON              bool
}

func (o prWaitOptions) ignored(author string) bool {
	for _, ignored := range o.IgnoreAuthors {
		if strings.EqualFold(ignored, author) {
			return true
		}
	}
	return false
}

type ghPRReadinessSource struct{}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }

func (s *stringSliceFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("author must not be empty")
	}
	*s = append(*s, value)
	return nil
}

func runPRCommand() {
	code := executePRCommand(os.Args[2:], os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}

func executePRCommand(args []string, stdout, stderr io.Writer) int {
	if (len(args) == 1 && isHelpArg(args[0])) || (len(args) == 2 && args[0] == "wait-ready" && isHelpArg(args[1])) {
		writePRHelp(stdout)
		return 0
	}
	if len(args) == 0 || args[0] != "wait-ready" {
		writePRHelp(stderr)
		return prWaitExitUsage
	}
	opts, err := parsePRWaitArgs(args[1:])
	if err != nil {
		fmt.Fprintf(stderr, "pr wait-ready: %v\n", err)
		return prWaitExitUsage
	}
	if _, err := exec.LookPath("gh"); err != nil {
		fmt.Fprintln(stderr, "pr wait-ready: gh is required")
		return prWaitExitUsage
	}

	// Progress must never contaminate a JSON result on stdout.
	progress := stdout
	if opts.JSON {
		progress = stderr
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	result, outcome, err := waitForPRActionable(ctx, ghPRReadinessSource{}, opts, progress)
	if err != nil {
		fmt.Fprintf(stderr, "pr wait-ready: %v\n", err)
		return prWaitExitError
	}
	return reportPROutcome(result, outcome, opts, stdout)
}

func reportPROutcome(result *prReadiness, outcome prOutcome, opts prWaitOptions, stdout io.Writer) int {
	detail := describePROutcome(result, outcome, opts)
	if opts.JSON {
		// "comments" reports what arrived during the wait. On any other outcome
		// the observation still carries the baseline, which is not news.
		fresh := []prComment{}
		if outcome == outcomeComment {
			fresh = result.Comments
		}
		payload := map[string]any{
			"outcome": string(outcome),
			"pr":      result.Number,
			"head":    result.HeadSHA,
			"state":   result.State,
			"draft":   result.Draft,
			"detail":  detail,
			"checks": map[string]any{
				"state": result.CheckState,
				"items": result.Checks,
			},
			"review": map[string]any{
				"state":    result.ReviewState,
				"reviewer": result.Reviewer,
			},
			"comments": fresh,
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			return prWaitExitError
		}
		return outcome.exitCode()
	}
	fmt.Fprintf(stdout, "%s: %s\n", outcome, detail)
	return outcome.exitCode()
}

func describePROutcome(result *prReadiness, outcome prOutcome, opts prWaitOptions) string {
	head := shortSHA(result.HeadSHA)
	switch outcome {
	case outcomeApproved:
		return fmt.Sprintf("%s approved %s; %d checks green", result.Reviewer, head, len(result.Checks))
	case outcomeChangesRequested:
		return fmt.Sprintf("%s requested changes on %s", result.Reviewer, head)
	case outcomeChecksFailed:
		return fmt.Sprintf("%s failed on %s", strings.Join(failedCheckNames(result.Checks), ", "), head)
	case outcomeComment:
		return describePRComments(result.Comments)
	case outcomeClosed:
		return fmt.Sprintf("pull request is %s", result.State)
	case outcomeTimeout:
		detail := fmt.Sprintf("no actionable update after %s (checks=%s review=%s)", opts.Timeout, result.CheckState, result.ReviewState)
		if result.ReviewerRequested && hasReviewVerdict(result) {
			detail += "; held the pre-baseline verdict, awaiting a re-review"
		}
		return detail
	default:
		return string(outcome)
	}
}

func describePRComments(comments []prComment) string {
	authors := make([]string, 0, len(comments))
	seen := map[string]bool{}
	for _, comment := range comments {
		if !seen[comment.Author] {
			seen[comment.Author] = true
			authors = append(authors, comment.Author)
		}
	}
	noun := "comments"
	if len(comments) == 1 {
		noun = "comment"
	}
	return fmt.Sprintf("%d new %s from %s", len(comments), noun, strings.Join(authors, ", "))
}

func failedCheckNames(checks []prCheck) []string {
	var names []string
	for _, check := range checks {
		if check.State == checksFailed {
			names = append(names, check.Name)
		}
	}
	return names
}

func isHelpArg(arg string) bool { return arg == "-h" || arg == "--help" }

func parsePRWaitArgs(args []string) (prWaitOptions, error) {
	fs := flag.NewFlagSet("pr wait-ready", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", "", "[host/]owner/repository")
	reviewer := fs.String("reviewer", "", "required reviewer login")
	timeout := fs.Duration("timeout", 30*time.Minute, "maximum wait")
	interval := fs.Duration("interval", 20*time.Second, "poll interval")
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	var ignore stringSliceFlag
	fs.Var(&ignore, "ignore-author", "comment author to ignore (repeatable)")

	target := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return prWaitOptions{}, err
	}
	if target == "" && fs.NArg() == 1 {
		target = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return prWaitOptions{}, errors.New("usage: attn pr wait-ready <number-or-url> --repo owner/repo --reviewer login")
	}
	if target == "" || strings.TrimSpace(*reviewer) == "" {
		return prWaitOptions{}, errors.New("target and --reviewer are required")
	}
	if *timeout <= 0 || *interval <= 0 {
		return prWaitOptions{}, errors.New("--timeout and --interval must be positive")
	}

	opts := prWaitOptions{
		Reviewer:      strings.TrimSpace(*reviewer),
		IgnoreAuthors: ignore,
		Timeout:       *timeout,
		Interval:      *interval,
		JSON:          *asJSON,
	}
	if strings.HasPrefix(target, "https://") {
		host, owner, repository, number, err := automation.ParsePullRequestURL(target)
		if err != nil {
			return prWaitOptions{}, err
		}
		opts.Host, opts.Owner, opts.Name, opts.Number = host, owner, repository, number
		return opts, nil
	}
	number, err := strconv.Atoi(target)
	if err != nil || number <= 0 {
		return prWaitOptions{}, errors.New("pull request number must be positive")
	}
	if strings.TrimSpace(*repo) == "" {
		return prWaitOptions{}, errors.New("--repo is required when the target is a number")
	}
	host, owner, name, err := parseRepoFlag(*repo)
	if err != nil {
		return prWaitOptions{}, err
	}
	opts.Host, opts.Owner, opts.Name, opts.Number = host, owner, name, number
	return opts, nil
}

func parseRepoFlag(repo string) (host, owner, name string, err error) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(repo), "/"), "/")
	switch len(parts) {
	case 2:
		host, owner, name = "", parts[0], parts[1]
	case 3:
		host, owner, name = parts[0], parts[1], parts[2]
	default:
		return "", "", "", errors.New("--repo must be [host/]owner/repository")
	}
	if owner == "" || name == "" {
		return "", "", "", errors.New("--repo must be [host/]owner/repository")
	}
	return host, owner, name, nil
}

// prSnapshotQuery collects head, checks, reviews, and every comment surface in
// one round trip so a poll cannot mix signals from different commits, and so
// bot authorship is authoritative. `gh pr view --json comments` strips the
// "[bot]" suffix and omits the author type, which makes bots indistinguishable
// from humans; GraphQL's __typename does not.
const prSnapshotQuery = `
query($owner:String!,$name:String!,$number:Int!){
  repository(owner:$owner,name:$name){
    pullRequest(number:$number){
      number state isDraft headRefOid
      commits(last:1){nodes{commit{statusCheckRollup{contexts(first:100){
        pageInfo{hasNextPage}
        nodes{__typename ... on CheckRun{name status conclusion} ... on StatusContext{context state}}
      }}}}}
      reviewRequests(first:100){nodes{requestedReviewer{__typename ... on User{login}}}}
      reviews(last:100){nodes{id state bodyText submittedAt author{__typename login} commit{oid}
        comments(first:100){pageInfo{hasNextPage} nodes{id createdAt author{__typename login}}}}}
      comments(last:100){nodes{id createdAt author{__typename login}}}
    }}}`

func (ghPRReadinessSource) Fetch(ctx context.Context, opts prWaitOptions) (*prReadiness, error) {
	args := []string{"api", "graphql",
		"-f", "query=" + prSnapshotQuery,
		"-F", "owner=" + opts.Owner,
		"-F", "name=" + opts.Name,
		"-F", "number=" + strconv.Itoa(opts.Number),
	}
	if opts.Host != "" {
		args = append(args, "--hostname", opts.Host)
	}
	output, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("gh api graphql: %s", strings.TrimSpace(string(output)))
	}
	return parsePRSnapshot(output, opts)
}

type prGraphQLAuthor struct {
	TypeName string `json:"__typename"`
	Login    string `json:"login"`
}

type prGraphQLComment struct {
	ID        string          `json:"id"`
	CreatedAt time.Time       `json:"createdAt"`
	Author    prGraphQLAuthor `json:"author"`
}

func parsePRSnapshot(output []byte, opts prWaitOptions) (*prReadiness, error) {
	var payload struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					Number         json.Number `json:"number"`
					State          string      `json:"state"`
					IsDraft        bool        `json:"isDraft"`
					HeadRefOID     string      `json:"headRefOid"`
					ReviewRequests struct {
						Nodes []struct {
							RequestedReviewer prGraphQLAuthor `json:"requestedReviewer"`
						} `json:"nodes"`
					} `json:"reviewRequests"`
					Commits struct {
						Nodes []struct {
							Commit struct {
								StatusCheckRollup *struct {
									Contexts struct {
										PageInfo struct {
											HasNextPage bool `json:"hasNextPage"`
										} `json:"pageInfo"`
										Nodes []struct {
											TypeName   string `json:"__typename"`
											Name       string `json:"name"`
											Context    string `json:"context"`
											Status     string `json:"status"`
											Conclusion string `json:"conclusion"`
											State      string `json:"state"`
										} `json:"nodes"`
									} `json:"contexts"`
								} `json:"statusCheckRollup"`
							} `json:"commit"`
						} `json:"nodes"`
					} `json:"commits"`
					Reviews struct {
						Nodes []struct {
							ID          string          `json:"id"`
							State       string          `json:"state"`
							BodyText    string          `json:"bodyText"`
							SubmittedAt time.Time       `json:"submittedAt"`
							Author      prGraphQLAuthor `json:"author"`
							Commit      struct {
								OID string `json:"oid"`
							} `json:"commit"`
							Comments struct {
								PageInfo struct {
									HasNextPage bool `json:"hasNextPage"`
								} `json:"pageInfo"`
								Nodes []prGraphQLComment `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviews"`
					Comments struct {
						Nodes []prGraphQLComment `json:"nodes"`
					} `json:"comments"`
					ReviewThreads struct {
						Nodes []struct {
							Comments struct {
								Nodes []prGraphQLComment `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("parse gh api graphql: %w", err)
	}
	if len(payload.Errors) > 0 {
		return nil, fmt.Errorf("gh api graphql: %s", payload.Errors[0].Message)
	}
	pr := payload.Data.Repository.PullRequest
	if pr == nil || pr.Number == "" || pr.HeadRefOID == "" {
		return nil, errors.New("gh api graphql returned no PR number or head SHA")
	}

	result := &prReadiness{
		Number: pr.Number.String(), State: strings.ToLower(pr.State), Draft: pr.IsDraft,
		HeadSHA: pr.HeadRefOID, Reviewer: opts.Reviewer, ReviewState: "waiting",
	}

	for _, request := range pr.ReviewRequests.Nodes {
		if request.RequestedReviewer.TypeName == "User" && strings.EqualFold(request.RequestedReviewer.Login, opts.Reviewer) {
			result.ReviewerRequested = true
			break
		}
	}

	if len(pr.Commits.Nodes) > 0 {
		if rollup := pr.Commits.Nodes[0].Commit.StatusCheckRollup; rollup != nil {
			// Checks are fetched first:100. Truncation here could hide a
			// failing check, so refuse rather than report a false green.
			if rollup.Contexts.PageInfo.HasNextPage {
				return nil, errors.New("PR has more than 100 checks; readiness cannot be verified without truncation")
			}
			for _, check := range rollup.Contexts.Nodes {
				name, state := "status:"+check.Context, statusState(check.State)
				if check.TypeName == "CheckRun" {
					name, state = "check:"+check.Name, checkRunState(check.Status, check.Conclusion)
				}
				result.Checks = append(result.Checks, prCheck{Name: name, State: state})
			}
		}
	}
	sort.Slice(result.Checks, func(i, j int) bool { return result.Checks[i].Name < result.Checks[j].Name })
	result.CheckState = summarizePRChecks(result.Checks)

	// Reviews and issue comments use last:100, so truncation drops only the
	// oldest entries, which cannot be the actionable update.
	//
	// Inline comments deliberately come from the reviews that carry them rather
	// than from reviewThreads. A thread's position is fixed when the thread
	// starts but comments can be appended to it forever, so a reply to an old
	// thread falls outside any newest-N slice of threads and would be missed.
	// Every inline comment, including a reply to a long-dormant thread, arrives
	// attached to a freshly submitted review, and reviews are submission
	// ordered. Sourcing them here makes thread age irrelevant.
	var latest time.Time
	for _, review := range pr.Reviews.Nodes {
		state := strings.ToUpper(review.State)
		// The baseline recorded at wait start is the reviewer's newest review in
		// any state on any commit, so a re-review can be recognized as newer.
		if strings.EqualFold(review.Author.Login, opts.Reviewer) && review.SubmittedAt.After(result.LatestReviewAt) {
			result.LatestReviewAt = review.SubmittedAt
		}
		// A review with no text of its own is just the wrapper GitHub creates
		// around an inline comment; its comments are reported below, so
		// counting the wrapper too would report one remark twice.
		if strings.TrimSpace(review.BodyText) != "" {
			result.Comments = appendPRComment(result.Comments, prGraphQLComment{
				ID: review.ID, CreatedAt: review.SubmittedAt, Author: review.Author,
			}, "review", opts)
		}
		if review.Comments.PageInfo.HasNextPage {
			return nil, errors.New("a review carries more than 100 comments; new comments cannot be detected without truncation")
		}
		for _, comment := range review.Comments.Nodes {
			result.Comments = appendPRComment(result.Comments, comment, "inline", opts)
		}
		if state == "COMMENTED" {
			continue
		}
		if !strings.EqualFold(review.Author.Login, opts.Reviewer) || review.Commit.OID != result.HeadSHA ||
			(state != "APPROVED" && state != "CHANGES_REQUESTED") || review.SubmittedAt.Before(latest) {
			continue
		}
		latest = review.SubmittedAt
		result.ReviewSubmittedAt = review.SubmittedAt
		if state == "APPROVED" {
			result.ReviewState = "approved"
		} else {
			result.ReviewState = "changes_requested"
		}
	}
	for _, comment := range pr.Comments.Nodes {
		result.Comments = appendPRComment(result.Comments, comment, "issue", opts)
	}
	sort.Slice(result.Comments, func(i, j int) bool {
		return result.Comments[i].CreatedAt.Before(result.Comments[j].CreatedAt)
	})
	return result, nil
}

// appendPRComment keeps only comments a human needs to answer. Bot authorship
// comes from __typename; the token owner is deliberately NOT filtered, because
// the operator and the agent share one token and the operator's own comment is
// the most actionable event there is. Self-waking is prevented by the baseline
// instead: anything present when the wait starts is never reported.
func appendPRComment(comments []prComment, node prGraphQLComment, kind string, opts prWaitOptions) []prComment {
	if node.ID == "" || node.Author.TypeName != "User" || opts.ignored(node.Author.Login) {
		return comments
	}
	return append(comments, prComment{
		ID: node.ID, Author: node.Author.Login, Kind: kind, CreatedAt: node.CreatedAt,
	})
}

func checkRunState(status, conclusion string) string {
	if !strings.EqualFold(status, "COMPLETED") {
		return checksPending
	}
	switch strings.ToUpper(conclusion) {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return checksGreen
	case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE":
		return checksFailed
	default:
		return checksPending
	}
}

func statusState(state string) string {
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return checksGreen
	case "FAILURE", "ERROR":
		return checksFailed
	default:
		return checksPending
	}
}

func summarizePRChecks(checks []prCheck) string {
	if len(checks) == 0 {
		return checksNone
	}
	result := checksGreen
	for _, check := range checks {
		if check.State == checksFailed {
			return checksFailed
		}
		if check.State != checksGreen {
			result = checksPending
		}
	}
	return result
}

// waitForPRActionable returns on the first actionable update. The error return
// is reserved for the waiter itself failing; every product outcome, including a
// timeout, comes back as a prOutcome so the caller can report it uniformly.
func waitForPRActionable(ctx context.Context, source prReadinessSource, opts prWaitOptions, progress io.Writer) (*prReadiness, prOutcome, error) {
	var lastLine, lastHead string
	var baseline map[string]bool
	var reviewBaseline time.Time
	var notedStaleVerdict bool
	last := &prReadiness{Number: strconv.Itoa(opts.Number), Reviewer: opts.Reviewer, CheckState: checksNone, ReviewState: "waiting"}

	for {
		observation, err := source.Fetch(ctx, opts)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return last, outcomeTimeout, nil
			}
			return last, "", err
		}
		last = observation

		if lastHead != "" && lastHead != observation.HeadSHA {
			// A new head invalidates prior approval and check results, but not
			// comments: a comment stays actionable regardless of what was pushed.
			fmt.Fprintf(progress, "head changed %s -> %s; reset\n", shortSHA(lastHead), shortSHA(observation.HeadSHA))
		}
		lastHead = observation.HeadSHA

		if line := readinessLine(observation); line != lastLine {
			fmt.Fprintln(progress, line)
			lastLine = line
		}

		if baseline == nil {
			baseline = make(map[string]bool, len(observation.Comments))
			for _, comment := range observation.Comments {
				baseline[comment.ID] = true
			}
			// Mirror the comment baseline for reviews: the reviewer's newest
			// review at wait start is stale context, so a verdict returns only
			// when it (or a re-review) is newer than this.
			reviewBaseline = observation.LatestReviewAt
		} else if fresh := unseenPRComments(observation.Comments, baseline); len(fresh) > 0 {
			observation.Comments = fresh
			return observation, outcomeComment, nil
		}

		// An existing verdict that a pending re-review holds back is not a return
		// but is easy to misread as a stuck wait; say so once when it first bites.
		if !notedStaleVerdict && hasReviewVerdict(observation) && !freshReviewVerdict(observation, reviewBaseline) {
			fmt.Fprintf(progress, "%s %s predates the pending re-review request; waiting for a new review\n",
				observation.Reviewer, observation.ReviewState)
			notedStaleVerdict = true
		}

		switch {
		case observation.State != "open":
			return observation, outcomeClosed, nil
		case observation.CheckState == checksFailed:
			return observation, outcomeChecksFailed, nil
		case observation.ReviewState == "changes_requested" && freshReviewVerdict(observation, reviewBaseline):
			return observation, outcomeChangesRequested, nil
		case observation.ready() && freshReviewVerdict(observation, reviewBaseline):
			return observation, outcomeApproved, nil
		}

		if err := waitPRPoll(ctx, opts.Interval); err != nil {
			return observation, outcomeTimeout, nil
		}
	}
}

// freshReviewVerdict reports whether the reviewer's current verdict should end
// the wait. A verdict is always the reviewer's current answer unless a re-review
// is pending, in which case only a verdict submitted after the baseline recorded
// at wait start counts; the pre-existing verdict is stale context. An
// already-approved PR with no pending re-review still returns immediately —
// approval is a state, not an event.
func freshReviewVerdict(observation *prReadiness, baseline time.Time) bool {
	if !observation.ReviewerRequested {
		return true
	}
	return observation.ReviewSubmittedAt.After(baseline)
}

func unseenPRComments(comments []prComment, baseline map[string]bool) []prComment {
	var fresh []prComment
	for _, comment := range comments {
		if !baseline[comment.ID] {
			fresh = append(fresh, comment)
		}
	}
	return fresh
}

func waitPRPoll(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func readinessLine(r *prReadiness) string {
	parts := make([]string, 0, len(r.Checks))
	for _, check := range r.Checks {
		parts = append(parts, check.Name+"="+check.State)
	}
	checks := "-"
	if len(parts) > 0 {
		checks = strings.Join(parts, ",")
	}
	line := fmt.Sprintf("pr=#%s head=%s state=%s draft=%t checks=%s [%s] review=%s reviewer=%s",
		r.Number, shortSHA(r.HeadSHA), r.State, r.Draft, r.CheckState, checks, r.ReviewState, r.Reviewer)
	// A re-review request is why an existing verdict does not end the wait, so
	// surface it; otherwise review=changes_requested with no return looks broken.
	if r.ReviewerRequested {
		line += " re-requested=true"
	}
	return line
}

// hasReviewVerdict reports whether the reviewer has left a verdict (approval or
// changes requested) as opposed to no review yet.
func hasReviewVerdict(r *prReadiness) bool {
	return r.ReviewState == "approved" || r.ReviewState == "changes_requested"
}

func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

func writePRHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn pr wait-ready <number-or-url> --reviewer <login> [options]

Wait until a pull request has an actionable update: a failing check, a review
requesting changes, a new human comment, or approval on a green exact head.

options:
  --repo [host/]owner/repository  required with a pull request number
  --reviewer login                required reviewer
  --timeout duration              maximum wait (default 30m)
  --interval duration             poll interval (default 20s)
  --ignore-author login           comment author to ignore (repeatable)
  --json                          emit the result as JSON on stdout

Bot comments are ignored. Comments already present when the wait starts are the
baseline and never reported; only comments posted during the wait are. A review
verdict present at wait start is likewise baselined: while the reviewer is
re-requested (a re-review is pending) the pre-existing verdict is stale and does
not end the wait; only a review submitted after the baseline does. When the
reviewer is not re-requested, an existing verdict returns immediately.

exit: 0 approved; 1 checks failed; 2 usage; 3 changes requested; 4 new comment;
      5 error; 124 timeout
`)
}
