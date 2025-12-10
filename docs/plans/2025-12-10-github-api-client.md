# GitHub Direct API Client Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace ALL `gh` CLI calls with direct HTTP API calls to enable mock server testing.

**Architecture:** Create a new `Client` struct in `internal/github/` with configurable `baseURL`. Production uses `https://api.github.com`, tests use a local mock server. Token resolution: `GITHUB_TOKEN` env → `gh auth token` fallback. The daemon uses only the new `Client`, eliminating the `Fetcher` entirely.

**Tech Stack:** Go `net/http`, Go `httptest` for mock server, Playwright for E2E tests

**API Methods to Implement:**
| Current `gh` CLI Call | GitHub REST API Endpoint |
|----------------------|--------------------------|
| `gh search prs --author @me` | `GET /search/issues?q=is:pr+is:open+author:@me` |
| `gh search prs --review-requested @me` | `GET /search/issues?q=is:pr+is:open+review-requested:@me` |
| `gh api repos/{repo}/pulls/{number}` | `GET /repos/{owner}/{repo}/pulls/{number}` |
| `gh api repos/{repo}/commits/{sha}/check-runs` | `GET /repos/{owner}/{repo}/commits/{sha}/check-runs` |
| `gh api repos/{repo}/pulls/{number}/reviews` | `GET /repos/{owner}/{repo}/pulls/{number}/reviews` |
| `gh pr review --approve` | `POST /repos/{owner}/{repo}/pulls/{number}/reviews` with `{"event":"APPROVE"}` |
| `gh pr merge --squash` | `PUT /repos/{owner}/{repo}/pulls/{number}/merge` with `{"merge_method":"squash"}` |

---

## Task 1: Create GitHub API Client with Token Resolution

**Files:**
- Create: `internal/github/client.go`
- Create: `internal/github/client_test.go`

**Step 1: Write the failing test for token resolution**

```go
// internal/github/client_test.go
package github

import (
	"os"
	"testing"
)

func TestNewClient_UsesEnvToken(t *testing.T) {
	os.Setenv("GITHUB_TOKEN", "test-token-from-env")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if client.token != "test-token-from-env" {
		t.Errorf("token = %q, want %q", client.token, "test-token-from-env")
	}
}

func TestNewClient_DefaultsToGitHubAPI(t *testing.T) {
	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if client.baseURL != "https://api.github.com" {
		t.Errorf("baseURL = %q, want %q", client.baseURL, "https://api.github.com")
	}
}

func TestNewClient_CustomBaseURL(t *testing.T) {
	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, err := NewClient("http://localhost:9999")
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if client.baseURL != "http://localhost:9999" {
		t.Errorf("baseURL = %q, want %q", client.baseURL, "http://localhost:9999")
	}
}

func TestNewClient_UsesEnvBaseURL(t *testing.T) {
	os.Setenv("GITHUB_TOKEN", "test-token")
	os.Setenv("GITHUB_API_URL", "http://mock:8080")
	defer os.Unsetenv("GITHUB_TOKEN")
	defer os.Unsetenv("GITHUB_API_URL")

	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if client.baseURL != "http://mock:8080" {
		t.Errorf("baseURL = %q, want %q", client.baseURL, "http://mock:8080")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/github -run TestNewClient -v`
Expected: FAIL with "undefined: NewClient"

**Step 3: Write minimal implementation**

```go
// internal/github/client.go
package github

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
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
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/github -run TestNewClient -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/github/client.go internal/github/client_test.go
git commit -m "feat(github): add API client with token resolution"
```

---

## Task 2: Add HTTP Helper Methods

**Files:**
- Modify: `internal/github/client.go`
- Modify: `internal/github/client_test.go`

**Step 1: Write failing test for helper method**

```go
// Add to internal/github/client_test.go
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

func TestClient_doRequest_SetsHeaders(t *testing.T) {
	var capturedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	os.Setenv("GITHUB_TOKEN", "test-token-123")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, _ := NewClient(server.URL)
	client.doRequest("GET", "/test", nil)

	if capturedHeaders.Get("Authorization") != "Bearer test-token-123" {
		t.Errorf("Authorization header = %q, want Bearer test-token-123", capturedHeaders.Get("Authorization"))
	}
	if capturedHeaders.Get("Accept") != "application/vnd.github+json" {
		t.Errorf("Accept header = %q, want application/vnd.github+json", capturedHeaders.Get("Accept"))
	}
	if capturedHeaders.Get("X-GitHub-Api-Version") != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version header missing or wrong")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/github -run TestClient_doRequest -v`
Expected: FAIL with "undefined: client.doRequest"

**Step 3: Write implementation**

```go
// Add to internal/github/client.go
import (
	"bytes"
	"encoding/json"
	"io"
)

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
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/github -run TestClient_doRequest -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/github/client.go internal/github/client_test.go
git commit -m "feat(github): add HTTP helper with proper headers"
```

---

## Task 3: Add SearchAuthoredPRs Method

**Files:**
- Modify: `internal/github/client.go`
- Modify: `internal/github/client_test.go`

**Step 1: Write failing test with mock server**

