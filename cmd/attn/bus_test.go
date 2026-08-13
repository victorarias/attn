package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bus"
)

// The human rendering is the whole product for anyone on a terminal, and it is
// the one place that decides what to leave out. A cap nobody can see is the
// failure mode: these check that what is dropped is still counted out loud, and
// that the JSON drops nothing at all.

var busRenderNow = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

func renderBusStatus(s bus.Status) string {
	var buf bytes.Buffer
	writeBusStatus(&buf, s, busRenderNow)
	return buf.String()
}

func busStatusFixture(producers ...bus.Producer) bus.Status {
	s := bus.Status{
		Earliest:        1,
		Head:            1000,
		Rows:            1000,
		Bytes:           64_000,
		OldestAt:        busRenderNow.Add(-8 * 24 * time.Hour),
		NewestAt:        busRenderNow,
		RetentionWindow: 30 * 24 * time.Hour,
		Producers:       producers,
	}
	return s
}

func TestBusStatusRenderingCountsWhatItLeavesOut(t *testing.T) {
	producers := []bus.Producer{{
		Name: "session.state.changed", Events: 800, Share: 0.8, Subjects: 4,
		RecentPerHour: 40, BaselinePerHour: 33,
	}}
	// One more class than the table shows, each holding 10 events.
	for i := 0; i < busProducerLines; i++ {
		producers = append(producers, bus.Producer{
			Name: fmt.Sprintf("quiet.%d", i), Events: 10, Share: 0.01, Subjects: 2,
		})
	}
	out := renderBusStatus(busStatusFixture(producers...))

	if strings.Contains(out, "quiet.14") {
		t.Fatalf("the table should stop at %d rows:\n%s", busProducerLines, out)
	}
	// A silent truncation reads as "that is everything". The tail has to be
	// counted, with the share it accounts for and the way to see it.
	for _, want := range []string{"and 1 quieter class", "10 event", "--json"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not state what it dropped (%q missing):\n%s", want, out)
		}
	}
}

func TestBusStatusRenderingSaysNothingAboutATailThatIsNotThere(t *testing.T) {
	out := renderBusStatus(busStatusFixture(bus.Producer{
		Name: "pr.updated", Events: 1000, Share: 1, Subjects: 100,
	}))

	if strings.Contains(out, "quieter class") {
		t.Errorf("a fully shown table must not claim a hidden tail:\n%s", out)
	}
}

func TestBusStatusRenderingMarksALoudProducerAndPrintsTheFindings(t *testing.T) {
	s := busStatusFixture(
		bus.Producer{
			Name: "session.state.changed", Events: 900, Share: 0.9, Subjects: 4,
			RecentPerHour: 1800, BaselinePerHour: 1892, SustainedPerHour: 680,
			Surging: true, SurgeWindow: bus.BaselineWindow, SurgePerHour: 1892,
		},
		bus.Producer{Name: "pr.updated", Events: 100, Share: 0.1, Subjects: 50},
	)
	s.Health = []bus.Health{
		{Level: bus.HealthError, Kind: bus.HealthConsumerLagging, Subject: "notifier",
			Message: "consumer notifier is 41,141 events behind and not advancing"},
		{Level: bus.HealthWarn, Kind: bus.HealthProducerSurging, Subject: "session.state.changed",
			Message: "producer session.state.changed is publishing 1892 events/hour"},
	}
	out := renderBusStatus(s)

	loud := producerRow(t, out, "session.state.changed")
	if !strings.Contains(loud, "!") {
		t.Errorf("a loud producer is not marked in the table: %q", loud)
	}
	if quiet := producerRow(t, out, "pr.updated"); strings.Contains(quiet, "!") {
		t.Errorf("a quiet producer must not be marked: %q", quiet)
	}
	// The findings are the daemon's sentences, printed as-is and led by severity.
	if !strings.Contains(out, "ERROR: consumer notifier is 41,141 events behind") {
		t.Errorf("the lagging finding is missing:\n%s", out)
	}
	if !strings.Contains(out, "WARN: producer session.state.changed is publishing") {
		t.Errorf("the surging finding is missing:\n%s", out)
	}
}

// An empty log is what a fresh install has, and the command opens on it.
func TestBusStatusRenderingOnAnEmptyLog(t *testing.T) {
	out := renderBusStatus(bus.Status{RetentionWindow: 30 * 24 * time.Hour})
	if out == "" {
		t.Fatal("an empty log must still be described")
	}
	if strings.Contains(out, "ERROR") || strings.Contains(out, "WARN") {
		t.Errorf("nothing is wrong with an empty log:\n%s", out)
	}
}

