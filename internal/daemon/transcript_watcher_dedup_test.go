package daemon

import (
	"testing"
	"time"
)

func TestAssistantDedupWindowBehavior(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name        string
		last        string
		lastAt      time.Time
		current     string
		currentAt   time.Time
		expectDedup bool
	}{
		{
			name:        "duplicate within window deduped",
			last:        "Hi. Good to see you.",
			lastAt:      now,
			current:     "Hi. Good to see you.",
			currentAt:   now.Add(500 * time.Millisecond),
			expectDedup: true,
		},
		{
			name:        "duplicate outside window not deduped",
			last:        "Hi. Good to see you.",
			lastAt:      now,
			current:     "Hi. Good to see you.",
			currentAt:   now.Add(3 * time.Second),
			expectDedup: false,
		},
		{
			name:        "different text not deduped",
			last:        "Hi. Good to see you.",
			lastAt:      now,
			current:     "How can I help?",
			currentAt:   now.Add(500 * time.Millisecond),
			expectDedup: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deduped := isDuplicateAssistantEvent(tc.last, tc.lastAt, tc.current, tc.currentAt)
			if deduped != tc.expectDedup {
				t.Fatalf("deduped=%v want=%v", deduped, tc.expectDedup)
			}
		})
	}
}