```go
// Add to internal/github/client_test.go
func TestClient_SearchAuthoredPRs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify correct endpoint
		if r.URL.Path != "/search/issues" {
			t.Errorf("Path = %q, want /search/issues", r.URL.Path)
		}

		q := r.URL.Query().Get("q")
		if !strings.Contains(q, "is:pr") || !strings.Contains(q, "is:open") || !strings.Contains(q, "author:@me") {
			t.Errorf("Query = %q, missing required qualifiers", q)
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"total_count": 2,
			"items": []map[string]interface{}{
				{
					"number":         123,
					"title":          "Test PR 1",
					"html_url":       "https://github.com/owner/repo/pull/123",
					"draft":          false,
					"repository_url": "https://api.github.com/repos/owner/repo",
				},
				{
					"number":         456,
					"title":          "Draft PR",
					"html_url":       "https://github.com/owner/repo/pull/456",
					"draft":          true,
					"repository_url": "https://api.github.com/repos/owner/repo",
				},
			},
		})
	}))
	defer server.Close()

	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, _ := NewClient(server.URL)
	prs, err := client.SearchAuthoredPRs()
	if err != nil {
		t.Fatalf("SearchAuthoredPRs error: %v", err)
	}

	// Should filter out draft PRs
	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want 1 (draft should be filtered)", len(prs))
	}

	if prs[0].Number != 123 {
		t.Errorf("PR number = %d, want 123", prs[0].Number)
	}
	if prs[0].Repo != "owner/repo" {
		t.Errorf("PR repo = %q, want owner/repo", prs[0].Repo)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/github -run TestClient_SearchAuthoredPRs -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// Add to internal/github/client.go
import (
	"net/url"
	"regexp"

	"github.com/victorarias/claude-manager/internal/protocol"
)

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
			ID:          fmt.Sprintf("%s#%d", repo, item.Number),
			Repo:        repo,
			Number:      item.Number,
			Title:       item.Title,
			URL:         item.HTMLURL,
			Role:        protocol.PRRoleAuthor,
			State:       protocol.StateWaiting,
			Reason:      "",
			LastUpdated: time.Now(),
			LastPolled:  time.Now(),
		})
	}

	return prs, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/github -run TestClient_SearchAuthoredPRs -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/github/client.go internal/github/client_test.go
git commit -m "feat(github): add SearchAuthoredPRs via REST API"
```

---

## Task 4: Add SearchReviewRequestedPRs Method

**Files:**
- Modify: `internal/github/client.go`
- Modify: `internal/github/client_test.go`

**Step 1: Write failing test**

```go
// Add to internal/github/client_test.go
func TestClient_SearchReviewRequestedPRs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if !strings.Contains(q, "review-requested:@me") {
			t.Errorf("Query = %q, missing review-requested:@me", q)
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"total_count": 1,
			"items": []map[string]interface{}{
				{
					"number":         789,
					"title":          "Needs Review",
					"html_url":       "https://github.com/other/repo/pull/789",
					"draft":          false,
					"repository_url": "https://api.github.com/repos/other/repo",
				},
			},
		})
	}))
	defer server.Close()

	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, _ := NewClient(server.URL)
	prs, err := client.SearchReviewRequestedPRs()
	if err != nil {
		t.Fatalf("SearchReviewRequestedPRs error: %v", err)
	}

	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want 1", len(prs))
	}
	if prs[0].Role != protocol.PRRoleReviewer {
		t.Errorf("Role = %q, want %q", prs[0].Role, protocol.PRRoleReviewer)
	}
	if prs[0].Reason != protocol.PRReasonReviewNeeded {
		t.Errorf("Reason = %q, want %q", prs[0].Reason, protocol.PRReasonReviewNeeded)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/github -run TestClient_SearchReviewRequestedPRs -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// Add to internal/github/client.go

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
			ID:          fmt.Sprintf("%s#%d", repo, item.Number),
			Repo:        repo,
			Number:      item.Number,
			Title:       item.Title,
			URL:         item.HTMLURL,
			Role:        protocol.PRRoleReviewer,
			State:       protocol.StateWaiting,
			Reason:      protocol.PRReasonReviewNeeded,
			LastUpdated: time.Now(),
			LastPolled:  time.Now(),
		})
	}

	return prs, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/github -run TestClient_SearchReviewRequestedPRs -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/github/client.go internal/github/client_test.go
git commit -m "feat(github): add SearchReviewRequestedPRs via REST API"
```

---

## Task 5: Add FetchAll Method (Combines Both Searches)

**Files:**
- Modify: `internal/github/client.go`
- Modify: `internal/github/client_test.go`

**Step 1: Write failing test**

```go
// Add to internal/github/client_test.go
func TestClient_FetchAll(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		q := r.URL.Query().Get("q")

		var items []map[string]interface{}
		if strings.Contains(q, "author:@me") {
			items = []map[string]interface{}{
				{"number": 1, "title": "My PR", "html_url": "https://github.com/a/b/pull/1", "draft": false, "repository_url": "https://api.github.com/repos/a/b"},
			}
		} else if strings.Contains(q, "review-requested:@me") {
			items = []map[string]interface{}{
				{"number": 2, "title": "Review This", "html_url": "https://github.com/c/d/pull/2", "draft": false, "repository_url": "https://api.github.com/repos/c/d"},
			}
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"total_count": len(items), "items": items})
	}))
	defer server.Close()

	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, _ := NewClient(server.URL)
	prs, err := client.FetchAll()
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}

	if len(prs) != 2 {
		t.Fatalf("got %d PRs, want 2", len(prs))
	}
	if callCount != 2 {
		t.Errorf("API called %d times, want 2", callCount)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/github -run TestClient_FetchAll -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// Add to internal/github/client.go

// FetchAll fetches all PRs (authored + review requests)
func (c *Client) FetchAll() ([]*protocol.PR, error) {
	var allPRs []*protocol.PR

	authored, err := c.SearchAuthoredPRs()
	if err != nil {
		return nil, fmt.Errorf("fetch authored: %w", err)
	}
	allPRs = append(allPRs, authored...)

	reviews, err := c.SearchReviewRequestedPRs()
	if err != nil {
		return nil, fmt.Errorf("fetch review requests: %w", err)
	}
	allPRs = append(allPRs, reviews...)

	return allPRs, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/github -run TestClient_FetchAll -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/github/client.go internal/github/client_test.go
git commit -m "feat(github): add FetchAll combining both searches"
```

