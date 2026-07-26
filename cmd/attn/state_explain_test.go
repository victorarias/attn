package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestParseStateExplainArgs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		want    stateExplainArgs
		wantErr bool
	}{
		{name: "target only", args: []string{"abc"}, want: stateExplainArgs{target: "abc"}},
		{name: "json", args: []string{"abc", "--json"}, want: stateExplainArgs{target: "abc", json: true}},
		{name: "no target", args: nil, wantErr: true},
		{name: "flag first", args: []string{"--json", "abc"}, wantErr: true},
		{name: "two targets", args: []string{"abc", "def"}, wantErr: true},
		{name: "unknown flag", args: []string{"abc", "--nope"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseStateExplainArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPrintStateExplainShowsEveryOutcome(t *testing.T) {
	var out bytes.Buffer
	printStateExplain(&out, &protocol.StateExplainResult{
		SessionID:  "sess-1",
		Agent:      "claude",
		State:      "working",
		StateSince: protocol.Ptr("2026-07-25T10:00:00Z"),
		Capacity:   256,
		Observations: []protocol.StateExplainEntry{
			{
				Source: "screen", Claim: "working", Outcome: "applied",
				Detail: protocol.Ptr("screen scrape"), Cause: protocol.Ptr("live_signal"),
				ObservedAt: "2026-07-25T10:00:00Z", RecordedAt: "2026-07-25T10:00:00Z",
			},
			{
				Source: "screen", Claim: "idle", Outcome: "vetoed",
				Reason:     protocol.Ptr("driver_transition_filter"),
				ObservedAt: "2026-07-25T10:00:01Z", RecordedAt: "2026-07-25T10:00:01Z",
			},
			{
				Source: "classifier", Claim: "", Outcome: "skipped",
				Reason:     protocol.Ptr("no_new_assistant_turn"),
				ObservedAt: "2026-07-25T10:00:02Z", RecordedAt: "2026-07-25T10:00:02Z",
			},
		},
	})

	got := out.String()
	for _, want := range []string{
		"session sess-1 (claude)",
		"state:   working",
		"applied",
		"vetoed",
		"driver_transition_filter",
		"skipped",
		"no_new_assistant_turn",
		`"screen scrape"`,
		"cause=live_signal",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	// A skip has no claim; the column must not collapse into a blank.
	if !strings.Contains(got, "-") {
		t.Fatalf("missing placeholder for the empty claim:\n%s", got)
	}
}

// A trace with nothing in it is the answer to "why is it stuck?" too — it says
// the daemon has seen no evidence at all — so it must not print an empty table.
func TestPrintStateExplainWithNoObservations(t *testing.T) {
	var out bytes.Buffer
	printStateExplain(&out, &protocol.StateExplainResult{
		SessionID: "sess-1", Agent: "claude", State: "idle", Capacity: 256,
	})
	got := out.String()
	if !strings.Contains(got, "No state observations recorded") {
		t.Fatalf("got:\n%s", got)
	}
	if strings.Contains(got, "OUTCOME") {
		t.Fatalf("should not print a header with no rows:\n%s", got)
	}
}

func TestPrintStateExplainFlagsAFullRingAsTruncated(t *testing.T) {
	observations := make([]protocol.StateExplainEntry, 3)
	for i := range observations {
		observations[i] = protocol.StateExplainEntry{
			Source: "screen", Claim: "working", Outcome: "applied",
			ObservedAt: "2026-07-25T10:00:00Z", RecordedAt: "2026-07-25T10:00:00Z",
		}
	}
	var out bytes.Buffer
	printStateExplain(&out, &protocol.StateExplainResult{
		SessionID: "sess-1", Agent: "claude", State: "working",
		Capacity: 3, Observations: observations,
	})
	if !strings.Contains(out.String(), "older ones were evicted") {
		t.Fatalf("a full ring must be reported as a tail:\n%s", out.String())
	}
}

func TestFormatStateExplainTimeFallsBackToTheRawValue(t *testing.T) {
	if got := formatStateExplainTime("not-a-time"); got != "not-a-time" {
		t.Fatalf("got %q", got)
	}
	if got := formatStateExplainTime(""); got != "-" {
		t.Fatalf("got %q", got)
	}
}
