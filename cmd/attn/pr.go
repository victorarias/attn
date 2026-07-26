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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/config"
)

const (
	prWaitExitApproved         = 0
	prWaitExitChecksFailed     = 1
	prWaitExitUsage            = 2
	prWaitExitChangesRequested = 3
	prWaitExitComment          = 4
	prWaitExitError            = 5
	prWaitExitBotComment       = 6
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
	outcomeBotComment       prOutcome = "bot_comment"
	outcomeClosed           prOutcome = "closed"
	outcomeTimeout          prOutcome = "timeout"
)

// prOutcomeRanking is the whole precedence order, in one place. One poll can see
// several events at once — CI can fail in the same 20 seconds someone comments —
// and the exit code can only carry one, so the ranking decides which. It reads
// by who is waiting on whom: the pull request being gone or broken first, then a
// verdict, then a human who asked something and is waiting for an answer.
//
// Approval ranks below a human comment because approval says nothing needs doing
// and reporting it over a fresh question would bury the question. It ranks above
// a bot comment for the mirror-image reason: a bot is not waiting for an answer,
// its findings usually arrive as a check as well, and it comments on nearly every
// push — ranking it higher would report "a bot said something" instead of "you
// can merge" in the ordinary case.
//
// Nothing is lost to the ranking: every event the ending poll saw is reported,
// so a caller that gets `comment` still sees the approval that came with it.
var prOutcomeRanking = []prOutcome{
	outcomeClosed,
	outcomeChecksFailed,
	outcomeChangesRequested,
	outcomeComment,
	outcomeApproved,
	outcomeBotComment,
}

// rankPROutcomes returns the highest-ranked event, and the events sorted by that
// same ranking so the report reads in the order the operator should read it.
func rankPROutcomes(events []prOutcome) (prOutcome, []prOutcome) {
	if len(events) == 0 {
		return "", nil
	}
	present := make(map[prOutcome]bool, len(events))
	for _, event := range events {
		present[event] = true
	}
	ranked := make([]prOutcome, 0, len(events))
	for _, candidate := range prOutcomeRanking {
		if present[candidate] {
			ranked = append(ranked, candidate)
		}
	}
	if len(ranked) == 0 {
		return events[0], events
	}
	return ranked[0], ranked
}

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
	case outcomeBotComment:
		return prWaitExitBotComment
	case outcomeTimeout:
		return prWaitExitTimeout
	default:
		return prWaitExitError
	}
}

type prCheck struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// URL is where the run's logs are. A failed check whose logs the caller has to
	// go hunting for is a follow-up query in everything but name.
	URL string `json:"url,omitempty"`
}

// prComment is any commentary surface: a standalone PR comment, an inline
// review-thread comment, or a review submitted without a verdict.
//
// A review that carries a verdict is not commentary. Its body is the verdict's
// explanation, and reporting it here as well would turn one review into two
// events — which is how an approval used to end the wait as "a new comment".
type prComment struct {
	ID        string    `json:"-"`
	Author    string    `json:"author"`
	Kind      string    `json:"kind"`
	Bot       bool      `json:"bot"`
	CreatedAt time.Time `json:"created_at"`
	// Body is what the comment actually says, and Location is `path:line` for an
	// inline comment. Both are carried so the caller can act on the remark instead
	// of going back to GitHub to read it.
	Body     string `json:"body,omitempty"`
	Location string `json:"location,omitempty"`
}

// isTrackedReviewerVerdict reports whether a review is the one the waiter reads
// as the reviewer's answer. Only those reviews are exempt from being reported as
// commentary; a `COMMENTED` review from the same person is a remark like any
// other, and is what the reviewer uses to say something without answering yet.
func isTrackedReviewerVerdict(author, state string, opts prWaitOptions) bool {
	if !strings.EqualFold(author, opts.Reviewer) {
		return false
	}
	return state == "APPROVED" || state == "CHANGES_REQUESTED"
}

func humanPRComments(comments []prComment) []prComment {
	return filterPRComments(comments, false)
}

