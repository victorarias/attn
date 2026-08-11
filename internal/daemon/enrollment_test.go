package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/protocol"
)

// newEnrolledDaemon returns a daemon whose data dir has a daemon id and, when
// homeDaemonID is non-empty, an enrollment record naming that other daemon as
// its home.
func newEnrolledDaemon(t *testing.T, homeDaemonID string) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	id, err := enrollment.EnsureDaemonID(d.dataRoot)
	if err != nil {
		t.Fatalf("EnsureDaemonID: %v", err)
	}
	d.daemonInstanceID = id
	if err := d.ensureEnrollment(); err != nil {
		t.Fatalf("ensureEnrollment: %v", err)
	}
	if homeDaemonID != "" {
		if _, err := enrollment.Enroll(d.dataRoot, homeDaemonID); err != nil {
			t.Fatalf("Enroll: %v", err)
		}
	}
	return d
}

func writeCorruptEnrollmentRecord(dataRoot string) error {
	return os.WriteFile(filepath.Join(dataRoot, enrollment.RecordFileName), []byte("{not json"), 0600)
}

func healthPayload(t *testing.T, d *Daemon) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	d.handleHealth(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health %q: %v", recorder.Body.String(), err)
	}
	return payload
}

func initialStateEvent(t *testing.T, d *Daemon) protocol.InitialStateMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})
	d.sendInitialState(client)
	message := <-client.send
	var event protocol.InitialStateMessage
	if err := json.Unmarshal(message.payload, &event); err != nil {
		t.Fatalf("decode initial_state: %v", err)
	}
	return event
}

func TestDaemon_FreshDataDirIsItsOwnHome(t *testing.T) {
	d := newEnrolledDaemon(t, "")

	status, err := d.enrollmentStatus()
	if err != nil {
		t.Fatalf("enrollmentStatus: %v", err)
	}
	if !status.IsHome() {
		t.Fatalf("fresh daemon is not a home: %+v", status)
	}
	if err := d.requireHome("the garden"); err != nil {
		t.Fatalf("fence refused on a home daemon: %v", err)
	}

	health := healthPayload(t, d)
	if health["enrollment"] != "home" {
		t.Fatalf("health enrollment = %v, want \"home\"", health["enrollment"])
	}
	if health["home_daemon_id"] != d.daemonInstanceID {
		t.Fatalf("health home_daemon_id = %v, want %q", health["home_daemon_id"], d.daemonInstanceID)
	}

	event := initialStateEvent(t, d)
	if got := protocol.Deref(event.HomeDaemonID); got != d.daemonInstanceID {
		t.Fatalf("initial_state home_daemon_id = %q, want its own id %q", got, d.daemonInstanceID)
	}
}

func TestDaemon_OutpostReportsItsHomeAndFencesHomeState(t *testing.T) {
	const home = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	d := newEnrolledDaemon(t, home)

	health := healthPayload(t, d)
	if want := "outpost of " + home; health["enrollment"] != want {
		t.Fatalf("health enrollment = %v, want %q", health["enrollment"], want)
	}
	if health["home_daemon_id"] != home {
		t.Fatalf("health home_daemon_id = %v, want %q", health["home_daemon_id"], home)
	}

	event := initialStateEvent(t, d)
	if got := protocol.Deref(event.HomeDaemonID); got != home {
		t.Fatalf("initial_state home_daemon_id = %q, want %q", got, home)
	}
	if got := protocol.Deref(event.DaemonInstanceID); got == home {
		t.Fatalf("initial_state daemon_instance_id = %q, want it to differ from its home", got)
	}

	err := d.requireHome("the garden")
	if err == nil {
		t.Fatal("fence passed on an outpost, want a refusal")
	}
	for _, want := range []string{"the garden", d.daemonInstanceID, home, "attn enrollment leave", enrollment.PlanPath} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("fence refusal does not name %q:\n%s", want, err.Error())
		}
	}
}

func TestDaemon_OutpostDoesNotEnrollRemotes(t *testing.T) {
	home := newEnrolledDaemon(t, "")
	if got := home.homeDaemonIDForEnrollment(); got != home.daemonInstanceID {
		t.Fatalf("home daemon offered %q as the enrolling home, want %q", got, home.daemonInstanceID)
	}

	outpost := newEnrolledDaemon(t, "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if got := outpost.homeDaemonIDForEnrollment(); got != "" {
		t.Fatalf("outpost offered %q as the enrolling home, want no enrollment", got)
	}
}

func TestDaemon_UnreadableRecordFailsTheFenceClosed(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	if err := writeCorruptEnrollmentRecord(d.dataRoot); err != nil {
		t.Fatalf("corrupt the record: %v", err)
	}

	if err := d.requireHome("the crew"); err == nil {
		t.Fatal("fence passed with an unreadable record, want it to fail closed")
	}
	if health := healthPayload(t, d); health["enrollment"] != "unknown" {
		t.Fatalf("health enrollment = %v, want \"unknown\"", health["enrollment"])
	}
	if got := protocol.Deref(initialStateEvent(t, d).HomeDaemonID); got != "" {
		t.Fatalf("initial_state home_daemon_id = %q, want empty on an unreadable record", got)
	}
}
