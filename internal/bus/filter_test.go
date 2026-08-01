package bus

import "testing"

func TestFilterMatching(t *testing.T) {
	cases := []struct {
		filter Filter
		name   string
		want   bool
	}{
		{All, "anything.at.all", true},
		{Filter{}, "anything.at.all", true},
		{Filter{"session.*"}, "session.state.changed", true},
		{Filter{"session.*"}, "session.registered", true},
		// The dot is part of the prefix, so a sibling domain does not leak in.
		{Filter{"session.*"}, "sessions.updated", false},
		{Filter{"session.*"}, "session", false},
		{Filter{"ticket.commented"}, "ticket.commented", true},
		{Filter{"ticket.commented"}, "ticket.created", false},
		{Filter{"ticket.*", "pr.updated"}, "pr.updated", true},
		{Filter{"ticket.*", "pr.updated"}, "session.state.changed", false},
		{Filter{"ext.gate.*"}, "ext.gate.approved", true},
		{Filter{"ext.gate.*"}, "ext.other.approved", false},
	}
	for _, tc := range cases {
		if got := tc.filter.Matches(tc.name); got != tc.want {
			t.Errorf("Filter%v.Matches(%q) = %v, want %v", tc.filter, tc.name, got, tc.want)
		}
	}
}

func TestParseFilterRoundTrip(t *testing.T) {
	f := ParseFilter("session.*, ticket.commented ")
	if len(f) != 2 || f[0] != "session.*" || f[1] != "ticket.commented" {
		t.Fatalf("ParseFilter dropped or mangled patterns: %v", f)
	}
	if f.String() != "session.*,ticket.commented" {
		t.Fatalf("String() = %q", f.String())
	}
	// A consumer row written without a filter must not be a consumer that
	// receives nothing.
	if got := ParseFilter(""); !got.Matches("whatever.happened") {
		t.Fatalf("empty expression should mean All, got %v", got)
	}
	if got := ParseFilter("  ,  "); !got.Matches("whatever.happened") {
		t.Fatalf("blank expression should mean All, got %v", got)
	}
}