func botPRComments(comments []prComment) []prComment {
	return filterPRComments(comments, true)
}

func filterPRComments(comments []prComment, bot bool) []prComment {
	result := make([]prComment, 0, len(comments))
	for _, comment := range comments {
		if comment.Bot == bot {
			result = append(result, comment)
		}
	}
	return result
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
	// ReviewBody is the verdict's own text — for changes_requested, the thing the
	// caller has to act on. It was already being fetched and dropped, which meant
	// every caller learning "changes requested" had to ask GitHub what was said.
	ReviewBody string
	// URL is the pull request's web address, so a caller reporting the outcome
	// does not have to assemble one from host, owner, name and number.
	URL string
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
	// CursorDir is where waits remember what they have already reported, so a
	// second call resumes instead of re-baselining. Empty disables the memory
	// entirely, which is what tests use; the CLI resolves it once at entry.
	CursorDir string
	// Since overrides the remembered position: report anything after this instant
	// and ignore the cursor. Reset discards the cursor and baselines from the
	// pull request's current state, as a first-ever call would.
	Since time.Time
	Reset bool
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

	// Resolve the cursor directory here and pass it down, so nothing below this
	// line can reach a data dir on its own.
	opts.CursorDir = filepath.Join(config.DataDir(), "pr-wait")

	var cursor prWaitCursor
	if !opts.Reset {
		loaded, err := loadPRWaitCursor(opts.CursorDir, opts)
		if err != nil {
			// A cursor that cannot be read costs history, not correctness: the wait
			// falls back to baselining from the PR's current state.
			fmt.Fprintf(stderr, "pr wait-ready: %v; starting from the current state\n", err)
		}
		cursor = loaded
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	result, err := waitForPRActionable(ctx, ghPRReadinessSource{}, opts, cursor, progress)
	if err != nil {
		fmt.Fprintf(stderr, "pr wait-ready: %v\n", err)
		return prWaitExitError
	}
	if err := savePRWaitCursor(opts.CursorDir, opts, result.Cursor, time.Now()); err != nil {
		// Losing the cursor means the next wait re-baselines, which is the old
		// behavior — worth a warning, not worth discarding this wait's answer.
		fmt.Fprintf(stderr, "pr wait-ready: could not save cursor: %v\n", err)
	}
	return reportPROutcome(result, opts, stdout)
}

func reportPROutcome(wait prWaitResult, opts prWaitOptions, stdout io.Writer) int {
	result, outcome, events := wait.Observation, wait.Outcome, wait.Events
	detail := describePROutcome(result, outcome, opts)
	if opts.JSON {
		// "comments" is everything that arrived during the wait, whichever event
		// won: the exit code can only name one, and a caller that has to re-query
		// for the rest is being made to fetch what this poll already saw. The
		// waiter has already reduced Comments to the unseen ones.
		fresh := result.Comments
		if fresh == nil {
			fresh = []prComment{}
		}
		reported := make([]string, 0, len(events))
		for _, event := range events {
			reported = append(reported, string(event))
		}
		payload := map[string]any{
			"outcome": string(outcome),
			"events":  reported,
			"pr":      result.Number,
			"url":     result.URL,
			"head":    result.HeadSHA,
			"state":   result.State,
			"draft":   result.Draft,
			"detail":  detail,
			"checks": map[string]any{
				"state":  result.CheckState,
				"items":  result.Checks,
				"failed": failedChecks(result.Checks),
			},
			"review": map[string]any{
				"state":    result.ReviewState,
				"reviewer": result.Reviewer,
				// The verdict's own text. On changes_requested it is the whole
				// point: what to fix.
				"body": result.ReviewBody,
			},
			"comments": fresh,
			// The position this wait leaves behind. Echoing it makes the memory
			// inspectable, and it is what a caller passes back as --since to replay
			// from a known point.
			"cursor": wait.Cursor,
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			return prWaitExitError
		}
		return outcome.exitCode()
	}
	fmt.Fprintf(stdout, "%s: %s\n", outcome, detail)
	// Plain text gets the same completeness as JSON: the events the ranking did
	// not pick are still things that happened, and a reader who only sees the
	// winner will go looking for them.
	for _, event := range events {
		if event == outcome {
			continue
		}
		fmt.Fprintf(stdout, "also %s: %s\n", event, describePROutcome(result, event, opts))
	}
	writePRContent(stdout, result, events)
	return outcome.exitCode()
}

// writePRContent prints what was actually said. Without it the caller knows a
// remark exists and has to go read it, which is the follow-up query this command
// exists to remove — and stdout is the only surface a shell caller has.
func writePRContent(stdout io.Writer, result *prReadiness, events []prOutcome) {
	reported := make(map[prOutcome]bool, len(events))
	for _, event := range events {
		reported[event] = true
	}
	if failed := failedChecks(result.Checks); reported[outcomeChecksFailed] && len(failed) > 0 {
		for _, check := range failed {
			if check.URL != "" {
				fmt.Fprintf(stdout, "  %s %s\n", check.Name, check.URL)
				continue
			}
			fmt.Fprintf(stdout, "  %s\n", check.Name)
		}
	}
	if (reported[outcomeChangesRequested] || reported[outcomeApproved]) && result.ReviewBody != "" {
		fmt.Fprintf(stdout, "  --- %s ---\n%s\n", result.Reviewer, indentPRBody(result.ReviewBody))
	}
	for _, comment := range result.Comments {
		if comment.Bot && !reported[outcomeBotComment] {
			continue
		}
		if !comment.Bot && !reported[outcomeComment] {
			continue
		}
		where := comment.Author
		if comment.Location != "" {
			where += " on " + comment.Location
		}
		fmt.Fprintf(stdout, "  --- %s ---\n", where)
		if comment.Body != "" {
			fmt.Fprintln(stdout, indentPRBody(comment.Body))
		}
	}
	if result.URL != "" {
		fmt.Fprintf(stdout, "%s\n", result.URL)
	}
}

func indentPRBody(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
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
		return describePRComments(humanPRComments(result.Comments))
	case outcomeBotComment:
		return describePRComments(botPRComments(result.Comments))
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

// failedChecks is the failing subset with its log URLs, reported separately so a
// caller does not have to filter a hundred green rows to find what broke.
func failedChecks(checks []prCheck) []prCheck {
	failed := make([]prCheck, 0)
	for _, check := range checks {
		if check.State == checksFailed {
			failed = append(failed, check)
		}
	}
	return failed
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
	reset := fs.Bool("reset", false, "forget what earlier waits reported and baseline from the current state")
	since := fs.String("since", "", "report anything after this RFC3339 instant instead of resuming")
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
		Reset:         *reset,
	}
	if strings.TrimSpace(*since) != "" {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(*since))
		if err != nil {
			return prWaitOptions{}, fmt.Errorf("--since must be an RFC3339 timestamp: %w", err)
		}
		opts.Since = at
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
//
// It asks for the text too — review bodies, comment bodies, an inline comment's
// file and line, a failing check's URL. A caller that has to fetch the words
// after being told a comment exists is making the second round trip this command
// exists to remove, and it is the same round trip either way: these are fields on
// objects the query already walks.
const prSnapshotQuery = `
query($owner:String!,$name:String!,$number:Int!){
  repository(owner:$owner,name:$name){
    pullRequest(number:$number){
      number state isDraft headRefOid url
      commits(last:1){nodes{commit{statusCheckRollup{contexts(first:100){
        pageInfo{hasNextPage}
        nodes{__typename ... on CheckRun{name status conclusion detailsUrl} ... on StatusContext{context state targetUrl}}
      }}}}}
      reviewRequests(first:100){nodes{requestedReviewer{__typename ... on User{login}}}}
      reviews(last:100){nodes{id state bodyText submittedAt author{__typename login} commit{oid}
        comments(first:100){pageInfo{hasNextPage} nodes{id createdAt bodyText path line originalLine author{__typename login}}}}}
      comments(last:100){nodes{id createdAt bodyText author{__typename login}}}
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
	ID           string          `json:"id"`
	CreatedAt    time.Time       `json:"createdAt"`
	BodyText     string          `json:"bodyText"`
	Path         string          `json:"path"`
	Line         *int            `json:"line"`
	OriginalLine *int            `json:"originalLine"`
	Author       prGraphQLAuthor `json:"author"`
}

// location renders where an inline comment sits. GitHub nulls `line` once the
// comment's hunk is outdated, and then `originalLine` is the only anchor left.
func (c prGraphQLComment) location() string {
	if c.Path == "" {
		return ""
	}
	line := c.Line
	if line == nil {
		line = c.OriginalLine
	}
	if line == nil {
		return c.Path
	}
	return fmt.Sprintf("%s:%d", c.Path, *line)
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
					URL            string      `json:"url"`
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
											DetailsURL string `json:"detailsUrl"`
											TargetURL  string `json:"targetUrl"`
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
		URL: pr.URL,
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
				name, state, url := "status:"+check.Context, statusState(check.State), check.TargetURL
				if check.TypeName == "CheckRun" {
					name, state, url = "check:"+check.Name, checkRunState(check.Status, check.Conclusion), check.DetailsURL
				}
				result.Checks = append(result.Checks, prCheck{Name: name, State: state, URL: url})
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
		//
		// The tracked reviewer's verdict is skipped for the same reason from the
		// other direction: its body is the verdict's own explanation, and the
		// verdict event already sends the caller to read it. Another author's
		// approval is not tracked as a verdict at all, so their prose is
		// commentary and stays reported.
		if strings.TrimSpace(review.BodyText) != "" && !isTrackedReviewerVerdict(review.Author.Login, state, opts) {
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
		result.ReviewBody = strings.TrimSpace(review.BodyText)
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

// appendPRComment records a comment and who wrote it. Bot authorship comes from
// __typename and separates the two comment events rather than dropping one: a
// bot's remark is real news (a doctor report, a coverage regression) but it is
// ranked below a human's and carries its own exit code, so a caller can act on
// one and ignore the other. `--ignore-author` drops either kind.
//
// The token owner is deliberately NOT filtered, because the operator and the
// agent share one token and the operator's own comment is the most actionable
// event there is. Self-waking is prevented by the baseline instead: anything
// present when the wait starts is never reported.
func appendPRComment(comments []prComment, node prGraphQLComment, kind string, opts prWaitOptions) []prComment {
	if node.ID == "" || opts.ignored(node.Author.Login) {
		return comments
	}
	return append(comments, prComment{
		ID:        node.ID,
		Author:    node.Author.Login,
		Kind:      kind,
		Bot:       node.Author.TypeName != "User",
		CreatedAt: node.CreatedAt,
		Body:      strings.TrimSpace(node.BodyText),
		Location:  node.location(),
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

// prWaitResult is one wait's answer: what happened, everything that happened,
// and the cursor the next wait should start from.
type prWaitResult struct {
	Observation *prReadiness
	Outcome     prOutcome
	Events      []prOutcome
	Cursor      prWaitCursor
}

// waitForPRActionable returns on the first poll that sees an actionable update.
// It returns every event that poll saw, ranked, plus the winner the exit code
// reports; a caller that only wants the winner can ignore the rest. The error
// return is reserved for the waiter itself failing; every product outcome,
// including a timeout, comes back as a prOutcome so the caller can report it
// uniformly.
//
// `cursor` is what a previous wait already reported, and starting from it is what
// closes the window between two calls. Zero means "baseline from the pull
// request's current state", which is right for a first call: nothing that
// happened before the agent started watching is news.
func waitForPRActionable(ctx context.Context, source prReadinessSource, opts prWaitOptions, cursor prWaitCursor, progress io.Writer) (prWaitResult, error) {
	var lastLine, lastHead string
	var baseline map[string]bool
	var reviewBaseline time.Time
	var notedStaleVerdict bool
	last := &prReadiness{Number: strconv.Itoa(opts.Number), Reviewer: opts.Reviewer, CheckState: checksNone, ReviewState: "waiting"}
	if !opts.Since.IsZero() {
		// --since is the manual override: report everything after this instant and
		// forget what any previous wait recorded.
		cursor = prWaitCursor{VerdictAt: opts.Since}
		baseline = map[string]bool{}
		reviewBaseline = opts.Since
	} else if !cursor.empty() {
		baseline = cursor.seenComments()
		reviewBaseline = cursor.VerdictAt
	}

	for {
		observation, err := source.Fetch(ctx, opts)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return prWaitResult{Observation: last, Outcome: outcomeTimeout, Events: []prOutcome{outcomeTimeout}, Cursor: cursor}, nil
			}
			return prWaitResult{Observation: last}, err
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
			// The baseline is also what the next wait must not be told again, so
			// it goes straight into the cursor: a wait that ends in a timeout
			// reports nothing, and without this the following call would rebuild
			// the same baseline from a later state and swallow whatever landed in
			// between.
			cursor.CommentIDs = append(cursor.CommentIDs, prCommentIDs(observation.Comments)...)
			// Mirror the comment baseline for reviews: the reviewer's newest
			// review at wait start is stale context, so a verdict returns only
			// when it (or a re-review) is newer than this.
			reviewBaseline = observation.LatestReviewAt
			// Nothing already on the PR is news, so this poll contributes no
			// comment event. Dropping them here rather than skipping the event
			// tests below also keeps the report honest if some other event ends
			// the wait on this very poll: no comment arrived during it.
			observation.Comments = nil
		} else {
			observation.Comments = unseenPRComments(observation.Comments, baseline, opts.Since)
		}

		// An existing verdict that a pending re-review holds back is not a return
		// but is easy to misread as a stuck wait; say so once when it first bites.
		if !notedStaleVerdict && hasReviewVerdict(observation) && !freshReviewVerdict(observation, reviewBaseline) {
			fmt.Fprintf(progress, "%s %s predates the pending re-review request; waiting for a new review\n",
				observation.Reviewer, observation.ReviewState)
			notedStaleVerdict = true
		}

		// Collect the whole poll before deciding anything. Every branch here used
		// to return the moment it matched, in two separate places, so whichever
		// event the code happened to test first won and the others were never
		// looked at. The ranking is the only thing that chooses now.
		var events []prOutcome
		if observation.State != "open" {
			events = append(events, outcomeClosed)
		}
		// A failing check is a condition, not an occurrence: it stays true until
		// someone pushes. Reporting it again for the same checks on the same commit
		// would turn a second wait into an instant return with nothing new in it,
		// which is the hot loop a caller uses this command to avoid.
		if observation.CheckState == checksFailed && !cursor.sameFailure(observation.HeadSHA, observation.Checks) {
			events = append(events, outcomeChecksFailed)
		}
		if freshReviewVerdict(observation, reviewBaseline) {
			switch {
			case observation.ReviewState == "changes_requested":
				events = append(events, outcomeChangesRequested)
			case observation.ready():
				events = append(events, outcomeApproved)
			}
		}
		if len(humanPRComments(observation.Comments)) > 0 {
			events = append(events, outcomeComment)
		}
		if len(botPRComments(observation.Comments)) > 0 {
			events = append(events, outcomeBotComment)
		}
		if winner, ranked := rankPROutcomes(events); winner != "" {
			return prWaitResult{
				Observation: observation,
				Outcome:     winner,
				Events:      ranked,
				Cursor:      advancePRWaitCursor(cursor, observation, ranked),
			}, nil
		}

		if err := waitPRPoll(ctx, opts.Interval); err != nil {
			return prWaitResult{Observation: observation, Outcome: outcomeTimeout, Events: []prOutcome{outcomeTimeout}, Cursor: cursor}, nil
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

// unseenPRComments keeps the comments the caller has not been told about. `since`
// is the --since override: it selects by arrival time instead of by identity, so
// a caller can replay a window without knowing any comment IDs.
func unseenPRComments(comments []prComment, baseline map[string]bool, since time.Time) []prComment {
	var fresh []prComment
	for _, comment := range comments {
		if baseline[comment.ID] {
			continue
		}
		if !since.IsZero() && !comment.CreatedAt.After(since) {
			continue
		}
		fresh = append(fresh, comment)
	}
	return fresh
}

func prCommentIDs(comments []prComment) []string {
	ids := make([]string, 0, len(comments))
	for _, comment := range comments {
		ids = append(ids, comment.ID)
	}
	return ids
}

// advancePRWaitCursor records what this wait reported, and nothing more. A poll
// sees more than it returns — a failure it recognized as already-reported, a
// verdict held back by a pending re-review — and folding those in would mark them
// seen without anyone having been told.
func advancePRWaitCursor(cursor prWaitCursor, observation *prReadiness, events []prOutcome) prWaitCursor {
	reported := make(map[prOutcome]bool, len(events))
	for _, event := range events {
		reported[event] = true
	}
	// observation.Comments is already reduced to the ones being reported, and a
	// poll carrying both human and bot remarks reports them together.
	if reported[outcomeComment] || reported[outcomeBotComment] {
		cursor.CommentIDs = append(cursor.CommentIDs, prCommentIDs(observation.Comments)...)
	}
	if reported[outcomeApproved] || reported[outcomeChangesRequested] {
		cursor.VerdictAt = observation.ReviewSubmittedAt
	}
	if reported[outcomeChecksFailed] {
		cursor.FailureHead = observation.HeadSHA
		cursor.FailureChecks = failedCheckNames(observation.Checks)
	}
	return cursor
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

Wait until a pull request has an actionable update: it closes, a check fails, the
reviewer requests changes, a human comments, a bot comments, or the reviewer
approves a green exact head.

options:
  --repo [host/]owner/repository  required with a pull request number
  --reviewer login                required reviewer
  --timeout duration              maximum wait (default 30m)
  --interval duration             poll interval (default 20s)
  --ignore-author login           comment author to ignore (repeatable)
  --json                          emit the result as JSON on stdout
  --reset                         forget earlier waits; baseline from now
  --since RFC3339                 report anything after this instant instead

One poll can see several of these at once. The exit code reports the highest
ranked: closed, checks failed, changes requested, human comment, approved, bot
comment. A human comment outranks approval because someone is waiting for an
answer; a bot comment ranks last because nobody is. Every event that poll saw is
still reported — "also <event>: ..." on stdout, "events" in --json — so an
approval that arrives alongside a comment is never lost to the one the exit code
names. The reviewer's own approval or changes-requested is one event, not two:
its body is the verdict's explanation, not a separate comment.

A bot comment ends the wait with its own exit code, so a caller can act on a
human's remark and skip a doctor report; --ignore-author drops either kind.
Comments already present when the wait starts are the baseline and never
reported; only comments posted during the wait are. A review verdict present at
wait start is likewise baselined: while the reviewer is re-requested (a re-review
is pending) the pre-existing verdict is stale and does not end the wait; only a
review submitted after the baseline does. When the reviewer is not re-requested,
an existing verdict returns immediately.

Successive waits on the same pull request resume rather than re-baseline. Each
wait records what it reported under the data dir, so a remark that lands while the
caller is answering the previous one is still reported by the next wait instead of
being absorbed into a fresh baseline. The same memory keeps a failing check from
returning instantly a second time for the same checks on the same commit; a
different failure, or the same one on a new commit, is reported again. --json
echoes the recorded position; --reset discards it and --since replays from an
instant of your choosing.

Also printed on stdout: comment bodies with their file:line when inline, the
verdict's own text, failing check names with their URLs, and the pull request URL
— so acting on the result needs no second query.

exit: 0 approved; 1 checks failed; 2 usage; 3 changes requested; 4 human comment;
      5 error; 6 bot comment; 124 timeout
`)
}