---

## Task 6: Add FetchPRDetails Method

**Files:**
- Modify: `internal/github/client.go`
- Modify: `internal/github/client_test.go`

**Step 1: Write failing test**

```go
// Add to internal/github/client_test.go
func TestClient_FetchPRDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/42"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"mergeable":       true,
				"mergeable_state": "clean",
				"head":            map[string]string{"sha": "abc123"},
			})
		case strings.Contains(r.URL.Path, "/check-runs"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"check_runs": []map[string]interface{}{
					{"conclusion": "success"},
					{"conclusion": "success"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/reviews"):
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{"state": "APPROVED"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, _ := NewClient(server.URL)
	details, err := client.FetchPRDetails("owner/repo", 42)
	if err != nil {
		t.Fatalf("FetchPRDetails error: %v", err)
	}

	if details.Mergeable == nil || *details.Mergeable != true {
		t.Error("Mergeable should be true")
	}
	if details.MergeableState != "clean" {
		t.Errorf("MergeableState = %q, want clean", details.MergeableState)
	}
	if details.CIStatus != "success" {
		t.Errorf("CIStatus = %q, want success", details.CIStatus)
	}
	if details.ReviewStatus != "approved" {
		t.Errorf("ReviewStatus = %q, want approved", details.ReviewStatus)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/github -run TestClient_FetchPRDetails -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// Add to internal/github/client.go

// PRDetails contains detailed PR status
type PRDetails struct {
	Mergeable      *bool
	MergeableState string
	CIStatus       string
	ReviewStatus   string
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
	}

	// Fetch CI status
	if prData.Head.SHA != "" {
		ciPath := fmt.Sprintf("/repos/%s/commits/%s/check-runs", repo, prData.Head.SHA)
		ciBody, err := c.doRequest("GET", ciPath, nil)
		if err == nil {
			var ciData struct {
				CheckRuns []struct {
					Conclusion *string `json:"conclusion"`
				} `json:"check_runs"`
			}
			if json.Unmarshal(ciBody, &ciData) == nil {
				details.CIStatus = computeCIStatus(ciData.CheckRuns)
			}
		}
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

func computeCIStatus(checkRuns []struct{ Conclusion *string `json:"conclusion"` }) string {
	if len(checkRuns) == 0 {
		return "none"
	}

	allSuccess := true
	hasPending := false
	for _, run := range checkRuns {
		if run.Conclusion == nil {
			hasPending = true
			allSuccess = false
		} else if *run.Conclusion != "success" {
			allSuccess = false
		}
	}

	if allSuccess {
		return "success"
	}
	if hasPending {
		return "pending"
	}
	return "failure"
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
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/github -run TestClient_FetchPRDetails -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/github/client.go internal/github/client_test.go
git commit -m "feat(github): add FetchPRDetails via REST API"
```

---

## Task 7: Add ApprovePR Method

**Files:**
- Modify: `internal/github/client.go`
- Modify: `internal/github/client_test.go`

**Step 1: Write failing test**

```go
// Add to internal/github/client_test.go
func TestClient_ApprovePR(t *testing.T) {
	var captured struct {
		Method string
		Path   string
		Body   map[string]string
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Method = r.Method
		captured.Path = r.URL.Path
		json.NewDecoder(r.Body).Decode(&captured.Body)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"id": 1, "state": "APPROVED"})
	}))
	defer server.Close()

	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, _ := NewClient(server.URL)
	err := client.ApprovePR("owner/repo", 42)
	if err != nil {
		t.Fatalf("ApprovePR error: %v", err)
	}

	if captured.Method != "POST" {
		t.Errorf("Method = %q, want POST", captured.Method)
	}
	if captured.Path != "/repos/owner/repo/pulls/42/reviews" {
		t.Errorf("Path = %q, want /repos/owner/repo/pulls/42/reviews", captured.Path)
	}
	if captured.Body["event"] != "APPROVE" {
		t.Errorf("Body event = %q, want APPROVE", captured.Body["event"])
	}
}

func TestClient_ApprovePR_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"message": "Must have admin rights"})
	}))
	defer server.Close()

	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, _ := NewClient(server.URL)
	err := client.ApprovePR("owner/repo", 42)

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("Error should contain 403: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/github -run TestClient_ApprovePR -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// Add to internal/github/client.go

// ApprovePR approves a pull request
func (c *Client) ApprovePR(repo string, number int) error {
	path := fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number)
	body := map[string]string{"event": "APPROVE"}

	_, err := c.doRequest("POST", path, body)
	return err
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/github -run TestClient_ApprovePR -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/github/client.go internal/github/client_test.go
git commit -m "feat(github): add ApprovePR via REST API"
```

