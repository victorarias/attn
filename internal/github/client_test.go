package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestClient_SearchAuthoredPRs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify correct endpoint
		if r.URL.Path != "/search/issues" {
			t.Errorf("Path = %q, want /search/issues", r.URL.Path)
		}

		q := r.URL.Query().Get("q")
		if !containsAll(q, "is:pr", "is:open", "author:@me") {
			t.Errorf("Query = %q, missing required qualifiers", q)
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"total_count": 2,
			"items": [
				{
					"number": 123,
					"title": "Test PR 1",
					"html_url": "https://github.com/owner/repo/pull/123",
					"draft": false,
					"repository_url": "https://api.github.com/repos/owner/repo"
				},
				{
					"number": 456,
					"title": "Draft PR",
					"html_url": "https://github.com/owner/repo/pull/456",
					"draft": true,
					"repository_url": "https://api.github.com/repos/owner/repo"
				}
			]
		}`))
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

// Helper function to check if string contains all substrings
func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !contains(s, part) {
			return false
		}
	}
	return true
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsInner(s, substr))
}

func containsInner(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestClient_SearchReviewRequestedPRs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if !contains(q, "review-requested:@me") {
			t.Errorf("Query = %q, missing review-requested:@me", q)
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"total_count": 1,
			"items": [
				{
					"number": 789,
					"title": "Needs Review",
					"html_url": "https://github.com/other/repo/pull/789",
					"draft": false,
					"repository_url": "https://api.github.com/repos/other/repo"
				}
			]
		}`))
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
	if prs[0].Role != "reviewer" {
		t.Errorf("Role = %q, want %q", prs[0].Role, "reviewer")
	}
	if prs[0].Reason != "review_needed" {
		t.Errorf("Reason = %q, want %q", prs[0].Reason, "review_needed")
	}
}

func TestClient_FetchAll(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		q := r.URL.Query().Get("q")

		var items []map[string]interface{}
		if contains(q, "author:@me") {
			items = []map[string]interface{}{
				{
					"number":         1,
					"title":          "My PR",
					"html_url":       "https://github.com/a/b/pull/1",
					"draft":          false,
					"repository_url": "https://api.github.com/repos/a/b",
				},
			}
		} else if contains(q, "review-requested:@me") {
			items = []map[string]interface{}{
				{
					"number":         2,
					"title":          "Review This",
					"html_url":       "https://github.com/c/d/pull/2",
					"draft":          false,
					"repository_url": "https://api.github.com/repos/c/d",
				},
			}
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		responseData := map[string]interface{}{
			"total_count": len(items),
			"items":       items,
		}

		// Convert to JSON
		jsonBytes, _ := json.Marshal(responseData)
		w.Write(jsonBytes)
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
