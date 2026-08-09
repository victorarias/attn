package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func reported(visible, dashboard bool, idle float64, at time.Time) clientPresence {
	return clientPresence{
		Visible:          visible,
		DashboardVisible: dashboard,
		IdleSeconds:      idle,
		ReportedAt:       at,
	}
}

func TestPresenceTierFromOneClientsReport(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	const idleLimit = 90 * time.Second

	cases := []struct {
		name    string
		report  clientPresence
		want    PresenceTier
		because string
	}{
		{
			name:    "dashboard on screen",
			report:  reported(true, true, 0, now),
			want:    PresenceWatching,
			because: "the line is actually being read",
		},
		{
			name:    "in the app, dashboard not showing, input recent",
			report:  reported(true, false, 10, now),
			want:    PresencePresent,
			because: "the user is inside a session and may glance back",
		},
		{
			name:    "in the app, dashboard not showing, input long ago",
			report:  reported(true, false, 600, now),
			want:    PresenceAway,
			because: "a window left open is not attention",
		},
		{
			name:    "app hidden even with the dashboard mounted",
			report:  reported(false, true, 0, now),
			want:    PresenceAway,
			because: "a rendered view nobody can see is still nobody looking",
		},
		{
			name:    "in the app, no input observed yet this connection",
			report:  reported(true, false, -1, now),
			want:    PresenceAway,
			because: "unknown idleness is not recent input",
		},
		{
			name:    "a client that never reported",
			report:  clientPresence{},
			want:    PresenceAway,
			because: "a fresh connection has said nothing yet",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.report.tier(now, idleLimit); got != tc.want {
				t.Errorf("tier = %s, want %s — %s", got, tc.want, tc.because)
			}
		})
	}
}

// Home stays on screen when nobody is there. Without an idle stop the cheapest
// tier to reach would be the only one that never expires, and an app left on
// home would generate for every working session until someone came back.
func TestWatchingExpiresAfterALongIdleOnHome(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	const idleLimit = 90 * time.Second

	reading := reported(true, true, presenceWatchingIdleLimit.Seconds()-60, now)
	if got := reading.tier(now, idleLimit); got != PresenceWatching {
		t.Errorf("tier = %s just inside the limit, want watching — reading home is a thing people do without touching anything", got)
	}
	abandoned := reported(true, true, presenceWatchingIdleLimit.Seconds()+60, now)
	if got := abandoned.tier(now, idleLimit); got != PresenceAway {
		t.Errorf("tier = %s past the limit, want away", got)
	}
}

// A client that has observed no input at all is the case the limit above would
// otherwise miss entirely: -1 is not a number that ever crosses a threshold. On
// home its idleness is measured from the connection instead, which reads a fresh
// untouched window as attention and the same window hours later as nobody there.
func TestWatchingWithNoInputMeasuresFromTheConnection(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	const idleLimit = 90 * time.Second

	fresh := clientPresence{
		Visible: true, DashboardVisible: true, IdleSeconds: -1,
		ReportedAt: now, FirstReportAt: now.Add(-time.Minute),
	}
	if got := fresh.tier(now, idleLimit); got != PresenceWatching {
		t.Errorf("tier = %s on a window opened a minute ago, want watching", got)
	}
	stale := clientPresence{
		Visible: true, DashboardVisible: true, IdleSeconds: -1,
		ReportedAt: now, FirstReportAt: now.Add(-8 * time.Hour),
	}
	if got := stale.tier(now, idleLimit); got != PresenceAway {
		t.Errorf("tier = %s on a window open for eight untouched hours, want away", got)
	}
}

// FirstReportAt is the floor for a client that never reports input, so it must
// survive every later report. Resetting it on each heartbeat would restart the
// clock forever and the limit above would never fire.
func TestFirstReportAtSurvivesLaterReports(t *testing.T) {
	client := &wsClient{}
	first := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	msg := &protocol.SetClientPresenceMessage{Visible: true, DashboardVisible: true}

	client.setPresence(msg, first)
	client.setPresence(msg, first.Add(time.Hour))

	if got := client.presenceReport().FirstReportAt; !got.Equal(first) {
		t.Errorf("FirstReportAt = %s after a later report, want the first one (%s)", got, first)
	}
}

// The whole reason presence is a heartbeat rather than a latch: a client that
// crashes or is force-quit while the dashboard is up must not pin generation on
// forever with nobody looking. Expiry fails toward off.
func TestAClientThatStopsHeartbeatingExpiresToAway(t *testing.T) {
	reportedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	watching := reported(true, true, 0, reportedAt)

	if got := watching.tier(reportedAt.Add(presenceHeartbeatGrace-time.Second), 90*time.Second); got != PresenceWatching {
		t.Errorf("inside the grace window tier = %s, want watching", got)
	}
	if got := watching.tier(reportedAt.Add(presenceHeartbeatGrace+time.Second), 90*time.Second); got != PresenceAway {
		t.Errorf("past the grace window tier = %s, want away", got)
	}
}

// Two windows open, one showing the dashboard: the line is being read
// somewhere, and that is the only question the tier answers.
func TestPresenceTierIsTheHighestAcrossClients(t *testing.T) {
	d := &Daemon{wsHub: newWSHub()}
	now := time.Now()

	background := &wsClient{presence: reported(true, false, 600, now)}
	foreground := &wsClient{presence: reported(true, true, 0, now)}
	d.wsHub.clients[background] = true
	d.wsHub.clients[foreground] = true

	if got := d.PresenceTier(); got != PresenceWatching {
		t.Errorf("tier = %s across an away client and a watching one, want watching", got)
	}

	delete(d.wsHub.clients, foreground)
	if got := d.PresenceTier(); got != PresenceAway {
		t.Errorf("tier = %s after the watching client disconnected, want away", got)
	}
}

// Nothing is persisted, so a daemon that has just started — or one every client
// has disconnected from — generates nothing until an app says otherwise.
func TestPresenceTierWithNoClientsIsAway(t *testing.T) {
	d := &Daemon{wsHub: newWSHub()}
	if got := d.PresenceTier(); got != PresenceAway {
		t.Errorf("tier = %s with no clients connected, want away", got)
	}
}

func TestSetPresenceRecordsWhatTheClientReported(t *testing.T) {
	d := &Daemon{wsHub: newWSHub()}
	client := &wsClient{}

	d.handleSetClientPresence(client, &protocol.SetClientPresenceMessage{
		Cmd:              protocol.CmdSetClientPresence,
		Visible:          true,
		DashboardVisible: true,
		IdleSeconds:      protocol.Ptr(3.5),
	})

	got := client.presenceReport()
	if !got.Visible || !got.DashboardVisible || got.IdleSeconds != 3.5 {
		t.Errorf("presence = %+v, want the reported values", got)
	}
	if got.ReportedAt.IsZero() {
		t.Error("presence carries no report time, so it could never expire")
	}
}

// A client that omits idle_seconds has observed no input; that must not read as
// "input zero seconds ago", which would make every visible window `present`
// forever.
func TestAbsentIdleSecondsIsNotZeroIdleSeconds(t *testing.T) {
	client := &wsClient{}
	client.setPresence(&protocol.SetClientPresenceMessage{Visible: true}, time.Now())

	if got := client.presenceReport().tier(time.Now(), 90*time.Second); got != PresenceAway {
		t.Errorf("tier = %s with no idle report, want away", got)
	}
}
