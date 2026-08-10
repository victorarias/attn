package bus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// Status is the one computation two surfaces render, and the tripwire is the
// half nobody has to be looking at. Both are driven here on a fixed clock: a
// rate is a claim about a window, and a test that cannot place events inside
// one is not testing the claim.

var statusNow = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

// statusBus builds a bus over a memStore with the clock pinned, so "the last
// hour" is a place events can be put rather than a race with the wall.
func statusBus(t *testing.T) (*Bus, *memStore) {
	t.Helper()
	s := newMemStore()
	b := New(Options{Store: s, Now: func() time.Time { return statusNow }})
	return b, s
}

// publishAt appends n events of a class at a fixed age, spread across subjects.
func publishAt(t *testing.T, s *memStore, name string, subjects, n int, ago time.Duration) {
	t.Helper()
	for i := 0; i < n; i++ {
		subject := fmt.Sprintf("%s_%d", name, i%subjects)
		if _, err := s.Append(Event{
			Name: name, Subject: subject, Source: "test",
		}, statusNow.Add(-ago)); err != nil {
			t.Fatalf("append %s: %v", name, err)
		}
	}
}

func producer(t *testing.T, s Status, name string) Producer {
	t.Helper()
	for _, p := range s.Producers {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no producer %q in status", name)
	return Producer{}
}

func findHealth(s Status, kind, subject string) (Health, bool) {
	for _, h := range s.Health {
		if h.Kind == kind && h.Subject == subject {
			return h, true
		}
	}
	return Health{}, false
}

func TestStatusReportsProducerRatesAndShare(t *testing.T) {
	b, s := statusBus(t)
	publishAt(t, s, "session.state.changed", 3, 60, 30*time.Minute)
	publishAt(t, s, "session.state.changed", 3, 120, 5*time.Hour)
	publishAt(t, s, "pr.updated", 20, 20, 2*time.Hour)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Rows != 200 {
		t.Fatalf("rows = %d, want 200", status.Rows)
	}

	loud := producer(t, status, "session.state.changed")
	// 60 events inside a 1h window is 60/hour; 180 inside 24h is 7.5/hour.
	if loud.RecentPerHour != 60 {
		t.Errorf("recent rate = %v, want 60", loud.RecentPerHour)
	}
	if loud.BaselinePerHour != 7.5 {
		t.Errorf("baseline rate = %v, want 7.5", loud.BaselinePerHour)
	}
	if want := 180.0 / 200.0; loud.Share != want {
		t.Errorf("share = %v, want %v", loud.Share, want)
	}
	if loud.Subjects != 3 {
		t.Errorf("subjects = %d, want 3", loud.Subjects)
	}
}

// The tripwire is an absolute ceiling on a sustained window, and it must not
// fire on a producer that is merely busy right now.
func TestStatusDoesNotCallABurstySmallProducerLoud(t *testing.T) {
	b, s := statusBus(t)
	// 900 events in the last 10 minutes: a 5400/hour instantaneous rate, far
	// past the ceiling — but only 900 across either sustained window.
	publishAt(t, s, "pr.updated", 40, 900, 10*time.Minute)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	p := producer(t, status, "pr.updated")
	if p.Surging {
		t.Errorf("a burst inside one window is not a sustained rate; surge = %v/h over %s",
			p.SurgePerHour, p.SurgeWindow)
	}
	if _, ok := findHealth(status, HealthProducerSurging, "pr.updated"); ok {
		t.Error("a burst must not raise a health warning")
	}
}

// Onset: a class crossing the ceiling on the 6h window is caught within hours,
// which is the whole point — the bug this exists for ran for a week.
func TestStatusCatchesASurgeOnTheSustainedWindow(t *testing.T) {
	b, s := statusBus(t)
	// 6001 events spread over the last 6 hours: just past 1000/hour.
	for i := 0; i < 6001; i++ {
		publishAt(t, s, "session.state.changed", 2, 1, time.Duration(i%360)*time.Minute)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	p := producer(t, status, "session.state.changed")
	if !p.Surging {
		t.Fatalf("sustained %v/h did not trip the %v/h ceiling", p.SustainedPerHour, SurgeRatePerHour)
	}
	h, ok := findHealth(status, HealthProducerSurging, "session.state.changed")
	if !ok {
		t.Fatal("a surging producer must raise a health warning")
	}
	// The line has to be actionable on its own: the number, the ceiling it
	// crossed, and the window it was measured over.
	for _, want := range []string{"session.state.changed", "1000/hour", "events/hour"} {
		if !strings.Contains(h.Message, want) {
			t.Errorf("message %q is missing %q", h.Message, want)
		}
	}
}

// Standing loudness: an evening lull drops the 6h rate below the line while the
// producer still owns most of the log. That is the state production was in.
func TestStatusCatchesAStandingLoudProducerOnTheBaselineWindow(t *testing.T) {
	b, s := statusBus(t)
	// Quiet for the last 6 hours, very loud in the 18 before that.
	publishAt(t, s, "session.state.changed", 4, 200, 3*time.Hour)
	for i := 0; i < 24000; i++ {
		publishAt(t, s, "session.state.changed", 4, 1, 7*time.Hour+time.Duration(i%600)*time.Minute)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	p := producer(t, status, "session.state.changed")
	if p.SustainedPerHour >= SurgeRatePerHour {
		t.Fatalf("this case needs a quiet 6h window; sustained = %v/h", p.SustainedPerHour)
	}
	if !p.Surging {
		t.Fatalf("a producer at %v/h over 24h must still be called loud", p.BaselinePerHour)
	}
	if p.SurgeWindow != BaselineWindow {
		t.Errorf("surge window = %s, want the 24h window that tripped", p.SurgeWindow)
	}
}

// ReportLoudProducers is the surface nobody has to be looking at. It says
// nothing when everything is fine — a tripwire working code never feels.
func TestReportLoudProducersIsSilentOnAHealthyLog(t *testing.T) {
	s := newMemStore()
	var lines []string
	var mu sync.Mutex
	b := New(Options{
		Store: s,
		Now:   func() time.Time { return statusNow },
		Log: func(format string, args ...interface{}) {
			mu.Lock()
			lines = append(lines, fmt.Sprintf(format, args...))
			mu.Unlock()
		},
	})
	publishAt(t, s, "pr.updated", 30, 400, 3*time.Hour)

	b.ReportLoudProducers()
	if len(lines) != 0 {
		t.Fatalf("want silence on a healthy log, got %q", lines)
	}
}

func TestReportLoudProducersNamesTheFactRateAndWindow(t *testing.T) {
	s := newMemStore()
	var lines []string
	var mu sync.Mutex
	b := New(Options{
		Store: s,
		Now:   func() time.Time { return statusNow },
		Log: func(format string, args ...interface{}) {
			mu.Lock()
			lines = append(lines, fmt.Sprintf(format, args...))
			mu.Unlock()
		},
	})
	for i := 0; i < 7000; i++ {
		publishAt(t, s, "session.state.changed", 2, 1, time.Duration(i%360)*time.Minute)
	}

	b.ReportLoudProducers()
	if len(lines) != 1 {
		t.Fatalf("want exactly one line for one loud class, got %q", lines)
	}
	for _, want := range []string{"session.state.changed", "1000/hour", "% of the log"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("line %q is missing %q", lines[0], want)
		}
	}
}

func TestStatusReportsConsumerLagAndTheRetentionFloor(t *testing.T) {
	b, s := statusBus(t)
	publishAt(t, s, "session.state.changed", 4, 100, 3*time.Hour)

	if err := s.SaveConsumer(Consumer{Name: "behind", Cursor: 10, Enabled: true}, statusNow); err != nil {
		t.Fatalf("SaveConsumer: %v", err)
	}
	if err := s.SaveConsumer(Consumer{Name: "ahead", Cursor: 90, Enabled: true}, statusNow); err != nil {
		t.Fatalf("SaveConsumer: %v", err)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	var behind, ahead ConsumerStatus
	for _, c := range status.Consumers {
		switch c.Name {
		case "behind":
			behind = c
		case "ahead":
			ahead = c
		}
	}
	if behind.Lag != 90 {
		t.Errorf("behind lag = %d, want 90", behind.Lag)
	}
	if !behind.HoldsRetentionFloor {
		t.Error("the lowest enabled cursor holds the retention floor")
	}
	if ahead.HoldsRetentionFloor {
		t.Error("only one consumer holds the floor, and it is the lowest")
	}
	// Lag counts events; this says how long the oldest has waited.
	if behind.OldestUnreadAt.IsZero() {
		t.Error("a consumer with lag must report the age of its oldest unread event")
	}
	if !ahead.OldestUnreadAt.IsZero() && ahead.Lag == 0 {
		t.Error("a caught-up consumer has no oldest unread event")
	}
}

// The oldest UNREAD event is the one after the cursor, not the one at it. The
// cursor names what was already handled, so reporting its stamp would age the
// backlog by one event and read as "waiting" when the consumer is caught up.
func TestStatusDatesTheOldestUnreadEventFromAfterTheCursor(t *testing.T) {
	b, s := statusBus(t)
	// Distinct ages, so an off-by-one is visible rather than absorbed.
	publishAt(t, s, "pr.updated", 1, 1, 9*time.Hour) // seq 1
	publishAt(t, s, "pr.updated", 1, 1, 5*time.Hour) // seq 2
	publishAt(t, s, "pr.updated", 1, 1, 1*time.Hour) // seq 3

	if err := s.SaveConsumer(Consumer{Name: "reader", Cursor: 1, Enabled: true}, statusNow); err != nil {
		t.Fatalf("SaveConsumer: %v", err)
	}
	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	got := status.Consumers[0].OldestUnreadAt
	want := statusNow.Add(-5 * time.Hour)
	if !got.Equal(want) {
		t.Fatalf("oldest unread = %s, want seq 2 at %s (seq 1 is already read)", got, want)
	}

	// And a consumer at the head is waiting on nothing.
	if err := s.SetCursor("reader", 3, statusNow); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	status, err = b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Consumers[0].OldestUnreadAt.IsZero() {
		t.Errorf("a caught-up consumer reported an oldest unread event at %s",
			status.Consumers[0].OldestUnreadAt)
	}
}

// A disabled consumer is a deliberate state, but it is also the state that
// silently stops work. The user must be told, not left to read a false column.
func TestStatusWarnsAboutADisabledConsumer(t *testing.T) {
	b, s := statusBus(t)
	publishAt(t, s, "pr.updated", 4, 10, time.Hour)
	if err := s.SaveConsumer(Consumer{Name: "killed", Cursor: 2, Enabled: false}, statusNow); err != nil {
		t.Fatalf("SaveConsumer: %v", err)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	h, ok := findHealth(status, HealthConsumerDisabled, "killed")
	if !ok {
		t.Fatal("a disabled consumer must raise a warning")
	}
	if !strings.Contains(h.Message, "killed") || !strings.Contains(h.Message, "disabled") {
		t.Errorf("message %q must name the consumer and its state", h.Message)
	}
	// A disabled consumer does not pin the log, exactly as trimming treats it.
	for _, c := range status.Consumers {
		if c.Name == "killed" && c.HoldsRetentionFloor {
			t.Error("a disabled consumer must not be shown holding the retention floor")
		}
	}
}

// "41,000 events behind and not advancing" is the sentence the brief asked for:
// a lag that is not moving is different from a lag that is being worked through.
func TestStatusSaysWhenAnEnabledConsumerStopsAdvancing(t *testing.T) {
	b, s := statusBus(t)
	publishAt(t, s, "session.state.changed", 4, 100, 3*time.Hour)
	if err := s.SaveConsumer(Consumer{Name: "stuck", Cursor: 1, Enabled: true}, statusNow.Add(-StallAge)); err != nil {
		t.Fatalf("SaveConsumer: %v", err)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	h, ok := findHealth(status, HealthConsumerLagging, "stuck")
	if !ok {
		t.Fatal("a consumer whose cursor stopped moving while behind must be reported")
	}
	if h.Level != HealthError {
		t.Errorf("level = %q, want %q", h.Level, HealthError)
	}
	for _, want := range []string{"stuck", "99 events", "not advancing"} {
		if !strings.Contains(h.Message, want) {
			t.Errorf("message %q is missing %q", h.Message, want)
		}
	}
}

// A consumer that is behind but visibly catching up is the system working.
func TestStatusDoesNotCallAMovingConsumerStalled(t *testing.T) {
	b, s := statusBus(t)
	publishAt(t, s, "session.state.changed", 4, 100, 3*time.Hour)
	if err := s.SaveConsumer(Consumer{Name: "catching-up", Cursor: 1, Enabled: true}, statusNow.Add(-time.Second)); err != nil {
		t.Fatalf("SaveConsumer: %v", err)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if h, ok := findHealth(status, HealthConsumerLagging, "catching-up"); ok {
		t.Errorf("a consumer that just advanced is not stalled: %q", h.Message)
	}
}

// Live and Stalled only mean something when the snapshot came from the process
// that owns delivery. `attn bus status` reads the database from outside it, and
// must not therefore accuse every consumer of not running.
func TestStatusOnlyClaimsDeliveryKnowledgeWhenItHasIt(t *testing.T) {
	b, s := statusBus(t)
	publishAt(t, s, "pr.updated", 2, 5, time.Hour)
	if err := s.SaveConsumer(Consumer{Name: "elsewhere", Cursor: 5, Enabled: true}, statusNow); err != nil {
		t.Fatalf("SaveConsumer: %v", err)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Delivering {
		t.Error("a bus with no registered consumers is not delivering")
	}
	if _, ok := findHealth(status, HealthConsumerNotLive, "elsewhere"); ok {
		t.Error("a reader outside the daemon cannot know a consumer is not running")
	}

	// The same registration, seen by a bus that does own delivery loops.
	running, _ := statusBus(t)
	running.store = s
	if err := running.Register("mine", All, func(context.Context, Event) error { return nil }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := running.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(running.Stop)

	status, err = running.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Delivering {
		t.Fatal("a started bus with a registered consumer is delivering")
	}
	if _, ok := findHealth(status, HealthConsumerNotLive, "elsewhere"); !ok {
		t.Error("a daemon that owns delivery must report a registration nothing is reading")
	}
}

func TestStatusOnAnEmptyLog(t *testing.T) {
	b, _ := statusBus(t)
	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Rows != 0 || len(status.Producers) != 0 || len(status.Health) != 0 {
		t.Fatalf("empty log must report nothing: %+v", status)
	}
	if !status.OldestAt.IsZero() {
		t.Error("an empty log has no oldest event")
	}
}

// A store that cannot answer must fail the snapshot rather than render a
// confident picture of a log it could not read.
func TestStatusFailsWhenTheLogCannotBeRead(t *testing.T) {
	b, s := statusBus(t)
	s.setBoundsErr(errors.New("disk gone"))
	if _, err := b.Status(); err == nil {
		t.Fatal("want an error when the log's bounds cannot be read")
	}
}