---

## Task 8: Add MergePR Method

**Files:**
- Modify: `internal/github/client.go`
- Modify: `internal/github/client_test.go`

**Step 1: Write failing test**

```go
// Add to internal/github/client_test.go
func TestClient_MergePR(t *testing.T) {
	var captured struct {
		Method string
		Path   string
		Body   map[string]string
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Method = r.Method
		captured.Path = r.URL.Path
		json.NewDecoder(r.Body).Decode(&captured.Body)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"merged": true, "sha": "abc123"})
	}))
	defer server.Close()

	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, _ := NewClient(server.URL)
	err := client.MergePR("owner/repo", 42, "squash")
	if err != nil {
		t.Fatalf("MergePR error: %v", err)
	}

	if captured.Method != "PUT" {
		t.Errorf("Method = %q, want PUT", captured.Method)
	}
	if captured.Path != "/repos/owner/repo/pulls/42/merge" {
		t.Errorf("Path = %q, want /repos/owner/repo/pulls/42/merge", captured.Path)
	}
	if captured.Body["merge_method"] != "squash" {
		t.Errorf("merge_method = %q, want squash", captured.Body["merge_method"])
	}
}

func TestClient_MergePR_InvalidMethod(t *testing.T) {
	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, _ := NewClient("http://unused")
	err := client.MergePR("owner/repo", 42, "invalid")

	if err == nil {
		t.Fatal("Expected error for invalid method")
	}
	if !strings.Contains(err.Error(), "invalid merge method") {
		t.Errorf("Error should mention invalid method: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/github -run TestClient_MergePR -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// Add to internal/github/client.go

// MergePR merges a pull request
func (c *Client) MergePR(repo string, number int, method string) error {
	validMethods := map[string]bool{"squash": true, "merge": true, "rebase": true}
	if !validMethods[method] {
		return fmt.Errorf("invalid merge method: %s (must be squash, merge, or rebase)", method)
	}

	path := fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, number)
	body := map[string]string{"merge_method": method}

	_, err := c.doRequest("PUT", path, body)
	return err
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/github -run TestClient_MergePR -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/github/client.go internal/github/client_test.go
git commit -m "feat(github): add MergePR via REST API"
```

---

## Task 9: Create GitHubClient Interface

**Files:**
- Create: `internal/github/interface.go`

**Step 1: Write the interface**

```go
// internal/github/interface.go
package github

import "github.com/victorarias/claude-manager/internal/protocol"

// GitHubClient defines the interface for all GitHub operations
type GitHubClient interface {
	IsAvailable() bool
	FetchAll() ([]*protocol.PR, error)
	FetchPRDetails(repo string, number int) (*PRDetails, error)
	ApprovePR(repo string, number int) error
	MergePR(repo string, number int, method string) error
}

// Ensure Client implements the interface
var _ GitHubClient = (*Client)(nil)
```

**Step 2: Run build**

Run: `go build ./internal/github`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/github/interface.go
git commit -m "feat(github): add GitHubClient interface"
```

---

## Task 10: Update Daemon to Use New Client

**Files:**
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/daemon/websocket.go`

**Step 1: Update daemon to use interface**

```go
// internal/daemon/daemon.go

// Change imports to use github package
import "github.com/victorarias/claude-manager/internal/github"

// Update Daemon struct
type Daemon struct {
	socketPath string
	store      *store.Store
	listener   net.Listener
	httpServer *http.Server
	wsHub      *wsHub
	done       chan struct{}
	logger     *logging.Logger
	ghClient   github.GitHubClient // Changed from ghFetcher *github.Fetcher
}

// Update New()
func New(socketPath string) *Daemon {
	logger, _ := logging.New(logging.DefaultLogPath())

	var ghClient github.GitHubClient
	client, err := github.NewClient("")
	if err != nil {
		logger.Infof("GitHub client not available: %v", err)
	} else {
		ghClient = client
	}

	return &Daemon{
		socketPath: socketPath,
		store:      store.NewWithPersistence(store.DefaultStatePath()),
		wsHub:      newWSHub(),
		done:       make(chan struct{}),
		logger:     logger,
		ghClient:   ghClient,
	}
}

// Update NewForTesting
func NewForTesting(socketPath string) *Daemon {
	return &Daemon{
		socketPath: socketPath,
		store:      store.New(),
		wsHub:      newWSHub(),
		done:       make(chan struct{}),
		logger:     nil,
		ghClient:   nil,
	}
}

// Add NewWithGitHubClient for testing
func NewWithGitHubClient(socketPath string, ghClient github.GitHubClient) *Daemon {
	return &Daemon{
		socketPath: socketPath,
		store:      store.New(),
		wsHub:      newWSHub(),
		done:       make(chan struct{}),
		logger:     nil,
		ghClient:   ghClient,
	}
}
```

**Step 2: Update all ghFetcher references**

Replace all `d.ghFetcher` with `d.ghClient` in:
- `daemon.go`: `handleFetchPRDetails`, `pollPRs`, `doPRPoll`, `RefreshPRs`
- `websocket.go`: `handleClientMessage` (ApprovePR, MergePR)

Example changes:
```go
// In pollPRs:
if d.ghClient == nil || !d.ghClient.IsAvailable() {
	d.log("GitHub client not available, PR polling disabled")
	return
}

// In handleClientMessage:
case protocol.MsgApprovePR:
	appMsg := msg.(*protocol.ApprovePRMessage)
	go func() {
		err := d.ghClient.ApprovePR(appMsg.Repo, appMsg.Number)
		// ...
	}()
```

