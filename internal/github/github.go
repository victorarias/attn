package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
)

// ghPR is the structure returned by gh pr list --json
type ghPR struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	HeadRepository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	StatusCheckRollup struct {
		State string `json:"state"` // SUCCESS, FAILURE, PENDING
	} `json:"statusCheckRollup"`
	ReviewDecision string `json:"reviewDecision"` // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED
	Mergeable      string `json:"mergeable"`      // MERGEABLE, CONFLICTING, UNKNOWN
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
	cmd := exec.Command(f.ghPath, "pr", "list",
		"--author", "@me",
		"--state", "open",
		"--json", "number,title,url,headRepository,statusCheckRollup,reviewDecision,mergeable")

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parsePRList(output, protocol.PRRoleAuthor)
}

func (f *Fetcher) fetchReviewRequests() ([]*protocol.PR, error) {
	cmd := exec.Command(f.ghPath, "pr", "list",
		"--search", "review-requested:@me",
		"--state", "open",
		"--json", "number,title,url,headRepository,statusCheckRollup,reviewDecision,mergeable")

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parsePRList(output, protocol.PRRoleReviewer)
}

func parsePRList(data []byte, role string) ([]*protocol.PR, error) {
	var ghPRs []ghPR
	if err := json.Unmarshal(data, &ghPRs); err != nil {
		return nil, err
	}

	var prs []*protocol.PR
	for _, gh := range ghPRs {
		pr := convertPR(gh, role)
		prs = append(prs, pr)
	}
	return prs, nil
}

func parsePR(data []byte, role string) (*protocol.PR, error) {
	var gh ghPR
	if err := json.Unmarshal(data, &gh); err != nil {
		return nil, err
	}
	return convertPR(gh, role), nil
}

func convertPR(gh ghPR, role string) *protocol.PR {
	repo := gh.HeadRepository.NameWithOwner
	state, reason := determineState(
		gh.StatusCheckRollup.State,
		gh.ReviewDecision,
		gh.Mergeable,
		role,
	)

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

func determineState(ciState, reviewDecision, mergeable, role string) (string, string) {
	// CI failed - author needs to fix
	if ciState == "FAILURE" || ciState == "ERROR" {
		if role == protocol.PRRoleAuthor {
			return protocol.StateWaiting, protocol.PRReasonCIFailed
		}
		return protocol.StateWorking, "" // Reviewer waiting for author to fix
	}

	// Changes requested - author needs to address
	if reviewDecision == "CHANGES_REQUESTED" {
		if role == protocol.PRRoleAuthor {
			return protocol.StateWaiting, protocol.PRReasonChangesRequested
		}
		return protocol.StateWorking, "" // Reviewer waiting for author
	}

	// Review needed - reviewer needs to act
	if reviewDecision == "REVIEW_REQUIRED" || reviewDecision == "" {
		if role == protocol.PRRoleReviewer {
			return protocol.StateWaiting, protocol.PRReasonReviewNeeded
		}
		return protocol.StateWorking, "" // Author waiting for reviews
	}

	// Approved + CI passed - author can merge
	if reviewDecision == "APPROVED" && (ciState == "SUCCESS" || ciState == "") {
		if role == protocol.PRRoleAuthor {
			return protocol.StateWaiting, protocol.PRReasonReadyToMerge
		}
		return protocol.StateWorking, "" // Reviewer done, waiting for author
	}

	// CI pending or other states - waiting on external
	return protocol.StateWorking, ""
}
