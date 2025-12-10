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
