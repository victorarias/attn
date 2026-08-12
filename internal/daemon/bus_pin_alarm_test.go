package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/store"
)

// The alarm's whole job is to be believed. A durable row per tick for a
// condition that can last days would teach the user to ignore the feed, and a
// row that never comes is the state this exists to end. These pin the rule
// between the two: once per episode, and a new episode may speak again.

func pinAlarmDaemon() *Daemon {
	return &Daemon{store: store.New()}
}

func samplePin(consumer string, cursor int64) bus.Pin {
	return bus.Pin{
		Consumer:       consumer,
		Cursor:         cursor,
		Events:         412,
		Bytes:          31_000,
		OldestUnreadAt: time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC),
		Age:            3 * time.Hour,
		Threshold:      time.Hour,
	}
}

func unread(t *testing.T, d *Daemon) []store.NotificationRecord {
	t.Helper()
	list, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	return list
}

// One look cannot tell an outage from a machine that just woke up: on wake the
// oldest unread event is as old as the sleep and the delivery loop has not
// polled yet. Two looks a whole interval apart can, because by then the cursor
// has moved. So the first sighting never notifies.
func TestFirstSightingOfAPinDoesNotNotify(t *testing.T) {
	d := pinAlarmDaemon()

	if n := d.recordBusPins([]bus.Pin{samplePin("app:ticketwatch", 900)}); n != 0 {
		t.Fatalf("notified %d on the first sighting, want 0", n)
	}
	if list := unread(t, d); len(list) != 0 {
		t.Fatalf("wrote %d notification(s) on the first sighting", len(list))
	}
}

// Confirmed at the same cursor, it is said once — and stays said once however
// long the condition lasts.
func TestConfirmedPinNotifiesExactlyOnce(t *testing.T) {
	d := pinAlarmDaemon()
	pin := samplePin("app:ticketwatch", 900)

	d.recordBusPins([]bus.Pin{pin})
	if n := d.recordBusPins([]bus.Pin{pin}); n != 1 {
		t.Fatalf("notified %d on the confirming sighting, want 1", n)
	}
	// The condition holds for another ten checks; the feed does not grow.
	for i := 0; i < 10; i++ {
		if n := d.recordBusPins([]bus.Pin{pin}); n != 0 {
			t.Fatalf("re-notified while the same episode was held (check %d)", i+2)
		}
	}
	list := unread(t, d)
	if len(list) != 1 {
		t.Fatalf("wrote %d notification(s) for one episode, want 1", len(list))
	}
	if list[0].Severity != store.NotificationWarning {
		t.Errorf("severity = %q, want warning: the log keeps every pinned event, nothing is lost", list[0].Severity)
	}
	if list[0].SourceKind != "bus_consumer" || list[0].SourceID != "app:ticketwatch" {
		t.Errorf("source = %s/%s, want the consumer it is about", list[0].SourceKind, list[0].SourceID)
	}
}

// A cleared condition ends the episode. When it comes back it is a new outage
// and the user has to hear about it — silence here is how the second one goes
// unnoticed.
func TestClearedThenRecrossedPinNotifiesAgain(t *testing.T) {
	d := pinAlarmDaemon()
	first := samplePin("app:ticketwatch", 900)

	d.recordBusPins([]bus.Pin{first})
	d.recordBusPins([]bus.Pin{first})
	// The consumer caught up: nothing is pinning.
	if n := d.recordBusPins(nil); n != 0 {
		t.Fatalf("notified on a cleared condition")
	}
	// It stalls again, further along the log.
	second := samplePin("app:ticketwatch", 4200)
	if n := d.recordBusPins([]bus.Pin{second}); n != 0 {
		t.Fatalf("a new episode notified on its first sighting")
	}
	if n := d.recordBusPins([]bus.Pin{second}); n != 1 {
		t.Fatalf("notified %d for the second episode, want 1", n)
	}
	if list := unread(t, d); len(list) != 2 {
		t.Fatalf("wrote %d notification(s) for two episodes, want 2", len(list))
	}
}