**Step 3: Run tests**

Run: `go test ./internal/daemon -v`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/websocket.go
git commit -m "refactor(daemon): use GitHubClient interface"
```

---

## Task 11: Delete Old Fetcher (Keep Only Client)

**Files:**
- Delete: `internal/github/github.go` (the old Fetcher)
- Update: `internal/github/github_test.go` (remove Fetcher tests)

**Step 1: Remove old code**

Delete `internal/github/github.go` entirely.

Update `internal/github/github_test.go` to only keep `parsePRList` and `convertPR` tests if needed, or delete if fully covered by new client tests.

**Step 2: Run tests**

Run: `go test ./... -v`
Expected: PASS (all tests should use new client)

**Step 3: Commit**

```bash
git rm internal/github/github.go
git add internal/github/
git commit -m "refactor(github): remove old Fetcher, use only Client"
```

---

## Task 12: Create Mock GitHub Server Package

**Files:**
- Create: `internal/github/mockserver/server.go`
- Create: `internal/github/mockserver/server_test.go`

**Step 1: Write the mock server**

```go
// internal/github/mockserver/server.go
package mockserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
)

// RequestLog captures a request for assertions
type RequestLog struct {
	Method string
	Path   string
	Body   map[string]interface{}
}

// Server is a mock GitHub API server for testing
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	requests []RequestLog
	// Configurable responses
	PRs []MockPR
}

type MockPR struct {
	Repo   string
	Number int
	Title  string
	Draft  bool
	Role   string // "author" or "reviewer"
}

// New creates a new mock GitHub server
func New() *Server {
	s := &Server{
		requests: []RequestLog{},
		PRs:      []MockPR{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)
	s.Server = httptest.NewServer(mux)
	return s
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()

	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	s.requests = append(s.requests, RequestLog{
		Method: r.Method,
		Path:   r.URL.Path,
		Body:   body,
	})
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")

	// Search endpoint
	if r.URL.Path == "/search/issues" {
		s.handleSearch(w, r)
		return
	}

	// PR review (approve)
	reviewPattern := regexp.MustCompile(`^/repos/([^/]+/[^/]+)/pulls/(\d+)/reviews$`)
	if reviewPattern.MatchString(r.URL.Path) && r.Method == "POST" {
		json.NewEncoder(w).Encode(map[string]interface{}{"id": 1, "state": "APPROVED"})
		return
	}

	// PR merge
	mergePattern := regexp.MustCompile(`^/repos/([^/]+/[^/]+)/pulls/(\d+)/merge$`)
	if mergePattern.MatchString(r.URL.Path) && r.Method == "PUT" {
		json.NewEncoder(w).Encode(map[string]interface{}{"merged": true, "sha": "abc123"})
		return
	}

	// PR details
	prPattern := regexp.MustCompile(`^/repos/([^/]+/[^/]+)/pulls/(\d+)$`)
	if prPattern.MatchString(r.URL.Path) && r.Method == "GET" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"mergeable":       true,
			"mergeable_state": "clean",
			"head":            map[string]string{"sha": "abc123"},
		})
		return
	}

	// Check runs
	checkPattern := regexp.MustCompile(`^/repos/([^/]+/[^/]+)/commits/([^/]+)/check-runs$`)
	if checkPattern.MatchString(r.URL.Path) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"check_runs": []map[string]interface{}{{"conclusion": "success"}},
		})
		return
	}

	// Reviews list
	reviewsPattern := regexp.MustCompile(`^/repos/([^/]+/[^/]+)/pulls/(\d+)/reviews$`)
	if reviewsPattern.MatchString(r.URL.Path) && r.Method == "GET" {
		json.NewEncoder(w).Encode([]map[string]interface{}{})
		return
	}

	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")

	var items []map[string]interface{}
	s.mu.Lock()
	for _, pr := range s.PRs {
		// Filter by query
		isAuthorSearch := regexp.MustCompile(`author:@me`).MatchString(q)
		isReviewSearch := regexp.MustCompile(`review-requested:@me`).MatchString(q)

		if (isAuthorSearch && pr.Role == "author") || (isReviewSearch && pr.Role == "reviewer") {
			items = append(items, map[string]interface{}{
				"number":         pr.Number,
				"title":          pr.Title,
				"html_url":       fmt.Sprintf("https://github.com/%s/pull/%d", pr.Repo, pr.Number),
				"draft":          pr.Draft,
				"repository_url": fmt.Sprintf("https://api.github.com/repos/%s", pr.Repo),
			})
		}
	}
	s.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_count": len(items),
		"items":       items,
	})
}

// AddPR adds a PR to the mock server
func (s *Server) AddPR(pr MockPR) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PRs = append(s.PRs, pr)
}

// Requests returns all captured requests
func (s *Server) Requests() []RequestLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RequestLog{}, s.requests...)
}

// Reset clears all state
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = []RequestLog{}
	s.PRs = []MockPR{}
}

// HasApproveRequest checks if approve was called for a PR
func (s *Server) HasApproveRequest(repo string, number int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number)
	for _, req := range s.requests {
		if req.Method == "POST" && req.Path == path {
			if event, ok := req.Body["event"].(string); ok && event == "APPROVE" {
				return true
			}
		}
	}
	return false
}

