// Package attention provides a unified interface for items that need user attention.
package attention

import "time"

// Source represents anything that can require user attention.
// Both sessions and PRs implement this interface.
type Source interface {
	// AttentionID returns a unique identifier for this item
	AttentionID() string

	// AttentionKind returns the type of item ("session" or "pr")
	AttentionKind() string

	// AttentionLabel returns a human-readable label for display
	AttentionLabel() string

	// NeedsAttention returns true if this item currently requires user attention
	NeedsAttention() bool

	// AttentionReason returns why this item needs attention (e.g., "waiting_input", "review_needed")
	AttentionReason() string

	// AttentionSince returns when this item started needing attention
	AttentionSince() time.Time

	// AttentionMuted returns true if user has muted this item
	AttentionMuted() bool
}

// Item is a snapshot of an attention source for aggregation and filtering.
// It's a concrete struct for easier handling in collections.
type Item struct {
	ID     string
	Kind   string
	Label  string
	Reason string
	Since  time.Time
	Muted  bool
}

// FromSource creates an Item snapshot from any Source.
func FromSource(s Source) Item {
	return Item{
		ID:     s.AttentionID(),
		Kind:   s.AttentionKind(),
		Label:  s.AttentionLabel(),
		Reason: s.AttentionReason(),
		Since:  s.AttentionSince(),
		Muted:  s.AttentionMuted(),
	}
}

// NeedsAttention returns true if this item needs attention and is not muted.
func (i Item) NeedsAttention() bool {
	return i.Reason != "" && !i.Muted
}