// A cursor that moved is a consumer that read something: whatever was wrong is
// over, even if it is stuck again by the next check. Confirmation is against the
// same position, never merely against the same name.
func TestMovedCursorRestartsTheConfirmation(t *testing.T) {
	d := pinAlarmDaemon()

	d.recordBusPins([]bus.Pin{samplePin("notifier", 100)})
	if n := d.recordBusPins([]bus.Pin{samplePin("notifier", 140)}); n != 0 {
		t.Fatalf("a moved cursor was treated as a confirmation of the old position")
	}
	if n := d.recordBusPins([]bus.Pin{samplePin("notifier", 140)}); n != 1 {
		t.Fatalf("the new position was never confirmed")
	}
}

// Two consumers can be stuck at once, and each is its own episode.
func TestEpisodesAreTrackedPerConsumer(t *testing.T) {
	d := pinAlarmDaemon()
	a, b := samplePin("app:one", 10), samplePin("app:two", 20)

	d.recordBusPins([]bus.Pin{a})
	// b appears one check later, so it is not confirmed when a is.
	if n := d.recordBusPins([]bus.Pin{a, b}); n != 1 {
		t.Fatalf("notified %d, want only the confirmed one", n)
	}
	if n := d.recordBusPins([]bus.Pin{a, b}); n != 1 {
		t.Fatalf("notified %d, want the second one now that it is confirmed", n)
	}
}

// The user reads a notification and an agent reads an error; the same text has
// to let either act. It names what is held, the tripwire it crossed, and a
// command that ends it.
func TestPinNotificationCarriesTheFindingAndTheWayOut(t *testing.T) {
	body := busPinNotificationBody(samplePin("app:ticketwatch", 900))

	for _, want := range []string{
		"app:ticketwatch", // who
		"3h",              // how long it has held
		"1h",              // the tripwire it crossed
		"seq 900",         // where the floor is stuck
		"412 events",      // what is held
		"30.3 KB",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q:\n%s", want, body)
		}
	}
	// An app consumer gets the app way out, including the parked-runtime case
	// that is the usual cause.
	for _, want := range []string{"attn app status ticketwatch", "attn app runtime restart", "attn bus disable app:ticketwatch"} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing the way out %q:\n%s", want, body)
		}
	}
}

// The advice is chosen from the consumer's name, so a consumer that is not an
// app is never told to restart the app runtime.
func TestNonAppPinNotificationOffersTheBusWayOut(t *testing.T) {
	body := busPinNotificationBody(samplePin("notifier", 12))

	if strings.Contains(body, "app runtime") {
		t.Errorf("a non-app consumer was told to restart the app runtime:\n%s", body)
	}
	for _, want := range []string{"attn bus status", "attn bus disable notifier"} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q:\n%s", want, body)
		}
	}
}

// A check less frequent than a quarter of its own tripwire reports an outage
// long after the tripwire named it; one that runs every second buys nothing on
// a condition measured in hours.
func TestPinAlarmIntervalTracksTheTripwire(t *testing.T) {
	for _, tc := range []struct {
		age  time.Duration
		want time.Duration
	}{
		{bus.DefaultPinAlarmAge, 15 * time.Minute},
		{4 * time.Hour, time.Hour},
		{2 * time.Minute, time.Minute},  // floored
		{10 * time.Second, time.Minute}, // floored
	} {
		if got := busPinAlarmInterval(tc.age); got != tc.want {
			t.Errorf("interval for a %s tripwire = %s, want %s", tc.age, got, tc.want)
		}
	}
}

