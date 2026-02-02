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
	// Use a real-looking token (not "test-token") since test-token is blocked
	// when targeting real GitHub API
	os.Setenv("GITHUB_TOKEN", "ghp_xxxxxxxxxxxx")
	os.Unsetenv("GITHUB_API_URL")
	os.Unsetenv("GITHUB_BASE_URL")
	defer os.Unsetenv("GITHUB_TOKEN")

	// Clear GITHUB_API_URL to test the default behavior
	origAPIURL := os.Getenv("GITHUB_API_URL")
	os.Unsetenv("GITHUB_API_URL")
	defer func() {
		if origAPIURL != "" {
			os.Setenv("GITHUB_API_URL", origAPIURL)
		}
	}()

	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if client.baseURL != "https://api.github.com" {
		t.Errorf("baseURL = %q, want %q", client.baseURL, "https://api.github.com")
	}
}

func TestNewClient_BlocksTestTokenWithRealAPI(t *testing.T) {
	os.Setenv("GITHUB_TOKEN", "test-token")
	os.Unsetenv("GITHUB_API_URL")
	os.Unsetenv("GITHUB_BASE_URL")
	defer os.Unsetenv("GITHUB_TOKEN")

	// Clear GITHUB_API_URL so we actually target the real API
	origAPIURL := os.Getenv("GITHUB_API_URL")
	os.Unsetenv("GITHUB_API_URL")
	defer func() {
		if origAPIURL != "" {
			os.Setenv("GITHUB_API_URL", origAPIURL)
		}
	}()

	// Should fail because test-token + real API is blocked
	_, err := NewClient("")
	if err == nil {
		t.Fatal("NewClient should fail when test-token is used with real GitHub API")
	}

	if !contains(err.Error(), "refusing to use real GitHub API") {
		t.Errorf("Error message = %q, should mention refusing to use real API", err.Error())
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
	if callCount != 3 {
		t.Errorf("API called %d times, want 3", callCount)
	}
}

func TestClient_FetchPRDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case contains(r.URL.Path, "/pulls/42") && !contains(r.URL.Path, "/reviews"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"mergeable":       true,
				"mergeable_state": "clean",
				"head":            map[string]string{"sha": "abc123"},
			})
		case contains(r.URL.Path, "/check-runs"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"check_runs": []map[string]interface{}{
					{"conclusion": "success"},
					{"conclusion": "success"},
				},
			})
		case contains(r.URL.Path, "/reviews"):
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

func TestClient_ApprovePR(t *testing.T) {
	var capturedMethod, capturedPath string
	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path

		// Parse request body
		json.NewDecoder(r.Body).Decode(&capturedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":    12345,
			"state": "APPROVED",
		})
	}))
	defer server.Close()

	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, _ := NewClient(server.URL)
	err := client.ApprovePR("owner/repo", 42)
	if err != nil {
		t.Fatalf("ApprovePR error: %v", err)
	}

	// Verify correct HTTP method
	if capturedMethod != "POST" {
		t.Errorf("HTTP method = %q, want POST", capturedMethod)
	}

	// Verify correct endpoint
	expectedPath := "/repos/owner/repo/pulls/42/reviews"
	if capturedPath != expectedPath {
		t.Errorf("Path = %q, want %q", capturedPath, expectedPath)
	}

	// Verify request body contains {"event": "APPROVE"}
	if capturedBody["event"] != "APPROVE" {
		t.Errorf("Request body event = %v, want APPROVE", capturedBody["event"])
	}
}

func TestClient_ApprovePR_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message": "Resource not accessible by integration"}`))
	}))
	defer server.Close()

	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, _ := NewClient(server.URL)
	err := client.ApprovePR("owner/repo", 42)
	if err == nil {
		t.Fatal("ApprovePR should return error on 403 response")
	}

	// Verify error message contains status code
	if !contains(err.Error(), "403") {
		t.Errorf("Error message = %q, should contain 403", err.Error())
	}
}

func TestClient_MergePR(t *testing.T) {
	var capturedMethod, capturedPath string
	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path

		// Parse request body
		json.NewDecoder(r.Body).Decode(&capturedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sha":     "abc123",
			"merged":  true,
			"message": "Pull Request successfully merged",
		})
	}))
	defer server.Close()

	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	client, _ := NewClient(server.URL)
	err := client.MergePR("owner/repo", 42, "squash")
	if err != nil {
		t.Fatalf("MergePR error: %v", err)
	}

	// Verify correct HTTP method
	if capturedMethod != "PUT" {
		t.Errorf("HTTP method = %q, want PUT", capturedMethod)
	}

	// Verify correct endpoint
	expectedPath := "/repos/owner/repo/pulls/42/merge"
	if capturedPath != expectedPath {
		t.Errorf("Path = %q, want %q", capturedPath, expectedPath)
	}

	// Verify request body contains {"merge_method": "squash"}
	if capturedBody["merge_method"] != "squash" {
		t.Errorf("Request body merge_method = %v, want squash", capturedBody["merge_method"])
	}
}

func TestClient_MergePR_InvalidMethod(t *testing.T) {
	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	// Use a mock URL so we can create the client (test-token + real API is blocked)
	client, _ := NewClient("http://localhost:9999")
	err := client.MergePR("owner/repo", 42, "invalid")
	if err == nil {
		t.Fatal("MergePR should return error for invalid merge method")
	}

	// Verify error message mentions invalid method
	if !contains(err.Error(), "invalid") || !contains(err.Error(), "merge") {
		t.Errorf("Error message = %q, should mention invalid merge method", err.Error())
	}
}
