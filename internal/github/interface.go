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
