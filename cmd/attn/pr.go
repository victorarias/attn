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
	prWaitExitFailed  = 1
	prWaitExitUsage   = 2
	prWaitExitTimeout = 124

	checksNone    = "none"
	checksPending = "pending"
	checksGreen   = "green"
	checksFailed  = "failed"
)

type prCheck struct{ Name, State string }

type prReadiness struct {
	Number, State, HeadSHA, CheckState, Reviewer, ReviewState string
	Draft                                                     bool
	Checks                                                    []prCheck
}

func (r *prReadiness) ready() bool {
	return r.State == "open" && !r.Draft && r.CheckState == checksGreen && r.ReviewState == "approved"
}

type prReadinessSource interface {
	Fetch(context.Context, prWaitOptions) (*prReadiness, error)
}

type prWaitOptions struct {
	Target, Repo, Reviewer string
	Timeout, Interval      time.Duration
}

type ghPRReadinessSource struct{}

var errPRChecksFailed = errors.New("checks failed")

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

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	result, err := waitForPRReady(ctx, ghPRReadinessSource{}, opts, stdout)
	switch {
	case err == nil:
		fmt.Fprintf(stdout, "READY pr=#%s head=%s checks=%d approval=%s\n", result.Number, result.HeadSHA, len(result.Checks), result.Reviewer)
		return 0
	case errors.Is(err, context.DeadlineExceeded):
		fmt.Fprintf(stderr, "pr wait-ready: timed out after %s\n", opts.Timeout)
		return prWaitExitTimeout
	case errors.Is(err, errPRChecksFailed):
		fmt.Fprintf(stderr, "pr wait-ready: checks failed on current head %s\n", result.HeadSHA)
		return prWaitExitFailed
	default:
		fmt.Fprintf(stderr, "pr wait-ready: %v\n", err)
		return prWaitExitFailed
	}
}

func isHelpArg(arg string) bool { return arg == "-h" || arg == "--help" }

func parsePRWaitArgs(args []string) (prWaitOptions, error) {
	fs := flag.NewFlagSet("pr wait-ready", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", "", "[host/]owner/repository")
	reviewer := fs.String("reviewer", "", "required reviewer login")
	timeout := fs.Duration("timeout", 30*time.Minute, "maximum wait")
	interval := fs.Duration("interval", 20*time.Second, "poll interval")

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

	if strings.HasPrefix(target, "https://") {
		host, owner, repository, number, err := automation.ParsePullRequestURL(target)
		if err != nil {
			return prWaitOptions{}, err
		}
		target = fmt.Sprintf("https://%s/%s/%s/pull/%d", host, owner, repository, number)
	} else {
		number, err := strconv.Atoi(target)
		if err != nil || number <= 0 {
			return prWaitOptions{}, errors.New("pull request number must be positive")
		}
		if strings.TrimSpace(*repo) == "" {
			return prWaitOptions{}, errors.New("--repo is required when the target is a number")
		}
	}
	return prWaitOptions{Target: target, Repo: strings.TrimSpace(*repo), Reviewer: strings.TrimSpace(*reviewer), Timeout: *timeout, Interval: *interval}, nil
}

func (ghPRReadinessSource) Fetch(ctx context.Context, opts prWaitOptions) (*prReadiness, error) {
	args := []string{"pr", "view", opts.Target}
	if opts.Repo != "" {
		args = append(args, "--repo", opts.Repo)
	}
	args = append(args, "--json", "number,state,isDraft,headRefOid,statusCheckRollup,reviews")
	output, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("gh pr view: %s", strings.TrimSpace(string(output)))
	}
	return parseGHPRReadiness(output, opts.Reviewer)
}

