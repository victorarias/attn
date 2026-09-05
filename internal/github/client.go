package github

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"golang.org/x/time/rate"
)

func GitHTTPSAuthorizationHeader(token string) string {
	if token == "" {
		return ""
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
}

func (c *Client) GitHTTPSAuthorizationHeader() string {
	if c == nil {
		return ""
	}
	return GitHTTPSAuthorizationHeader(c.token)
}

var ErrRateLimited = errors.New("GitHub rate limit exceeded")

var ErrSelfRateLimited = errors.New("self-imposed rate limit exceeded")

var ErrNoToken = errors.New("no GitHub token available")

type RateLimitInfo struct {
	Resource  string
	Remaining int
	ResetAt   time.Time
}

type Client struct {
	host       string
	baseURL    string
	token      string
	httpClient *http.Client

	rateLimitsMu sync.RWMutex
	rateLimits   map[string]*RateLimitInfo

	selfLimiter *rate.Limiter
}

func NewClient(baseURL, token string) (*Client, error) {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	if token == "" {
		return nil, ErrNoToken
	}

	host := hostFromAPIURL(baseURL)
	return newClientWithToken(host, baseURL, token)
}

func newClientWithToken(host, baseURL, token string) (*Client, error) {
	if token == "" {
		return nil, ErrNoToken
	}
	// SAFETY: never let a test token reach the real GitHub API.
	if token == "test-token" && baseURL == "https://api.github.com" {
		return nil, fmt.Errorf("refusing to use real GitHub API with test token - use a mock server URL")
	}

	return &Client{
		host:    host,
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		rateLimits: make(map[string]*RateLimitInfo),
		// 1 request/second average, burst 60: absorbs an app launch with many PRs
		selfLimiter: rate.NewLimiter(rate.Limit(1), 60),
	}, nil
}

func hostFromAPIURL(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	if baseURL == "https://api.github.com" || baseURL == "http://api.github.com" {
		return "github.com"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	host := parsed.Hostname()
	if host == "api.github.com" {
		return "github.com"
	}
	return host
}

func (c *Client) IsAvailable() bool {
	return c.token != ""
}

func (c *Client) doRequest(method, path string, body interface{}) ([]byte, error) {
	if !c.selfLimiter.Allow() {
		return nil, ErrSelfRateLimited
	}

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

	c.parseRateLimitHeaders(resp)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		if remaining := resp.Header.Get("X-RateLimit-Remaining"); remaining == "0" {
			return nil, fmt.Errorf("%w: %s", ErrRateLimited, string(respBody))
		}
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GitHub API error %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func (c *Client) parseRateLimitHeaders(resp *http.Response) {
	resource := resp.Header.Get("X-RateLimit-Resource")
	if resource == "" {
		resource = "core"
	}

	remainingStr := resp.Header.Get("X-RateLimit-Remaining")
	resetStr := resp.Header.Get("X-RateLimit-Reset")

	if remainingStr == "" || resetStr == "" {
		return
	}

	remaining, err := strconv.Atoi(remainingStr)
	if err != nil {
		return
	}

	resetUnix, err := strconv.ParseInt(resetStr, 10, 64)
	if err != nil {
		return
	}

	c.rateLimitsMu.Lock()
	c.rateLimits[resource] = &RateLimitInfo{
		Resource:  resource,
		Remaining: remaining,
		ResetAt:   time.Unix(resetUnix, 0),
	}
	c.rateLimitsMu.Unlock()
}

func (c *Client) IsRateLimited(resource string) (bool, time.Time) {
	c.rateLimitsMu.RLock()
	defer c.rateLimitsMu.RUnlock()

	info, ok := c.rateLimits[resource]
	if !ok {
		return false, time.Time{}
	}

	if info.Remaining < 5 && time.Now().Before(info.ResetAt) {
		return true, info.ResetAt
	}

	return false, time.Time{}
}

func (c *Client) GetRateLimit(resource string) *RateLimitInfo {
	c.rateLimitsMu.RLock()
	defer c.rateLimitsMu.RUnlock()

	if info, ok := c.rateLimits[resource]; ok {
		return &RateLimitInfo{
			Resource:  info.Resource,
			Remaining: info.Remaining,
			ResetAt:   info.ResetAt,
		}
	}
	return nil
}

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
	Comments      int    `json:"comments"`
	User          *struct {
		Login string `json:"login"`
	} `json:"user"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
}

func extractRepoFromURL(repoURL string) string {
	re := regexp.MustCompile(`/repos/([^/]+/[^/]+)$`)
	matches := re.FindStringSubmatch(repoURL)
	if len(matches) == 2 {
		return matches[1]
	}
	return ""
}

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
		if item.Draft {
			continue
		}

		repo := extractRepoFromURL(item.RepositoryURL)
		author := ""
		if item.User != nil {
			author = item.User.Login
		}
		prs = append(prs, &protocol.PR{
			ID:           protocol.FormatPRID(c.host, repo, item.Number),
			Host:         c.host,
			Repo:         repo,
			Number:       item.Number,
			Title:        item.Title,
			URL:          item.HTMLURL,
			Author:       author,
			Role:         protocol.PRRoleAuthor,
			State:        protocol.PRStateWaiting,
			Reason:       "",
			LastUpdated:  string(protocol.TimestampNow()),
			LastPolled:   string(protocol.TimestampNow()),
			CommentCount: protocol.Ptr(item.Comments),
		})
	}

	return prs, nil
}

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
		author := ""
		if item.User != nil {
			author = item.User.Login
		}
		prs = append(prs, &protocol.PR{
			ID:           protocol.FormatPRID(c.host, repo, item.Number),
			Host:         c.host,
			Repo:         repo,
			Number:       item.Number,
			Title:        item.Title,
			URL:          item.HTMLURL,
			Author:       author,
			Role:         protocol.PRRoleReviewer,
			State:        protocol.PRStateWaiting,
			Reason:       protocol.PRReasonReviewNeeded,
			LastUpdated:  string(protocol.TimestampNow()),
			LastPolled:   string(protocol.TimestampNow()),
			CommentCount: protocol.Ptr(item.Comments),
		})
	}

	return prs, nil
}

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
		author := ""
		if item.User != nil {
			author = item.User.Login
		}
		prs = append(prs, &protocol.PR{
			ID:           protocol.FormatPRID(c.host, repo, item.Number),
			Host:         c.host,
			Repo:         repo,
			Number:       item.Number,
			Title:        item.Title,
			URL:          item.HTMLURL,
			Author:       author,
			Role:         protocol.PRRoleReviewer,
			State:        protocol.PRStateWaiting,
			Reason:       protocol.PRReasonReviewNeeded,
			LastUpdated:  string(protocol.TimestampNow()),
			LastPolled:   string(protocol.TimestampNow()),
			CommentCount: protocol.Ptr(item.Comments),
			ApprovedByMe: true,
		})
	}

	return prs, nil
}

func (c *Client) FetchAll() ([]*protocol.PR, error) {
	prMap := make(map[string]*protocol.PR)

	authored, err := c.SearchAuthoredPRs()
	if err != nil {
		return nil, fmt.Errorf("fetch authored: %w", err)
	}
	for _, pr := range authored {
		prMap[pr.ID] = pr
	}

	reviewRequested, err := c.SearchReviewRequestedPRs()
	if err != nil {
		return nil, fmt.Errorf("fetch review requests: %w", err)
	}
	reviewRequestedSet := make(map[string]bool)
	for _, pr := range reviewRequested {
		reviewRequestedSet[pr.ID] = true
		if existing, ok := prMap[pr.ID]; ok {
			existing.CommentCount = pr.CommentCount
		} else {
			prMap[pr.ID] = pr
		}
	}

	reviewedByMe, err := c.SearchReviewedByMePRs()
	if err != nil {
		// Non-fatal: reviewed-by-me is enhancement, not critical
		reviewedByMe = nil
	}
	for _, pr := range reviewedByMe {
		if existing, ok := prMap[pr.ID]; ok {
			existing.CommentCount = pr.CommentCount
			if !reviewRequestedSet[pr.ID] && existing.Role == protocol.PRRoleReviewer {
				existing.ApprovedByMe = true
			}
		} else {
			pr.ApprovedByMe = true
			prMap[pr.ID] = pr
		}
	}

	var allPRs []*protocol.PR
	for _, pr := range prMap {
		allPRs = append(allPRs, pr)
	}

	return allPRs, nil
}

type PRDetails struct {
	Mergeable      *bool
	MergeableState string
	CIStatus       string
	ReviewStatus   string
	HeadSHA        string
	HeadBranch     string
}

type PullRequestSnapshot struct {
	Number         int
	URL            string
	Title          string
	Body           string
	Author         string
	Draft          bool
	State          string
	Merged         bool
	Mergeable      *bool
	MergeableState string
	HeadSHA        string
	HeadRef        string
	HeadRepository string
	BaseSHA        string
	BaseRef        string
	BaseRepository string
}

func (c *Client) FetchPullRequestSnapshot(repo string, number int) (*PullRequestSnapshot, error) {
	body, err := c.doRequest("GET", fmt.Sprintf("/repos/%s/pulls/%d", repo, number), nil)
	if err != nil {
		return nil, fmt.Errorf("fetch pull request snapshot: %w", err)
	}
	var response struct {
		Number         int    `json:"number"`
		HTMLURL        string `json:"html_url"`
		Title          string `json:"title"`
		Body           string `json:"body"`
		Draft          bool   `json:"draft"`
		State          string `json:"state"`
		Merged         bool   `json:"merged"`
		Mergeable      *bool  `json:"mergeable"`
		MergeableState string `json:"mergeable_state"`
		User           struct {
			Login string `json:"login"`
		} `json:"user"`
		Head struct {
			SHA  string `json:"sha"`
			Ref  string `json:"ref"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			SHA  string `json:"sha"`
			Ref  string `json:"ref"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"base"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse pull request snapshot: %w", err)
	}
	return &PullRequestSnapshot{
		Number: response.Number, URL: response.HTMLURL, Title: response.Title, Body: response.Body,
		Author: response.User.Login, Draft: response.Draft, State: response.State, Merged: response.Merged,
		Mergeable: response.Mergeable, MergeableState: response.MergeableState,
		HeadSHA: response.Head.SHA, HeadRef: response.Head.Ref, HeadRepository: response.Head.Repo.FullName,
		BaseSHA: response.Base.SHA, BaseRef: response.Base.Ref, BaseRepository: response.Base.Repo.FullName,
	}, nil
}

// Deliberately not built on FetchPRDetails, which makes extra review calls.
func (c *Client) FetchPRState(repo string, number int) (state string, merged bool, title string, err error) {
	body, err := c.doRequest("GET", fmt.Sprintf("/repos/%s/pulls/%d", repo, number), nil)
	if err != nil {
		return "", false, "", fmt.Errorf("fetch PR state: %w", err)
	}
	var prData struct {
		State  string `json:"state"`
		Merged bool   `json:"merged"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(body, &prData); err != nil {
		return "", false, "", fmt.Errorf("parse PR state: %w", err)
	}
	return prData.State, prData.Merged, prData.Title, nil
}

func (c *Client) RepoVisibility(repo string) (string, error) {
	body, err := c.doRequest("GET", "/repos/"+repo, nil)
	if err != nil {
		return "", fmt.Errorf("fetch repo visibility: %w", err)
	}
	var repoData struct {
		Private bool `json:"private"`
	}
	if err := json.Unmarshal(body, &repoData); err != nil {
		return "", fmt.Errorf("parse repo visibility: %w", err)
	}
	if repoData.Private {
		return "private", nil
	}
	return "public", nil
}

func (c *Client) FetchPRDetails(repo string, number int) (*PRDetails, error) {
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
			Ref string `json:"ref"`
		} `json:"head"`
	}
	if err := json.Unmarshal(prBody, &prData); err != nil {
		return nil, fmt.Errorf("parse PR: %w", err)
	}

	details := &PRDetails{
		Mergeable:      prData.Mergeable,
		MergeableState: prData.MergeableState,
		HeadSHA:        prData.Head.SHA,
		HeadBranch:     prData.Head.Ref,
	}

	details.CIStatus = CIStatusFromMergeableState(prData.MergeableState)
	// Unreadable reviews leave the field empty; mergeability is still worth reporting.
	details.ReviewStatus, _ = c.FetchPullRequestReviewStatus(repo, number)

	return details, nil
}