// The daemon wires the tripwire in two places — the bus it builds and the
// recurring check that watches it — and they have to be the same number. Reading
// it once is also what keeps the log from announcing the limit twice at every
// boot. (Where the number itself comes from is internal/bus's own test.)
func TestTheDaemonResolvesTheTripwireOnce(t *testing.T) {
	t.Setenv(bus.PinAlarmAgeEnv, "90s")
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	logger, err := logging.New(logPath)
	if err != nil {
		t.Fatalf("new test logger: %v", err)
	}
	defer logger.Close()
	d := &Daemon{logger: logger}

	first := d.busPinAlarmAge()
	if first != 90*time.Second {
		t.Fatalf("tripwire = %s, want the 90s the environment asked for", first)
	}
	if got := d.busPinAlarmAge(); got != first {
		t.Errorf("second read = %s, want the same %s the bus was built with", got, first)
	}
	written, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read the daemon log: %v", err)
	}
	if got := strings.Count(string(written), "retention-pin alarm set to"); got != 1 {
		t.Errorf("the tripwire was announced %d times, want once:\n%s", got, written)
	}
}

// The wiring, end to end on a real store and a real bus: a consumer row whose
// cursor sits under an aged log becomes a durable warning after the confirming
// check, and nothing before it. Every piece above this tests one link; this is
// the one that fails when the handler is reading the wrong bus.
func TestPinAlarmHandlerNotifiesFromARealLog(t *testing.T) {
	t.Setenv(bus.PinAlarmAgeEnv, "1h")
	d := &Daemon{store: store.New()}
	d.ensureEventBus()

	old := time.Now().Add(-3 * time.Hour)
	for i := 0; i < 4; i++ {
		if _, err := d.store.AppendBusEvent(store.BusEvent{
			Name: "session.state.changed", Subject: "s1", Payload: `{"state":"idle"}`,
		}, old); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := d.store.SaveBusConsumer(store.BusConsumer{
		Name: "app:ticketwatch", Cursor: 1, Enabled: true,
	}, old); err != nil {
		t.Fatalf("save consumer: %v", err)
	}

	if _, err := d.busPinAlarmHandler(t.Context(), nil); err != nil {
		t.Fatalf("first check: %v", err)
	}
	if list := unread(t, d); len(list) != 0 {
		t.Fatalf("the first check notified: %+v", list)
	}
	if _, err := d.busPinAlarmHandler(t.Context(), nil); err != nil {
		t.Fatalf("confirming check: %v", err)
	}
	list := unread(t, d)
	if len(list) != 1 {
		t.Fatalf("wrote %d notification(s), want 1", len(list))
	}
	if !strings.Contains(list[0].Title, "app:ticketwatch") {
		t.Errorf("title does not name the consumer: %q", list[0].Title)
	}
	// The finding is measured, not guessed: three unread events above seq 1.
	if !strings.Contains(list[0].Body, "3 events") {
		t.Errorf("body does not carry what is held: %q", list[0].Body)
	}
}

// A healthy bus must cost nothing to confirm and say nothing at all — the check
// runs on every daemon forever, and a tripwire working code never feels.
func TestPinAlarmHandlerIsSilentOnAHealthyBus(t *testing.T) {
	t.Setenv(bus.PinAlarmAgeEnv, "1h")
	d := &Daemon{store: store.New()}
	d.ensureEventBus()

	if _, err := d.store.AppendBusEvent(store.BusEvent{Name: "ticket.updated", Subject: "t"}, time.Now()); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := d.store.SaveBusConsumer(store.BusConsumer{
		Name: "app:ticketwatch", Cursor: 1, Enabled: true,
	}, time.Now()); err != nil {
		t.Fatalf("save consumer: %v", err)
	}

	for i := 0; i < 3; i++ {
		result, err := d.busPinAlarmHandler(t.Context(), nil)
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if counts, ok := result.(map[string]any); !ok || counts["pinned"] != 0 {
			t.Fatalf("check %d reported %+v, want nothing pinned", i, result)
		}
	}
	if list := unread(t, d); len(list) != 0 {
		t.Fatalf("a healthy bus produced %+v", list)
	}
}