func parseGHPRReadiness(output []byte, reviewer string) (*prReadiness, error) {
	var payload struct {
		Number     json.Number `json:"number"`
		State      string      `json:"state"`
		IsDraft    bool        `json:"isDraft"`
		HeadRefOID string      `json:"headRefOid"`
		Checks     []struct {
			TypeName   string `json:"__typename"`
			Name       string
			Context    string
			Status     string
			Conclusion string
			State      string
		} `json:"statusCheckRollup"`
		Reviews []struct {
			State       string
			SubmittedAt time.Time
			Author      struct{ Login string }
			Commit      struct{ OID string }
		}
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("parse gh pr view: %w", err)
	}
	if payload.Number == "" || payload.HeadRefOID == "" {
		return nil, errors.New("gh pr view returned no PR number or head SHA")
	}
	// gh requests the first 100 nodes for both connections but omits pageInfo
	// from its JSON output. Refuse the boundary instead of silently accepting a
	// snapshot that may have hidden checks or reviews.
	if len(payload.Checks) >= 100 || len(payload.Reviews) >= 100 {
		return nil, errors.New("PR has at least 100 checks or reviews; readiness cannot be verified without truncation")
	}

	result := &prReadiness{
		Number: payload.Number.String(), State: strings.ToLower(payload.State), Draft: payload.IsDraft,
		HeadSHA: payload.HeadRefOID, Reviewer: reviewer, ReviewState: "waiting",
	}
	for _, check := range payload.Checks {
		name, state := "status:"+check.Context, statusState(check.State)
		if check.TypeName == "CheckRun" {
			name, state = "check:"+check.Name, checkRunState(check.Status, check.Conclusion)
		}
		result.Checks = append(result.Checks, prCheck{Name: name, State: state})
	}
	sort.Slice(result.Checks, func(i, j int) bool { return result.Checks[i].Name < result.Checks[j].Name })
	result.CheckState = summarizePRChecks(result.Checks)

	var latest time.Time
	for _, review := range payload.Reviews {
		state := strings.ToUpper(review.State)
		if !strings.EqualFold(review.Author.Login, reviewer) || review.Commit.OID != result.HeadSHA ||
			(state != "APPROVED" && state != "CHANGES_REQUESTED") || review.SubmittedAt.Before(latest) {
			continue
		}
		latest = review.SubmittedAt
		if state == "APPROVED" {
			result.ReviewState = "approved"
		} else {
			result.ReviewState = "changes_requested"
		}
	}
	return result, nil
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

func waitForPRReady(ctx context.Context, source prReadinessSource, opts prWaitOptions, out io.Writer) (*prReadiness, error) {
	var lastLine, lastHead string
	for {
		observation, err := source.Fetch(ctx, opts)
		if err != nil {
			return observation, err
		}
		if lastHead != "" && lastHead != observation.HeadSHA {
			fmt.Fprintf(out, "head changed %s -> %s; reset\n", shortSHA(lastHead), shortSHA(observation.HeadSHA))
		}
		lastHead = observation.HeadSHA
		line := readinessLine(observation)
		if line != lastLine {
			fmt.Fprintln(out, line)
			lastLine = line
		}
		if observation.State != "open" {
			return observation, fmt.Errorf("pull request is %s", observation.State)
		}
		if observation.CheckState == checksFailed {
			return observation, errPRChecksFailed
		}
		if observation.ready() {
			return observation, nil
		}
		if err := waitPRPoll(ctx, opts.Interval); err != nil {
			return observation, err
		}
	}
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
	return fmt.Sprintf("pr=#%s head=%s state=%s draft=%t checks=%s [%s] review=%s reviewer=%s",
		r.Number, shortSHA(r.HeadSHA), r.State, r.Draft, r.CheckState, checks, r.ReviewState, r.Reviewer)
}

func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

func writePRHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn pr wait-ready <number-or-url> --reviewer <login> [options]

Wait until one pull request snapshot has green checks and an approval from the
required reviewer on that exact head commit.

options:
  --repo [host/]owner/repository  required with a pull request number
  --reviewer login               required reviewer
  --timeout duration             maximum wait (default 30m)
  --interval duration            poll interval (default 20s)

exit: 0 ready; 1 failed or closed; 2 usage; 124 timeout
`)
}
