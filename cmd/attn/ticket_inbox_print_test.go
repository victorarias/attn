package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// TestFprintTicketInboxUserPresence covers the presence header this feature
// adds ahead of the unread bundles: printed when the daemon has observed the
// user at the app (last_user_activity_at present), omitted entirely when it
// hasn't (nil) — mirroring the daemon's own absent-when-unobserved contract.
func TestFprintTicketInboxUserPresence(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		lastActive := time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339)
		result := &protocol.TicketInboxResult{
			Bundles:            []protocol.TicketEventBundle{{TicketID: "tkt-1"}},
			LastUserActivityAt: &lastActive,
		}
		var buf bytes.Buffer
		fprintTicketInbox(&buf, result)
		out := buf.String()
		if !strings.HasPrefix(out, "user: active 1m ago\n") {
			t.Fatalf("output = %q, want presence header first, got prefix %q", out, out[:min(len(out), 40)])
		}
		if !strings.Contains(out, "tkt-1") {
			t.Fatalf("output = %q, want bundle still printed", out)
		}
	})

	t.Run("absent", func(t *testing.T) {
		result := &protocol.TicketInboxResult{
			Bundles: []protocol.TicketEventBundle{{TicketID: "tkt-1"}},
		}
		var buf bytes.Buffer
		fprintTicketInbox(&buf, result)
		out := buf.String()
		if strings.Contains(out, "user: active") {
			t.Fatalf("output = %q, want no presence header when last_user_activity_at is nil", out)
		}
	})
}

func TestHumanizeDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "5s"},
		{90 * time.Second, "1m"},
		{45 * time.Minute, "45m"},
		{3 * time.Hour, "3h"},
	}
	for _, c := range cases {
		if got := humanizeDuration(c.d); got != c.want {
			t.Errorf("humanizeDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
