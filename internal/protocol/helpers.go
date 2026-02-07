package protocol

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Timestamp is a string representation of time in RFC3339 format.
// Used in generated types for JSON serialization, with helper methods
// for conversion to/from time.Time.
type Timestamp string

// Time parses the timestamp string into time.Time.
// Returns zero time if the string is empty or invalid.
func (t Timestamp) Time() time.Time {
	if t == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, string(t))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// IsZero returns true if the timestamp is empty or represents zero time.
func (t Timestamp) IsZero() bool {
	return t == "" || t.Time().IsZero()
}

// String returns the string representation.
func (t Timestamp) String() string {
	return string(t)
}

// NewTimestamp creates a Timestamp from time.Time.
func NewTimestamp(t time.Time) Timestamp {
	if t.IsZero() {
		return ""
	}
	return Timestamp(t.Format(time.RFC3339))
}

// Now returns the current time as a Timestamp.
func TimestampNow() Timestamp {
	return NewTimestamp(time.Now())
}

// Pointer helper functions for working with optional fields.

// Ptr returns a pointer to the given value.
func Ptr[T any](v T) *T {
	return &v
}

// Deref returns the value pointed to, or the zero value if nil.
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// DerefOr returns the value pointed to, or the default if nil.
func DerefOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

func normalizeSessionAgentValue(agent string) SessionAgent {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case string(SessionAgentClaude):
		return SessionAgentClaude
	case string(SessionAgentCodex):
		return SessionAgentCodex
	default:
		return ""
	}
}

// NormalizeSessionAgent returns a valid stored session agent.
// Invalid/empty values fall back to fallback (or codex if fallback is invalid).
func NormalizeSessionAgent(agent, fallback SessionAgent) SessionAgent {
	if normalized := normalizeSessionAgentValue(string(agent)); normalized != "" {
		return normalized
	}
	if normalizedFallback := normalizeSessionAgentValue(string(fallback)); normalizedFallback != "" {
		return normalizedFallback
	}
	return SessionAgentCodex
}

// NormalizeSessionAgentString normalizes string input to a valid session agent.
func NormalizeSessionAgentString(agent, fallback string) SessionAgent {
	return NormalizeSessionAgent(SessionAgent(agent), SessionAgent(fallback))
}

// NormalizeSpawnAgent returns a valid spawn agent value.
// Accepts "shell" in addition to session agents.
func NormalizeSpawnAgent(agent, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case string(SessionAgentClaude):
		return string(SessionAgentClaude)
	case string(SessionAgentCodex):
		return string(SessionAgentCodex)
	case AgentShellValue:
		return AgentShellValue
	}

	switch strings.ToLower(strings.TrimSpace(fallback)) {
	case string(SessionAgentClaude):
		return string(SessionAgentClaude)
	case string(SessionAgentCodex):
		return string(SessionAgentCodex)
	case AgentShellValue:
		return AgentShellValue
	}

	return string(SessionAgentCodex)
}

// Slice conversion helpers for Response types.
// Store returns pointer slices, but generated Response expects value slices.

func SessionsToValues(sessions []*Session) []Session {
	if sessions == nil {
		return nil
	}
	result := make([]Session, len(sessions))
	for i, s := range sessions {
		if s != nil {
			result[i] = *s
		}
	}
	return result
}

func PRsToValues(prs []*PR) []PR {
	if prs == nil {
		return nil
	}
	result := make([]PR, len(prs))
	for i, p := range prs {
		if p != nil {
			result[i] = *p
		}
	}
	return result
}

func RepoStatesToValues(repos []*RepoState) []RepoState {
	if repos == nil {
		return nil
	}
	result := make([]RepoState, len(repos))
	for i, r := range repos {
		if r != nil {
			result[i] = *r
		}
	}
	return result
}

func AuthorStatesToValues(authors []*AuthorState) []AuthorState {
	if authors == nil {
		return nil
	}
	result := make([]AuthorState, len(authors))
	for i, a := range authors {
		if a != nil {
			result[i] = *a
		}
	}
	return result
}

func RecentLocationsToValues(locs []*RecentLocation) []RecentLocation {
	if locs == nil {
		return nil
	}
	result := make([]RecentLocation, len(locs))
	for i, l := range locs {
		if l != nil {
			result[i] = *l
		}
	}
	return result
}

func BranchesToValues(branches []*Branch) []Branch {
	if branches == nil {
		return nil
	}
	result := make([]Branch, len(branches))
	for i, b := range branches {
		if b != nil {
			result[i] = *b
		}
	}
	return result
}

// ParsePRID parses a PR id in either old or new format.
// Old: "owner/repo#123" (defaults to github.com)
// New: "host:owner/repo#123"
func ParsePRID(id string) (host, repo string, number int, err error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", 0, fmt.Errorf("empty PR id")
	}

	hashIdx := strings.LastIndex(id, "#")
	if hashIdx <= 0 || hashIdx == len(id)-1 {
		return "", "", 0, fmt.Errorf("invalid PR id: %s", id)
	}

	left := id[:hashIdx]
	numStr := id[hashIdx+1:]
	parsedNum, err := strconv.Atoi(numStr)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid PR number: %s", numStr)
	}

	host = "github.com"
	repo = left
	if colonIdx := strings.Index(left, ":"); colonIdx > 0 {
		prefix := left[:colonIdx]
		suffix := left[colonIdx+1:]
		if strings.Contains(prefix, ".") || !strings.Contains(prefix, "/") {
			host = prefix
			repo = suffix
		}
	}

	if repo == "" || !strings.Contains(repo, "/") {
		return "", "", 0, fmt.Errorf("invalid PR repo: %s", repo)
	}

	return host, repo, parsedNum, nil
}

// FormatPRID formats a PR id in the new format.
func FormatPRID(host, repo string, number int) string {
	if host == "" {
		host = "github.com"
	}
	return fmt.Sprintf("%s:%s#%d", host, repo, number)
}
