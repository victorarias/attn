package protocol

import (
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
