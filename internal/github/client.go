package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
)

// Client is an HTTP client for the GitHub API
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a new GitHub API client.
// baseURL priority: parameter > GITHUB_API_URL env > https://api.github.com
// Token priority: GITHUB_TOKEN env > gh auth token command
func NewClient(baseURL string) (*Client, error) {
	if baseURL == "" {
		baseURL = os.Getenv("GITHUB_API_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		// Try gh auth token
		cmd := exec.Command("gh", "auth", "token")
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("no GITHUB_TOKEN and gh auth token failed: %w", err)
		}
		token = strings.TrimSpace(string(output))
	}

	if token == "" {
		return nil, fmt.Errorf("no GitHub token available")
	}

	// SAFETY: Refuse to use real GitHub API with test token
	// This prevents accidental real API calls in tests
	if token == "test-token" && baseURL == "https://api.github.com" {
		return nil, fmt.Errorf("refusing to use real GitHub API with test token - set GITHUB_API_URL to mock server")
	}

	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// IsAvailable returns true if the client has a valid token
func (c *Client) IsAvailable() bool {
	return c.token != ""
}

// doRequest performs an HTTP request with proper GitHub headers
func (c *Client) doRequest(method, path string, body interface{}) ([]byte, error) {
	url := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GitHub API error %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// searchResult represents GitHub search API response
type searchResult struct {
	TotalCount int          `json:"total_count"`
	Items      []searchItem `json:"items"`
}

type searchItem struct {
	Number        int    `json:"number"`
	Title         string `json:"title"`
	HTMLURL       string `json:"html_url"`
	Draft         bool   `json:"draft"`
	RepositoryURL string `json:"repository_url"`
	Comments      int    `json:"comments"` // Comment count for change detection
	PullRequest   *struct {
		URL string `json:"url"` // PR API URL to fetch head SHA
	} `json:"pull_request"`
}

// extractRepoFromURL extracts "owner/repo" from repository_url
// e.g., "https://api.github.com/repos/owner/repo" -> "owner/repo"
func extractRepoFromURL(repoURL string) string {
	re := regexp.MustCompile(`/repos/([^/]+/[^/]+)$`)
	matches := re.FindStringSubmatch(repoURL)
	if len(matches) == 2 {
		return matches[1]
	}
	return ""
}

// SearchAuthoredPRs searches for open PRs authored by the authenticated user
func (c *Client) SearchAuthoredPRs() ([]*protocol.PR, error) {
	query := url.QueryEscape("is:pr is:open author:@me")
	path := fmt.Sprintf("/search/issues?q=%s&per_page=50", query)

	body, err := c.doRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result searchResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var prs []*protocol.PR
	for _, item := range result.Items {
		// Skip drafts
		if item.Draft {
			continue
		}

		repo := extractRepoFromURL(item.RepositoryURL)
		prs = append(prs, &protocol.PR{
			ID:           fmt.Sprintf("%s#%d", repo, item.Number),
			Repo:         repo,
			Number:       item.Number,
			Title:        item.Title,
			URL:          item.HTMLURL,
			Role:         protocol.PRRoleAuthor,
			State:        protocol.StateWaiting,
			Reason:       "",
			LastUpdated:  time.Now(),
			LastPolled:   time.Now(),
			CommentCount: item.Comments,
		})
	}

	return prs, nil
}

// SearchReviewRequestedPRs searches for open PRs where authenticated user is requested reviewer
func (c *Client) SearchReviewRequestedPRs() ([]*protocol.PR, error) {
	query := url.QueryEscape("is:pr is:open review-requested:@me")
	path := fmt.Sprintf("/search/issues?q=%s&per_page=50", query)

	body, err := c.doRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result searchResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var prs []*protocol.PR
	for _, item := range result.Items {
		if item.Draft {
			continue
		}

		repo := extractRepoFromURL(item.RepositoryURL)
		prs = append(prs, &protocol.PR{
			ID:           fmt.Sprintf("%s#%d", repo, item.Number),
			Repo:         repo,
			Number:       item.Number,
			Title:        item.Title,
			URL:          item.HTMLURL,
			Role:         protocol.PRRoleReviewer,
			State:        protocol.StateWaiting,
			Reason:       protocol.PRReasonReviewNeeded,
			LastUpdated:  time.Now(),
			LastPolled:   time.Now(),
			CommentCount: item.Comments,
		})
	}

	return prs, nil
}

// SearchReviewedByMePRs searches for open PRs where authenticated user has submitted a review
// This catches PRs the user has approved (no longer in review-requested:@me)
func (c *Client) SearchReviewedByMePRs() ([]*protocol.PR, error) {
	query := url.QueryEscape("is:pr is:open reviewed-by:@me")
	path := fmt.Sprintf("/search/issues?q=%s&per_page=50", query)

	body, err := c.doRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result searchResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var prs []*protocol.PR
	for _, item := range result.Items {
		if item.Draft {
			continue
		}

		repo := extractRepoFromURL(item.RepositoryURL)
		prs = append(prs, &protocol.PR{
			ID:           fmt.Sprintf("%s#%d", repo, item.Number),
			Repo:         repo,
			Number:       item.Number,
			Title:        item.Title,
			URL:          item.HTMLURL,
			Role:         protocol.PRRoleReviewer,
			State:        protocol.StateWaiting,
			Reason:       protocol.PRReasonReviewNeeded,
			LastUpdated:  time.Now(),
			LastPolled:   time.Now(),
			CommentCount: item.Comments,
			ApprovedByMe: true, // Will be refined based on actual review state
		})
	}

	return prs, nil
}

// FetchAll fetches all PRs (authored + review requests + reviewed-by-me)
func (c *Client) FetchAll() ([]*protocol.PR, error) {
	prMap := make(map[string]*protocol.PR)

	// 1. Authored PRs
	authored, err := c.SearchAuthoredPRs()
	if err != nil {
		return nil, fmt.Errorf("fetch authored: %w", err)
	}
	for _, pr := range authored {
		prMap[pr.ID] = pr
	}

	// 2. Review-requested PRs (user needs to review)
	reviewRequested, err := c.SearchReviewRequestedPRs()
	if err != nil {
		return nil, fmt.Errorf("fetch review requests: %w", err)
	}
	reviewRequestedSet := make(map[string]bool)
	for _, pr := range reviewRequested {
		reviewRequestedSet[pr.ID] = true
		if existing, ok := prMap[pr.ID]; ok {
			// Already in map (authored), keep author role
			existing.CommentCount = pr.CommentCount
		} else {
			prMap[pr.ID] = pr
		}
	}

	// 3. Reviewed-by-me PRs (user has already reviewed, may have approved)
	reviewedByMe, err := c.SearchReviewedByMePRs()
	if err != nil {
		// Non-fatal: reviewed-by-me is enhancement, not critical
		reviewedByMe = nil
	}
	for _, pr := range reviewedByMe {
		if existing, ok := prMap[pr.ID]; ok {
			// Already in map - just update comment count
			existing.CommentCount = pr.CommentCount
			// If not in review-requested, user has completed their review
			if !reviewRequestedSet[pr.ID] && existing.Role == protocol.PRRoleReviewer {
				existing.ApprovedByMe = true
			}
		} else {
			// PR only in reviewed-by-me = user approved and is no longer requested
			pr.ApprovedByMe = true
			prMap[pr.ID] = pr
		}
	}

	// Convert map to slice
	var allPRs []*protocol.PR
	for _, pr := range prMap {
		allPRs = append(allPRs, pr)
	}

	return allPRs, nil
}

// PRDetails contains detailed PR status
type PRDetails struct {
	Mergeable      *bool
	MergeableState string
	CIStatus       string
	ReviewStatus   string
	HeadSHA        string // Current commit SHA for change detection
}

// FetchPRDetails fetches detailed status for a PR
func (c *Client) FetchPRDetails(repo string, number int) (*PRDetails, error) {
	// Fetch PR details
	prPath := fmt.Sprintf("/repos/%s/pulls/%d", repo, number)
	prBody, err := c.doRequest("GET", prPath, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch PR: %w", err)
	}

	var prData struct {
		Mergeable      *bool  `json:"mergeable"`
		MergeableState string `json:"mergeable_state"`
		Head           struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.Unmarshal(prBody, &prData); err != nil {
		return nil, fmt.Errorf("parse PR: %w", err)
	}

	details := &PRDetails{
		Mergeable:      prData.Mergeable,
		MergeableState: prData.MergeableState,
		HeadSHA:        prData.Head.SHA,
	}

	// Derive CI status from mergeable_state (no extra API call needed)
	// This is more accurate than check-runs because it accounts for:
	// - Required status checks from branch protection/rulesets
	// - Merge conflicts
	// - All blocking conditions GitHub enforces
	switch prData.MergeableState {
	case "clean":
		details.CIStatus = "success" // Ready to merge
	case "blocked":
		details.CIStatus = "pending" // Required checks not passed
	case "dirty":
		details.CIStatus = "failure" // Merge conflicts
	case "unstable":
		details.CIStatus = "pending" // CI failing but not blocking
	default:
		details.CIStatus = "none"
	}

	// Fetch review status
	reviewPath := fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number)
	reviewBody, err := c.doRequest("GET", reviewPath, nil)
	if err == nil {
		var reviews []struct {
			State string `json:"state"`
		}
		if json.Unmarshal(reviewBody, &reviews) == nil {
			details.ReviewStatus = computeReviewStatus(reviews)
		}
	}

	return details, nil
}


func computeReviewStatus(reviews []struct{ State string `json:"state"` }) string {
	if len(reviews) == 0 {
		return "none"
	}

	hasApproved := false
	hasChangesRequested := false
	for _, review := range reviews {
		if review.State == "COMMENTED" {
			continue
		}
		if review.State == "APPROVED" {
			hasApproved = true
		}
		if review.State == "CHANGES_REQUESTED" {
			hasChangesRequested = true
		}
	}

	if hasChangesRequested {
		return "changes_requested"
	}
	if hasApproved {
		return "approved"
	}
	return "pending"
}

// ApprovePR approves a pull request by submitting an approval review
func (c *Client) ApprovePR(repo string, number int) error {
	path := fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number)
	body := map[string]string{
		"event": "APPROVE",
	}

	_, err := c.doRequest("POST", path, body)
	if err != nil {
		return fmt.Errorf("approve PR: %w", err)
	}

	return nil
}

// MergePR merges a pull request using the specified merge method
func (c *Client) MergePR(repo string, number int, method string) error {
	// Validate merge method
	validMethods := map[string]bool{
		"squash": true,
		"merge":  true,
		"rebase": true,
	}
	if !validMethods[method] {
		return fmt.Errorf("invalid merge method %q, must be squash, merge, or rebase", method)
	}

	path := fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, number)
	body := map[string]string{
		"merge_method": method,
	}

	_, err := c.doRequest("PUT", path, body)
	if err != nil {
		return fmt.Errorf("merge PR: %w", err)
	}

	return nil
}
