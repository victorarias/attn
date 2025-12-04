package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
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