// HasMergeRequest checks if merge was called for a PR
func (s *Server) HasMergeRequest(repo string, number int, method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, number)
	for _, req := range s.requests {
		if req.Method == "PUT" && req.Path == path {
			if m, ok := req.Body["merge_method"].(string); ok && m == method {
				return true
			}
		}
	}
	return false
}
```

**Step 2: Write test**

```go
// internal/github/mockserver/server_test.go
package mockserver

import (
	"os"
	"testing"

	"github.com/victorarias/claude-manager/internal/github"
)

func TestMockServer_SearchAndApprove(t *testing.T) {
	server := New()
	defer server.Close()

	// Add test PR
	server.AddPR(MockPR{
		Repo:   "test/repo",
		Number: 123,
		Title:  "Test PR",
		Draft:  false,
		Role:   "reviewer",
	})

	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, err := github.NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	// Fetch PRs
	prs, err := client.FetchAll()
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want 1", len(prs))
	}

	// Approve
	err = client.ApprovePR("test/repo", 123)
	if err != nil {
		t.Fatalf("ApprovePR error: %v", err)
	}

	if !server.HasApproveRequest("test/repo", 123) {
		t.Error("Expected approve request for test/repo#123")
	}
}
```

**Step 3: Run tests**

Run: `go test ./internal/github/mockserver -v`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/github/mockserver/
git commit -m "feat(github): add mock server for testing"
```

---

## Task 13: Add Daemon Integration Test with Mock GitHub

**Files:**
- Modify: `internal/daemon/daemon_test.go`

**Step 1: Write integration test**

```go
// Add to internal/daemon/daemon_test.go
import (
	"context"
	"nhooyr.io/websocket"

	"github.com/victorarias/claude-manager/internal/github"
	"github.com/victorarias/claude-manager/internal/github/mockserver"
)

func TestDaemon_ApprovePR_ViaWebSocket(t *testing.T) {
	// Start mock GitHub
	mockGH := mockserver.New()
	defer mockGH.Close()

	mockGH.AddPR(mockserver.MockPR{
		Repo:   "test/repo",
		Number: 42,
		Title:  "Test PR",
		Role:   "reviewer",
	})

	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	// Create client with mock
	ghClient, err := github.NewClient(mockGH.URL)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	// Start daemon
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")
	os.Setenv("CM_WS_PORT", "19849")
	defer os.Unsetenv("CM_WS_PORT")

	d := NewWithGitHubClient(sockPath, ghClient)
	go d.Start()
	defer d.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect WebSocket
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, "ws://127.0.0.1:19849/ws", nil)
	if err != nil {
		t.Fatalf("WebSocket dial error: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	// Read initial state
	_, _, _ = ws.Read(ctx)

	// Send approve
	approveCmd := `{"cmd":"approve_pr","repo":"test/repo","number":42}`
	ws.Write(ctx, websocket.MessageText, []byte(approveCmd))

	// Read response
	_, respBytes, _ := ws.Read(ctx)
	var resp map[string]interface{}
	json.Unmarshal(respBytes, &resp)

	if resp["success"] != true {
		t.Errorf("success = %v, want true", resp["success"])
	}

	// Verify mock received request
	if !mockGH.HasApproveRequest("test/repo", 42) {
		t.Error("Mock did not receive approve request")
	}
}
```

**Step 2: Run test**

Run: `go test ./internal/daemon -run TestDaemon_ApprovePR_ViaWebSocket -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/daemon/daemon_test.go
git commit -m "test(daemon): add WebSocket PR action integration test"
```

---

## Task 14: Setup Playwright E2E Infrastructure

**Files:**
- Modify: `app/package.json`
- Create: `app/playwright.config.ts`
- Create: `app/e2e/fixtures.ts`

**Step 1: Install Playwright**

```bash
cd app && pnpm add -D @playwright/test
```

**Step 2: Create Playwright config**

```typescript
// app/playwright.config.ts
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 30000,
  retries: 0,
  use: {
    baseURL: 'http://localhost:1420',
    trace: 'on-first-retry',
  },
  webServer: {
    command: 'pnpm run dev',
    url: 'http://localhost:1420',
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
  },
});
```

**Step 3: Create E2E fixtures**