// --json is the scripting surface, so it carries every window a decision could
// be made on — including which one tripped, which the human view only says in
// prose.
func TestBusStatusJSONCarriesBothSustainedWindows(t *testing.T) {
	report := busStatusReport(busStatusFixture(bus.Producer{
		Name: "session.state.changed", Events: 1000, Share: 1, Subjects: 4,
		RecentPerHour: 1800, BaselinePerHour: 1892, SustainedPerHour: 680,
		Surging: true, SurgeWindow: bus.BaselineWindow, SurgePerHour: 1892,
	}))
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Producers []struct {
			SustainedPerHour   float64 `json:"sustained_per_hour"`
			SurgePerHour       float64 `json:"surge_per_hour"`
			SurgeWindowSeconds float64 `json:"surge_window_seconds"`
			Surging            bool    `json:"surging"`
		} `json:"producers"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Producers) != 1 {
		t.Fatalf("producers = %d, want 1", len(decoded.Producers))
	}
	p := decoded.Producers[0]
	if p.SustainedPerHour != 680 {
		t.Errorf("sustained_per_hour = %v, want the 6h rate 680", p.SustainedPerHour)
	}
	if p.SurgeWindowSeconds != bus.BaselineWindow.Seconds() {
		t.Errorf("surge_window_seconds = %v, want the 24h window that tripped", p.SurgeWindowSeconds)
	}
	if p.SurgePerHour != 1892 || !p.Surging {
		t.Errorf("surge = %v/h surging=%v, want 1892 and true", p.SurgePerHour, p.Surging)
	}
}

// The table row for a fact, matched on the name starting the line so the marker
// column and the health sentences below cannot stand in for it.
func producerRow(t *testing.T, out, name string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name) {
			return line
		}
	}
	t.Fatalf("no table row for %q in:\n%s", name, out)
	return ""
}

// Holding the retention floor is normal and holding it past the tripwire is an
// outage. The table has to read differently for the two, or someone acting on
// the warning opens the CLI and cannot tell which consumer it meant.
func TestBusStatusRenderingSeparatesANormalFloorFromAnAlarmingOne(t *testing.T) {
	s := busStatusFixture()
	s.PinAlarmAge = time.Hour
	s.Consumers = []bus.ConsumerStatus{
		{Name: "ordinary", Cursor: 990, Lag: 10, Enabled: true, HoldsRetentionFloor: true},
		{
			Name: "app:ticketwatch", Cursor: 120, Lag: 880, Enabled: true,
			HoldsRetentionFloor: true, PinAlarm: true, PinnedBytes: 31_000,
			OldestUnreadAt: busRenderNow.Add(-3 * time.Hour),
		},
	}

	out := renderBusStatus(s)
	if !strings.Contains(out, "ordinary (retention floor)") {
		t.Errorf("a healthy floor holder lost its tag:\n%s", out)
	}
	if !strings.Contains(out, "app:ticketwatch (PINNING 30.3 KB)") {
		t.Errorf("an alarming pin is not marked, or does not say what it holds:\n%s", out)
	}
}

// A script acting on the crossing needs the limit beside the value, and needs to
// tell "holding nothing" from "not measured" — which is what the flag is for.
func TestBusStatusJSONCarriesTheTripwireAndWhatIsHeld(t *testing.T) {
	s := busStatusFixture()
	s.PinAlarmAge = 90 * time.Minute
	s.Consumers = []bus.ConsumerStatus{{
		Name: "app:ticketwatch", Cursor: 120, Lag: 880, Enabled: true,
		HoldsRetentionFloor: true, PinAlarm: true, PinnedBytes: 31_000,
	}}

	raw, err := json.Marshal(busStatusReport(s))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		PinAlarmSeconds float64 `json:"pin_alarm_seconds"`
		Consumers       []struct {
			PinAlarm    bool  `json:"pin_alarm"`
			PinnedBytes int64 `json:"pinned_bytes"`
		} `json:"consumers"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.PinAlarmSeconds != 5400 {
		t.Errorf("pin_alarm_seconds = %v, want the tripwire in effect", got.PinAlarmSeconds)
	}
	if len(got.Consumers) != 1 || !got.Consumers[0].PinAlarm || got.Consumers[0].PinnedBytes != 31_000 {
		t.Errorf("consumer report = %+v", got.Consumers)
	}
}
