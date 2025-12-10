package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
)

// ghSearchPR is the structure returned by gh search prs --json
type ghSearchPR struct {
	Number     int    `json:"number"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	IsDraft    bool   `json:"isDraft"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

// Fetcher fetches PRs from GitHub
type Fetcher struct {
	ghPath string
}

// NewFetcher creates a new GitHub PR fetcher
func NewFetcher() *Fetcher {
	ghPath, _ := exec.LookPath("gh")
	return &Fetcher{ghPath: ghPath}
}

// IsAvailable returns true if gh CLI is available
func (f *Fetcher) IsAvailable() bool {
	return f.ghPath != ""
}

// FetchAll fetches all PRs that need tracking
func (f *Fetcher) FetchAll() ([]*protocol.PR, error) {
	if !f.IsAvailable() {
		return nil, fmt.Errorf("gh CLI not available")
	}

	var allPRs []*protocol.PR

	// Fetch authored PRs
	authored, err := f.fetchAuthored()
	if err != nil {
		return nil, fmt.Errorf("fetch authored PRs: %w", err)
	}
	allPRs = append(allPRs, authored...)

	// Fetch review requests
	reviews, err := f.fetchReviewRequests()
	if err != nil {
		return nil, fmt.Errorf("fetch review requests: %w", err)
	}
	allPRs = append(allPRs, reviews...)

	return allPRs, nil
}

func (f *Fetcher) fetchAuthored() ([]*protocol.PR, error) {
	cmd := exec.Command(f.ghPath, "search", "prs",
		"--author", "@me",
		"--state", "open",
		"--limit", "50",
		"--json", "number,title,url,repository,isDraft")

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parsePRList(output, protocol.PRRoleAuthor)
}

func (f *Fetcher) fetchReviewRequests() ([]*protocol.PR, error) {
	cmd := exec.Command(f.ghPath, "search", "prs",
		"--review-requested", "@me",
		"--state", "open",
		"--limit", "50",
		"--json", "number,title,url,repository,isDraft")

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parsePRList(output, protocol.PRRoleReviewer)
}

func parsePRList(data []byte, role string) ([]*protocol.PR, error) {
	var ghPRs []ghSearchPR
	if err := json.Unmarshal(data, &ghPRs); err != nil {
		return nil, err
	}

	var prs []*protocol.PR
	for _, gh := range ghPRs {
		// Skip draft PRs
		if gh.IsDraft {
			continue
		}
		pr := convertPR(gh, role)
		prs = append(prs, pr)
	}
	return prs, nil
}

func convertPR(gh ghSearchPR, role string) *protocol.PR {
	repo := gh.Repository.NameWithOwner

	// Simplified state: authored PRs need attention, review requests need review
	var state, reason string
	if role == protocol.PRRoleAuthor {
		state = protocol.StateWaiting
		reason = "" // Could be any reason, we don't have detailed status
	} else {
		state = protocol.StateWaiting
		reason = protocol.PRReasonReviewNeeded
	}

	return &protocol.PR{
		ID:          fmt.Sprintf("%s#%d", repo, gh.Number),
		Repo:        repo,
		Number:      gh.Number,
		Title:       gh.Title,
		URL:         gh.URL,
		Role:        role,
		State:       state,
		Reason:      reason,
		LastUpdated: time.Now(),
		LastPolled:  time.Now(),
	}
}

// PRDetails contains detailed PR status from GitHub API
type PRDetails struct {
	Mergeable      *bool
	MergeableState string
	CIStatus       string
	ReviewStatus   string
}

// FetchPRDetails fetches detailed status for a PR via gh api
func (f *Fetcher) FetchPRDetails(repo string, number int) (*PRDetails, error) {
	if !f.IsAvailable() {
		return nil, fmt.Errorf("gh CLI not available")
	}

	// Fetch PR details
	prCmd := exec.Command(f.ghPath, "api",
		fmt.Sprintf("repos/%s/pulls/%d", repo, number),
		"--jq", "{mergeable, mergeable_state, head_sha: .head.sha}")

	prOutput, err := prCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("fetch PR: %w", err)
	}

	var prData struct {
		Mergeable      *bool  `json:"mergeable"`
		MergeableState string `json:"mergeable_state"`
		HeadSHA        string `json:"head_sha"`
	}
	if err := json.Unmarshal(prOutput, &prData); err != nil {
		return nil, fmt.Errorf("parse PR: %w", err)
	}

	details := &PRDetails{
		Mergeable:      prData.Mergeable,
		MergeableState: prData.MergeableState,
	}

	// Fetch CI status
	if prData.HeadSHA != "" {
		ciCmd := exec.Command(f.ghPath, "api",
			fmt.Sprintf("repos/%s/commits/%s/check-runs", repo, prData.HeadSHA),
			"--jq", "[.check_runs[].conclusion] | if length == 0 then \"none\" elif all(. == \"success\") then \"success\" elif any(. == null) then \"pending\" else \"failure\" end")

		ciOutput, err := ciCmd.Output()
		if err == nil {
			details.CIStatus = strings.TrimSpace(string(ciOutput))
		}
	}

	// Fetch review status
	reviewCmd := exec.Command(f.ghPath, "api",
		fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, number),
		"--jq", `[.[] | select(.state != "COMMENTED")] | if length == 0 then "none" elif any(.state == "CHANGES_REQUESTED") then "changes_requested" elif any(.state == "APPROVED") then "approved" else "pending" end`)

	reviewOutput, err := reviewCmd.Output()
	if err == nil {
		details.ReviewStatus = strings.TrimSpace(string(reviewOutput))
	}

	return details, nil
}

// ApprovePR approves a pull request
func (f *Fetcher) ApprovePR(repo string, number int) error {
	if !f.IsAvailable() {
		return fmt.Errorf("gh CLI not available")
	}

	cmd := exec.Command(f.ghPath, "pr", "review",
		"--repo", repo,
		"--approve",
		fmt.Sprintf("%d", number))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("approve failed: %s", string(output))
	}
	return nil
}

// MergePR merges a pull request
func (f *Fetcher) MergePR(repo string, number int, method string) error {
	if !f.IsAvailable() {
		return fmt.Errorf("gh CLI not available")
	}

	// Default to squash if not specified
	if method == "" {
		method = "squash"
	}

	// Validate merge method
	validMethods := map[string]bool{
		"squash": true,
		"merge":  true,
		"rebase": true,
	}
	if !validMethods[method] {
		return fmt.Errorf("invalid merge method: %s (must be squash, merge, or rebase)", method)
	}

	cmd := exec.Command(f.ghPath, "pr", "merge",
		"--repo", repo,
		"--"+method, // --squash, --merge, or --rebase
		"--delete-branch",
		fmt.Sprintf("%d", number))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("merge failed: %s", string(output))
	}
	return nil
}
