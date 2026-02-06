package github

import (
	"fmt"
	"sort"
	"sync"

	"github.com/victorarias/attn/internal/protocol"
)

// ClientRegistry stores GitHub clients per host.
type ClientRegistry struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

// NewClientRegistry creates an empty registry.
func NewClientRegistry() *ClientRegistry {
	return &ClientRegistry{clients: make(map[string]*Client)}
}

// Register stores a client for the given host.
func (r *ClientRegistry) Register(host string, client *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[host] = client
}

// Remove removes a client for the given host.
func (r *ClientRegistry) Remove(host string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, host)
}

// Get returns the client for the host.
func (r *ClientRegistry) Get(host string) (*Client, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	client, ok := r.clients[host]
	return client, ok
}

// Hosts returns all registered hosts.
func (r *ClientRegistry) Hosts() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.clients) == 0 {
		return nil
	}
	hosts := make([]string, 0, len(r.clients))
	for host := range r.clients {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

// FetchAllPRs aggregates PRs from all registered hosts.
func (r *ClientRegistry) FetchAllPRs() ([]*protocol.PR, error) {
	clients := r.snapshotClients()
	if len(clients) == 0 {
		return nil, nil
	}

	var allPRs []*protocol.PR
	var errOut error
	for host, client := range clients {
		prs, err := client.FetchAll()
		if err != nil {
			if errOut == nil {
				errOut = fmt.Errorf("fetch PRs for %s: %w", host, err)
			}
			continue
		}
		allPRs = append(allPRs, prs...)
	}
	return allPRs, errOut
}

// IsAnyHostRateLimited returns true if any host is rate limited for the resource.
func (r *ClientRegistry) IsAnyHostRateLimited(resource string) bool {
	clients := r.snapshotClients()
	for _, client := range clients {
		if limited, _ := client.IsRateLimited(resource); limited {
			return true
		}
	}
	return false
}

// GetRateLimitedHosts returns the hosts that are rate limited for the resource.
func (r *ClientRegistry) GetRateLimitedHosts(resource string) []string {
	clients := r.snapshotClients()
	var hosts []string
	for host, client := range clients {
		if limited, _ := client.IsRateLimited(resource); limited {
			hosts = append(hosts, host)
		}
	}
	sort.Strings(hosts)
	return hosts
}

// NewClientForHost creates a GitHub API client for a specific host using the provided token.
// This requires explicit tokens to avoid cross-contamination between hosts.
func NewClientForHost(host, apiURL, token string) (*Client, error) {
	if apiURL == "" {
		apiURL = mapHostToAPIURL(host)
	}
	if token == "" {
		return nil, ErrNoToken
	}
	return newClientWithToken(host, apiURL, token)
}

func (r *ClientRegistry) snapshotClients() map[string]*Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.clients) == 0 {
		return nil
	}
	clients := make(map[string]*Client, len(r.clients))
	for host, client := range r.clients {
		clients[host] = client
	}
	return clients
}
