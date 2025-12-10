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