```typescript
// app/e2e/fixtures.ts
import { test as base, expect } from '@playwright/test';
import { spawn, ChildProcess } from 'child_process';
import * as http from 'http';
import * as path from 'path';
import * as fs from 'fs';
import * as os from 'os';
import * as net from 'net';

// Mock GitHub Server
class MockGitHub {
  private server: http.Server;
  private requests: Array<{ method: string; path: string; body: any }> = [];
  public url = '';
  private prs: Array<{ repo: string; number: number; title: string; role: string }> = [];

  constructor() {
    this.server = http.createServer((req, res) => {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => {
        const parsed = body ? JSON.parse(body) : {};
        this.requests.push({ method: req.method!, path: req.url!, body: parsed });

        res.setHeader('Content-Type', 'application/json');

        // Handle search
        if (req.url?.startsWith('/search/issues')) {
          const q = new URL(`http://x${req.url}`).searchParams.get('q') || '';
          const isAuthor = q.includes('author:@me');
          const isReview = q.includes('review-requested:@me');

          const items = this.prs
            .filter((pr) => (isAuthor && pr.role === 'author') || (isReview && pr.role === 'reviewer'))
            .map((pr) => ({
              number: pr.number,
              title: pr.title,
              html_url: `https://github.com/${pr.repo}/pull/${pr.number}`,
              draft: false,
              repository_url: `https://api.github.com/repos/${pr.repo}`,
            }));

          res.end(JSON.stringify({ total_count: items.length, items }));
          return;
        }

        // Handle approve
        if (req.url?.match(/\/repos\/[^/]+\/[^/]+\/pulls\/\d+\/reviews$/) && req.method === 'POST') {
          res.end(JSON.stringify({ id: 1, state: 'APPROVED' }));
          return;
        }

        // Handle merge
        if (req.url?.match(/\/repos\/[^/]+\/[^/]+\/pulls\/\d+\/merge$/) && req.method === 'PUT') {
          res.end(JSON.stringify({ merged: true }));
          return;
        }

        res.statusCode = 404;
        res.end(JSON.stringify({ message: 'Not Found' }));
      });
    });
  }

  async start(): Promise<void> {
    return new Promise((resolve) => {
      this.server.listen(0, '127.0.0.1', () => {
        const addr = this.server.address() as net.AddressInfo;
        this.url = `http://127.0.0.1:${addr.port}`;
        resolve();
      });
    });
  }

  addPR(pr: { repo: string; number: number; title: string; role: 'author' | 'reviewer' }) {
    this.prs.push(pr);
  }

  hasApproveRequest(repo: string, number: number): boolean {
    return this.requests.some(
      (r) => r.method === 'POST' && r.path === `/repos/${repo}/pulls/${number}/reviews` && r.body.event === 'APPROVE'
    );
  }

  hasMergeRequest(repo: string, number: number): boolean {
    return this.requests.some((r) => r.method === 'PUT' && r.path === `/repos/${repo}/pulls/${number}/merge`);
  }

  reset() {
    this.requests = [];
    this.prs = [];
  }

  close() {
    this.server.close();
  }
}

// Daemon launcher
async function startDaemon(ghUrl: string, wsPort: number): Promise<{ proc: ChildProcess; socketPath: string; stop: () => void }> {
  const socketPath = path.join(os.tmpdir(), `cm-e2e-${Date.now()}.sock`);
  const cmPath = path.join(os.homedir(), '.local', 'bin', 'cm');

  const proc = spawn(cmPath, ['--daemon'], {
    env: {
      ...process.env,
      CM_SOCKET: socketPath,
      CM_WS_PORT: String(wsPort),
      GITHUB_API_URL: ghUrl,
      GITHUB_TOKEN: 'test-token',
    },
    stdio: 'pipe',
  });

  // Wait for socket
  await new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('Daemon timeout')), 5000);
    const check = setInterval(() => {
      if (fs.existsSync(socketPath)) {
        clearInterval(check);
        clearTimeout(timeout);
        resolve();
      }
    }, 100);
  });

  return {
    proc,
    socketPath,
    stop() {
      proc.kill();
      try {
        fs.unlinkSync(socketPath);
      } catch {}
    },
  };
}

// Inject test PR via Unix socket
async function injectTestPR(
  socketPath: string,
  pr: { id: string; repo: string; number: number; title: string; role: string }
): Promise<void> {
  return new Promise((resolve, reject) => {
    const client = net.createConnection(socketPath, () => {
      client.write(JSON.stringify({ cmd: 'inject_test_pr', ...pr }));
    });
    client.on('data', () => {
      client.end();
      resolve();
    });
    client.on('error', reject);
  });
}

// Export fixtures
type Fixtures = {
  mockGitHub: MockGitHub;
  daemonWsUrl: string;
  daemonSocketPath: string;
};

export const test = base.extend<Fixtures>({
  mockGitHub: async ({}, use) => {
    const mock = new MockGitHub();
    await mock.start();
    await use(mock);
    mock.close();
  },

  daemonWsUrl: async ({ mockGitHub }, use) => {
    const wsPort = 29849;
    const daemon = await startDaemon(mockGitHub.url, wsPort);
    await use(`ws://127.0.0.1:${wsPort}/ws`);
    daemon.stop();
  },

  daemonSocketPath: async ({ mockGitHub }, use) => {
    const wsPort = 29849;
    const daemon = await startDaemon(mockGitHub.url, wsPort);
    await use(daemon.socketPath);
    daemon.stop();
  },
});

export { expect, injectTestPR };
```

**Step 4: Add test script**

```json
// In app/package.json scripts:
"test:e2e": "playwright test"
```

**Step 5: Commit**

```bash
git add app/package.json app/playwright.config.ts app/e2e/
git commit -m "chore(app): setup Playwright E2E infrastructure"
```

---

## Task 15: Add Test PR Injection to Daemon

**Files:**
- Modify: `internal/protocol/types.go`
- Modify: `internal/protocol/parse.go`
- Modify: `internal/daemon/daemon.go`

**Step 1: Add protocol message**

```go
// internal/protocol/types.go
const CmdInjectTestPR = "inject_test_pr"

