package github

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/github/mockserver"
)

func TestClientRegistryCRUD(t *testing.T) {
	registry := NewClientRegistry()
	if got := registry.Hosts(); len(got) != 0 {
		t.Fatalf("expected empty hosts, got %v", got)
	}

	client, err := newClientWithToken("github.com", "http://localhost:9999", "test-token")
	if err != nil {
		t.Fatalf("newClientWithToken error: %v", err)
	}

	registry.Register("github.com", client)
	if _, ok := registry.Get("github.com"); !ok {
		t.Fatalf("expected registered client")
	}

	hosts := registry.Hosts()
	if len(hosts) != 1 || hosts[0] != "github.com" {
		t.Fatalf("hosts = %v", hosts)
	}

	registry.Remove("github.com")
	if _, ok := registry.Get("github.com"); ok {
		t.Fatalf("expected client removed")
	}
}

func TestClientRegistryFetchAllPRs(t *testing.T) {
	serverA := mockserver.New()
	defer serverA.Close()
	serverB := mockserver.New()
	defer serverB.Close()

	serverA.AddPR(mockserver.MockPR{Repo: "owner/a", Number: 1, Title: "A", Role: "reviewer"})
	serverB.AddPR(mockserver.MockPR{Repo: "owner/b", Number: 2, Title: "B", Role: "reviewer"})

	clientA, err := NewClientForHost("github.com", serverA.URL, "test-token")
	if err != nil {
		t.Fatalf("NewClientForHost A error: %v", err)
	}
	clientB, err := NewClientForHost("ghe.corp.com", serverB.URL, "test-token")
	if err != nil {
		t.Fatalf("NewClientForHost B error: %v", err)
	}

	registry := NewClientRegistry()
	registry.Register("github.com", clientA)
	registry.Register("ghe.corp.com", clientB)

	prs, err := registry.FetchAllPRs()
	if err != nil {
		t.Fatalf("FetchAllPRs error: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("expected 2 PRs, got %d", len(prs))
	}

	found := map[string]bool{}
	for _, pr := range prs {
		found[pr.ID] = true
	}
	if !found["github.com:owner/a#1"] || !found["ghe.corp.com:owner/b#2"] {
		t.Fatalf("unexpected PR IDs: %v", found)
	}
}

func TestClientRegistryRateLimits(t *testing.T) {
	clientA, err := NewClientForHost("github.com", "http://localhost:9999", "test-token")
	if err != nil {
		t.Fatalf("NewClientForHost error: %v", err)
	}
	clientB, err := NewClientForHost("ghe.corp.com", "http://localhost:9998", "test-token")
	if err != nil {
		t.Fatalf("NewClientForHost error: %v", err)
	}

	clientA.rateLimitsMu.Lock()
	clientA.rateLimits["search"] = &RateLimitInfo{Resource: "search", Remaining: 0, ResetAt: time.Now().Add(1 * time.Hour)}
	clientA.rateLimitsMu.Unlock()

	registry := NewClientRegistry()
	registry.Register("github.com", clientA)
	registry.Register("ghe.corp.com", clientB)

	if !registry.IsAnyHostRateLimited("search") {
		t.Fatalf("expected rate limited true")
	}

	hosts := registry.GetRateLimitedHosts("search")
	if len(hosts) != 1 || hosts[0] != "github.com" {
		t.Fatalf("rate limited hosts = %v", hosts)
	}
}