func CIStatusFromMergeableState(mergeableState string) string {
	switch mergeableState {
	case "clean":
		return "success"
	case "blocked", "unstable":
		return "pending"
	case "dirty":
		return "failure"
	default:
		return "none"
	}
}

func (c *Client) FetchPullRequestReviewStatus(repo string, number int) (string, error) {
	body, err := c.doRequest("GET", fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number), nil)
	if err != nil {
		return "", fmt.Errorf("fetch PR reviews: %w", err)
	}
	var reviews []struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &reviews); err != nil {
		return "", fmt.Errorf("parse PR reviews: %w", err)
	}
	return computeReviewStatus(reviews), nil
}

func computeReviewStatus(reviews []struct {
	State string `json:"state"`
}) string {
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

func (c *Client) MergePR(repo string, number int, method string) error {
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

func (c *Client) Host() string {
	return c.host
}

// The head branch is the sweep's strongest merged signal; the base branch is what
// resolves the repository's integration branch.
type MergedPullRequest struct {
	Number   int
	URL      string
	HeadRef  string
	HeadSHA  string
	BaseRef  string
	MergedAt string
}

// 300 merged pull requests, past the 152 and 200 the measured repositories carry.
// Receipts in docs/worktree-sweep.md.
const mergedPullRequestPageLimit = 3

func (c *Client) ListMergedPullRequests(repo string) ([]MergedPullRequest, error) {
	var merged []MergedPullRequest
	for page := 1; page <= mergedPullRequestPageLimit; page++ {
		path := fmt.Sprintf("/repos/%s/pulls?state=closed&sort=updated&direction=desc&per_page=100&page=%d", repo, page)
		body, err := c.doRequest("GET", path, nil)
		if err != nil {
			return nil, fmt.Errorf("list merged pull requests: %w", err)
		}
		var response []struct {
			Number   int    `json:"number"`
			HTMLURL  string `json:"html_url"`
			MergedAt string `json:"merged_at"`
			Head     struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("parse merged pull requests: %w", err)
		}
		for _, pr := range response {
			if pr.MergedAt == "" {
				continue
			}
			merged = append(merged, MergedPullRequest{
				Number: pr.Number, URL: pr.HTMLURL, HeadRef: pr.Head.Ref, HeadSHA: pr.Head.SHA,
				BaseRef: pr.Base.Ref, MergedAt: pr.MergedAt,
			})
		}
		if len(response) < 100 {
			break
		}
	}
	return merged, nil
}