type InjectTestPRMessage struct {
	ID     string `json:"id"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Role   string `json:"role"`
}
```

**Step 2: Add parser**

```go
// internal/protocol/parse.go - add case
case CmdInjectTestPR:
	var msg InjectTestPRMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", nil, err
	}
	return cmd, &msg, nil
```

**Step 3: Add handler**

```go
// internal/daemon/daemon.go - in handleConnection switch
case protocol.CmdInjectTestPR:
	d.handleInjectTestPR(conn, msg.(*protocol.InjectTestPRMessage))

// Add handler method
func (d *Daemon) handleInjectTestPR(conn net.Conn, msg *protocol.InjectTestPRMessage) {
	pr := &protocol.PR{
		ID:          msg.ID,
		Repo:        msg.Repo,
		Number:      msg.Number,
		Title:       msg.Title,
		URL:         msg.URL,
		Role:        msg.Role,
		State:       protocol.StateWaiting,
		Reason:      protocol.PRReasonReviewNeeded,
		LastUpdated: time.Now(),
		LastPolled:  time.Now(),
	}
	d.store.AddPR(pr)
	d.sendOK(conn)

	// Broadcast
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		PRs:   d.store.ListPRs(""),
	})
}
```

**Step 4: Run tests**

Run: `go test ./internal/daemon -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/protocol/ internal/daemon/daemon.go
git commit -m "feat(daemon): add test PR injection endpoint"
```

---

## Task 16: E2E Test - Full Flow Approve PR

**Files:**
- Create: `app/e2e/pr-approve.spec.ts`
- Modify: `app/src/App.tsx` (add test WS URL support)

**Step 1: Add test WS URL support to App**

```typescript
// app/src/App.tsx - at component start
const wsUrl = (window as any).__TEST_WS_URL__ || 'ws://127.0.0.1:9849/ws';
// Use this in useDaemonSocket call
```

**Step 2: Add data-pr-id to PR rows**

```typescript
// In Dashboard.tsx or wherever PRs are rendered
<div data-pr-id={pr.id} className="pr-row">
```

**Step 3: Write E2E test**

```typescript
// app/e2e/pr-approve.spec.ts
import { test, expect, injectTestPR } from './fixtures';

test('approve PR via UI sends request to mock GitHub', async ({ page, mockGitHub, daemonWsUrl, daemonSocketPath }) => {
  // Add test PR to mock server
  mockGitHub.addPR({
    repo: 'test/repo',
    number: 123,
    title: 'Test PR for E2E',
    role: 'reviewer',
  });

  // Inject PR into daemon so it appears in UI
  await injectTestPR(daemonSocketPath, {
    id: 'test/repo#123',
    repo: 'test/repo',
    number: 123,
    title: 'Test PR for E2E',
    url: 'https://github.com/test/repo/pull/123',
    role: 'reviewer',
  });

  // Set test WebSocket URL
  await page.addInitScript((url) => {
    (window as any).__TEST_WS_URL__ = url;
  }, daemonWsUrl);

  await page.goto('/');

  // Wait for PR to appear
  await expect(page.locator('text=Test PR for E2E')).toBeVisible({ timeout: 10000 });

  // Click approve
  const prRow = page.locator('[data-pr-id="test/repo#123"]');
  const approveBtn = prRow.locator('button[data-action="approve"]');
  await approveBtn.click();

  // Wait for success
  await expect(approveBtn).toHaveAttribute('data-success', 'true', { timeout: 10000 });

  // Verify mock received request
  expect(mockGitHub.hasApproveRequest('test/repo', 123)).toBe(true);
});

test('merge PR via UI sends request to mock GitHub', async ({ page, mockGitHub, daemonWsUrl, daemonSocketPath }) => {
  mockGitHub.addPR({
    repo: 'test/repo',
    number: 456,
    title: 'Test PR for Merge',
    role: 'author',
  });

  await injectTestPR(daemonSocketPath, {
    id: 'test/repo#456',
    repo: 'test/repo',
    number: 456,
    title: 'Test PR for Merge',
    url: 'https://github.com/test/repo/pull/456',
    role: 'author',
  });

  await page.addInitScript((url) => {
    (window as any).__TEST_WS_URL__ = url;
  }, daemonWsUrl);

  await page.goto('/');

  await expect(page.locator('text=Test PR for Merge')).toBeVisible({ timeout: 10000 });

  const prRow = page.locator('[data-pr-id="test/repo#456"]');
  const mergeBtn = prRow.locator('button[data-action="merge"]');
  await mergeBtn.click();

  // Confirm modal
  await page.locator('.modal-btn-primary').click();

  // Wait for success
  await expect(mergeBtn).toHaveAttribute('data-success', 'true', { timeout: 10000 });

  // Verify mock received request
  expect(mockGitHub.hasMergeRequest('test/repo', 456)).toBe(true);
});
```

**Step 4: Run E2E tests**

Run: `cd app && pnpm run test:e2e`
Expected: PASS - Both tests verify UI → WebSocket → Daemon → Mock GitHub

**Step 5: Commit**

```bash
git add app/e2e/pr-approve.spec.ts app/src/App.tsx app/src/components/Dashboard.tsx
git commit -m "test(e2e): add full-flow PR approve/merge E2E tests"
```

---

## Summary

This plan implements complete replacement of `gh` CLI with direct API:

| Tasks | What It Does |
|-------|--------------|
| **1-2** | Client with token resolution + HTTP helper |
| **3-5** | Search PRs (authored + review-requested + FetchAll) |
| **6-8** | FetchPRDetails, ApprovePR, MergePR |
| **9-10** | Interface + daemon refactor |
| **11** | Delete old Fetcher code |
| **12-13** | Mock server + daemon integration test |
| **14-15** | Playwright setup + test PR injection |
| **16** | Full E2E test: UI → Mock GitHub verification |

All GitHub operations now go through direct HTTP API, fully testable with mock server.
