package bus

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Retention never trims past an enabled consumer's cursor, so a consumer that
// stops consuming grows the log for as long as it lasts. These drive the
// tripwire that turns that from something you have to go and look for into
// something the system says.
//
// The clock is pinned (statusNow), because every claim here is about an age.

// pinStore seeds a log and one consumer whose cursor sits below head, with its
// oldest unread event `unread` old.
func pinStore(t *testing.T, name string, enabled bool, unread time.Duration) *memStore {
	t.Helper()
	s := newMemStore()
	// Two events already read, then three the consumer has not reached.
	for i := 0; i < 2; i++ {
		if _, err := s.Append(Event{Name: "session.state.changed", Subject: "s1"}, statusNow.Add(-48*time.Hour)); err != nil {
			t.Fatalf("append read: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Append(Event{Name: "ticket.updated", Subject: fmt.Sprintf("t%d", i)}, statusNow.Add(-unread)); err != nil {
			t.Fatalf("append unread: %v", err)
		}
	}
	if err := s.SaveConsumer(Consumer{Name: name, Cursor: 2, Enabled: enabled}, statusNow.Add(-unread)); err != nil {
		t.Fatalf("save consumer: %v", err)
	}
	return s
}

func pinBus(t *testing.T, s *memStore, age time.Duration) *Bus {
	t.Helper()
	return New(Options{Store: s, Now: func() time.Time { return statusNow }, PinAlarmAge: age})
}

func consumerStatus(t *testing.T, s Status, name string) ConsumerStatus {
	t.Helper()
	for _, c := range s.Consumers {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no consumer %q in status", name)
	return ConsumerStatus{}
}

// A consumer a little behind is the system working, and saying so every time is
// what would make the finding meaningless. Nothing is reported below the line.
func TestPinUnderTheTripwireIsSilent(t *testing.T) {
	s := pinStore(t, "notifier", true, 20*time.Minute)
	b := pinBus(t, s, time.Hour)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	c := consumerStatus(t, status, "notifier")
	if !c.HoldsRetentionFloor {
		t.Fatal("the only enabled consumer is the floor; the fixture is wrong")
	}
	if c.PinAlarm {
		t.Errorf("a 20-minute pin alarmed under a 1h tripwire")
	}
	if _, ok := findHealth(status, HealthRetentionPinned, "notifier"); ok {
		t.Errorf("a 20-minute pin produced a health finding")
	}
	pins, err := b.PinAlarms()
	if err != nil {
		t.Fatalf("pin alarms: %v", err)
	}
	if len(pins) != 0 {
		t.Errorf("PinAlarms reported %+v under the tripwire", pins)
	}
}

// Past the line the finding names what is held, how long, and the limit it
// crossed — everything needed to act on it without reading the code.
func TestPinPastTheTripwireNamesTheHoldAndTheLimit(t *testing.T) {
	s := pinStore(t, "notifier", true, 3*time.Hour)
	b := pinBus(t, s, time.Hour)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	c := consumerStatus(t, status, "notifier")
	if !c.PinAlarm {
		t.Fatal("a 3-hour pin did not cross a 1h tripwire")
	}
	if c.PinnedBytes <= 0 {
		t.Errorf("pinned bytes = %d, want the weight of the three unread events", c.PinnedBytes)
	}
	h, ok := findHealth(status, HealthRetentionPinned, "notifier")
	if !ok {
		t.Fatal("no retention_pinned health entry for an alarming pin")
	}
	if h.Level != HealthWarn {
		t.Errorf("level = %q, want warn", h.Level)
	}
	for _, want := range []string{"notifier", "3h", "1h", "3 events", "seq 2"} {
		if !strings.Contains(h.Message, want) {
			t.Errorf("message %q is missing %q", h.Message, want)
		}
	}
}

// Both surfaces read one predicate, so the page a user opens to check the alarm
// cannot disagree with the alarm about who is at fault.
func TestPinAlarmsMatchesTheSnapshot(t *testing.T) {
	s := pinStore(t, "app:ticketwatch", true, 6*time.Hour)
	b := pinBus(t, s, time.Hour)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	pins, err := b.PinAlarms()
	if err != nil {
		t.Fatalf("pin alarms: %v", err)
	}
	if len(pins) != 1 {
		t.Fatalf("pins = %+v, want exactly the one the snapshot marked", pins)
	}
	c := consumerStatus(t, status, "app:ticketwatch")
	if pins[0].Consumer != c.Name || pins[0].Cursor != c.Cursor ||
		pins[0].Events != c.Lag || pins[0].Bytes != c.PinnedBytes {
		t.Fatalf("pin %+v does not match the snapshot's consumer %+v", pins[0], c)
	}
	if pins[0].Threshold != time.Hour {
		t.Errorf("threshold = %s, want the tripwire it crossed", pins[0].Threshold)
	}
	// The finding renders identically wherever it is said.
	if h, ok := findHealth(status, HealthRetentionPinned, "app:ticketwatch"); !ok || h.Message != PinMessage(pins[0]) {
		t.Errorf("health message and PinMessage disagree:\n %q\n %q", h.Message, PinMessage(pins[0]))
	}
}

// A disabled ordinary consumer does not hold retention open — the kill switch
// is the way out of exactly this condition, so it must not keep reporting it.
func TestDisabledConsumerNeverAlarms(t *testing.T) {
	s := pinStore(t, "notifier", false, 30*24*time.Hour)
	b := pinBus(t, s, time.Hour)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if c := consumerStatus(t, status, "notifier"); c.PinAlarm {
		t.Error("a disabled consumer alarmed; disabling is the way out, not a way in")
	}
	pins, err := b.PinAlarms()
	if err != nil {
		t.Fatalf("pin alarms: %v", err)
	}
	if len(pins) != 0 {
		t.Errorf("PinAlarms reported a disabled consumer: %+v", pins)
	}
}

// Only the consumer at the floor holds the log. One further along is behind, and
// that is a different finding — reporting it here would blame the wrong one.
func TestOnlyTheFloorHolderAlarms(t *testing.T) {
	s := pinStore(t, "behind", true, 6*time.Hour)
	if err := s.SaveConsumer(Consumer{Name: "further-behind", Cursor: 0, Enabled: true}, statusNow.Add(-6*time.Hour)); err != nil {
		t.Fatalf("save second consumer: %v", err)
	}
	b := pinBus(t, s, time.Hour)

	pins, err := b.PinAlarms()
	if err != nil {
		t.Fatalf("pin alarms: %v", err)
	}
	if len(pins) != 1 || pins[0].Consumer != "further-behind" {
		t.Fatalf("pins = %+v, want only the consumer at the floor", pins)
	}
}

// A caught-up consumer is the floor and is holding nothing. Its lag is zero, so
// there is no oldest unread event and no age to be past.
func TestCaughtUpConsumerNeverAlarms(t *testing.T) {
	s := newMemStore()
	if _, err := s.Append(Event{Name: "ticket.updated", Subject: "t"}, statusNow.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.SaveConsumer(Consumer{Name: "notifier", Cursor: 1, Enabled: true}, statusNow.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("save consumer: %v", err)
	}
	b := pinBus(t, s, time.Hour)

	pins, err := b.PinAlarms()
	if err != nil {
		t.Fatalf("pin alarms: %v", err)
	}
	if len(pins) != 0 {
		t.Fatalf("a caught-up consumer alarmed: %+v", pins)
	}
}

// The escape hatch: a negative age turns the finding off everywhere, so a
// surface cannot keep marking a consumer the alarm has been told to ignore.
func TestNegativePinAlarmAgeTurnsTheFindingOff(t *testing.T) {
	s := pinStore(t, "notifier", true, 30*24*time.Hour)
	b := pinBus(t, s, -1)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if c := consumerStatus(t, status, "notifier"); c.PinAlarm {
		t.Error("the snapshot marked a consumer with the alarm turned off")
	}
	if _, ok := findHealth(status, HealthRetentionPinned, "notifier"); ok {
		t.Error("a health finding survived the alarm being turned off")
	}
	pins, err := b.PinAlarms()
	if err != nil {
		t.Fatalf("pin alarms: %v", err)
	}
	if len(pins) != 0 {
		t.Errorf("PinAlarms reported with the alarm off: %+v", pins)
	}
}

// Weighing a backlog is what the alarm's harm number is, so it has to be the
// backlog and not the log.
func TestPendingBytesWeighsOnlyTheBacklog(t *testing.T) {
	s := pinStore(t, "notifier", true, 3*time.Hour)
	whole, err := s.PendingBytes(0)
	if err != nil {
		t.Fatalf("pending bytes: %v", err)
	}
	backlog, err := s.PendingBytes(2)
	if err != nil {
		t.Fatalf("pending bytes: %v", err)
	}
	if backlog <= 0 || backlog >= whole {
		t.Fatalf("backlog above seq 2 = %d, whole log = %d; want a strict subset", backlog, whole)
	}
}

// The tripwire is resolved here, not in whichever process wants it, so the
// daemon that raises the alarm and the CLI that reads the same database cannot
// disagree about where the line is. A knob nobody can read back is a limit
// nobody can see, so every answer it gives is said out loud.
func TestPinAlarmAgeFromEnv(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want time.Duration
		says string
	}{
		{raw: "", want: DefaultPinAlarmAge},
		{raw: "90s", want: 90 * time.Second, says: "retention-pin alarm set to 1m30s"},
		{raw: "2h", want: 2 * time.Hour, says: "retention-pin alarm set to 2h"},
		// Off, and negative rather than zero: zero reads as "unset" inside New and
		// would quietly restore the default the user just turned off.
		{raw: "0", want: -1, says: "the retention-pin alarm is off"},
		{raw: "-5m", want: -1, says: "the retention-pin alarm is off"},
		// Garbage falls back loudly. Silently ignoring it would leave the user
		// believing a tripwire they set is in force.
		{raw: "soon", want: DefaultPinAlarmAge, says: "is not a duration"},
	} {
		t.Run(fmt.Sprintf("%q", tc.raw), func(t *testing.T) {
			if tc.raw == "" {
				t.Setenv(PinAlarmAgeEnv, "")
			} else {
				t.Setenv(PinAlarmAgeEnv, tc.raw)
			}
			var said []string
			got := PinAlarmAgeFromEnv(func(format string, args ...interface{}) {
				said = append(said, fmt.Sprintf(format, args...))
			})
			if got != tc.want {
				t.Errorf("%q = %s, want %s", tc.raw, got, tc.want)
			}
			whole := strings.Join(said, "\n")
			if tc.says == "" {
				if whole != "" {
					t.Errorf("an unset knob said %q; a default nobody asked to change is not news", whole)
				}
				return
			}
			if !strings.Contains(whole, tc.says) {
				t.Errorf("said %q, want it to carry %q", whole, tc.says)
			}
		})
	}
}

// A bus built with the resolved value keeps it, including the off switch — the
// path every caller actually takes.
func TestNewCarriesAResolvedPinAlarmAge(t *testing.T) {
	t.Setenv(PinAlarmAgeEnv, "0")
	b := New(Options{PinAlarmAge: PinAlarmAgeFromEnv(nil)})
	if b.pinAlarmAge >= 0 {
		t.Errorf("a bus built with the off switch has pinAlarmAge %s, want it negative", b.pinAlarmAge)
	}
}

// The tripwire is printed so a reader can check the value they set against the
// one in force. Rounding it away defeats that; rounding the observed age does not.
func TestTheMessageNamesTheTripwireExactly(t *testing.T) {
	for _, tc := range []struct {
		threshold time.Duration
		want      string
	}{
		{threshold: time.Hour, want: "past the 1h tripwire"},
		{threshold: 90 * time.Second, want: "past the 1m30s tripwire"},
		{threshold: 45 * time.Second, want: "past the 45s tripwire"},
		{threshold: 90 * time.Minute, want: "past the 1h30m tripwire"},
	} {
		got := PinMessage(Pin{Consumer: "app:x", Age: 11 * time.Minute, Threshold: tc.threshold})
		if !strings.Contains(got, tc.want) {
			t.Errorf("a %s tripwire reads %q, want it to carry %q", tc.threshold, got, tc.want)
		}
	}
}

// The other window an operator can move, and the one with no other way to be
// seen: thirty days is longer than any run anyone watches, so a trim against a
// database younger than that removes nothing whatever the cursor floor says.
// Without this knob, "resumes below the oldest surviving fact" is a state the
// product can only reach in a unit test.
func TestRetentionFromEnv(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want time.Duration
		says string
	}{
		{raw: "", want: DefaultRetention},
		{raw: "1s", want: time.Second, says: "retention window set to 1s"},
		{raw: "5m", want: 5 * time.Minute, says: "retention window set to 5m"},
		// Retention is a window, not a finding, so there is nothing for zero or a
		// negative to mean. Both fall back loudly rather than turning trim into a
		// pass that deletes everything below the floor.
		{raw: "0", want: DefaultRetention, says: "is not a positive window"},
		{raw: "-5m", want: DefaultRetention, says: "is not a positive window"},
		{raw: "soon", want: DefaultRetention, says: "is not a duration"},
	} {
		t.Run(fmt.Sprintf("%q", tc.raw), func(t *testing.T) {
			t.Setenv(RetentionEnv, tc.raw)
			var said []string
			got := RetentionFromEnv(func(format string, args ...interface{}) {
				said = append(said, fmt.Sprintf(format, args...))
			})
			if got != tc.want {
				t.Errorf("%q = %s, want %s", tc.raw, got, tc.want)
			}
			whole := strings.Join(said, "\n")
			if tc.says == "" {
				if whole != "" {
					t.Errorf("an unset knob said %q; a default nobody asked to change is not news", whole)
				}
				return
			}
			if !strings.Contains(whole, tc.says) {
				t.Errorf("said %q, want it to carry %q", whole, tc.says)
			}
		})
	}
}

// What the knob is for. The window is what decides whether a trim can remove
// anything at all, and at the shipped default a fresh log is untouchable — which
// is the whole reason a consumer resuming below `earliest` had no way to be
// produced outside a unit test.
func TestAMovedRetentionWindowIsWhatLetsATrimRemoveAnything(t *testing.T) {
	seed := func(t *testing.T, b *Bus, s Store) {
		t.Helper()
		for _, name := range []string{"a.happened", "b.happened"} {
			if _, err := b.Publish(name, "subject-"+name, nil); err != nil {
				t.Fatalf("Publish(%s): %v", name, err)
			}
		}
		// A consumer at head, so the cursor floor is not what decides this.
		_, head, err := s.Bounds()
		if err != nil {
			t.Fatalf("Bounds: %v", err)
		}
		if err := s.SaveConsumer(Consumer{Name: "reader", Cursor: head, Enabled: true}, time.Now()); err != nil {
			t.Fatalf("SaveConsumer: %v", err)
		}
	}

	t.Run("at the shipped default nothing is old enough", func(t *testing.T) {
		s := newMemStore()
		b := New(Options{Store: s, Retention: DefaultRetention})
		seed(t, b, s)
		if n, err := b.Trim(); err != nil || n != 0 {
			t.Fatalf("trimmed %d event(s) from a fresh log at the 30-day window, want 0 (err=%v)", n, err)
		}
	})

	t.Run("moved to a window a run can cross", func(t *testing.T) {
		s := newMemStore()
		b := New(Options{Store: s, Retention: time.Nanosecond})
		seed(t, b, s)
		if n, err := b.Trim(); err != nil || n != 2 {
			t.Fatalf("trimmed %d event(s) with the window moved, want 2 (err=%v)", n, err)
		}
	})
}
