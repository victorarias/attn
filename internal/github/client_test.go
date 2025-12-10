package github

import (
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
