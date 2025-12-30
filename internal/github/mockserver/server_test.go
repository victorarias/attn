// internal/github/mockserver/server_test.go
package mockserver

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/github"
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
