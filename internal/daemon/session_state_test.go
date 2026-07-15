package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSessionStateDoor_AcceptedCauseProfiles(t *testing.T) {
	for _, tc := range []struct {
		name          string
		state         string
		cause         func(*testing.T, *Daemon, string) sessionStateCause
		wantTouch     bool
		wantTracking  bool
		wantBroadcast bool
	}{
		{
			name:          "live signal",
			state:         protocol.StateWorking,
			cause:         func(*testing.T, *Daemon, string) sessionStateCause { return liveSignal{} },
			wantTouch:     true,
			wantTracking:  true,
			wantBroadcast: true,
		},
		{
			name:          "daemon observation",
			state:         protocol.StateWorking,
			cause:         func(*testing.T, *Daemon, string) sessionStateCause { return daemonObservation{} },
			wantTracking:  true,
			wantBroadcast: true,
		},
		{
			name:  "classifier observation",
			state: protocol.StateWorking,
			cause: func(*testing.T, *Daemon, string) sessionStateCause {
				return classifierObservation{observedAt: time.Now()}
			},
			wantTracking:  true,
			wantBroadcast: true,
		},
		{
			name:  "plugin report",
			state: protocol.StateWorking,
			cause: func(t *testing.T, d *Daemon, id string) sessionStateCause {
				if !d.store.BeginAgentDriverRun(id, "plugin", "run") {
					t.Fatal("BeginAgentDriverRun() = false")
				}
				return pluginReport{runID: "run", seq: 1}
			},
			wantTouch:     true,
			wantTracking:  true,
			wantBroadcast: true,
		},
		{
			name:  "startup recovery",
			state: protocol.StateWorking,
			cause: func(*testing.T, *Daemon, string) sessionStateCause { return startupRecovery{} },
		},
		{
			name:          "process exit",
			state:         protocol.StateIdle,
			cause:         func(*testing.T, *Daemon, string) sessionStateCause { return processExit{} },
			wantTouch:     true,
			wantBroadcast: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
			id := "session"
			d.store.Add(&protocol.Session{
				ID:             id,
				Label:          id,
				Agent:          protocol.SessionAgentCodex,
				Directory:      t.TempDir(),
				State:          protocol.SessionStateIdle,
				StateSince:     characterizationOldTimestamp,
				StateUpdatedAt: characterizationOldTimestamp,
				LastSeen:       characterizationOldTimestamp,
			})
			capture := captureBroadcasts(d)

			if !d.applyState(sessionStateChange{sessionID: id, state: tc.state, cause: tc.cause(t, d, id)}) {
				t.Fatal("applyState() = false, want true")
			}

			session := d.store.Get(id)
			if session == nil || string(session.State) != tc.state {
				t.Fatalf("session=%+v, want state %q", session, tc.state)
			}
			if session.StateUpdatedAt == characterizationOldTimestamp {
				t.Fatal("accepted transition did not update state timestamp")
			}
			if touched := session.LastSeen != characterizationOldTimestamp; touched != tc.wantTouch {
				t.Fatalf("Touch=%v, want %v; LastSeen=%q", touched, tc.wantTouch, session.LastSeen)
			}
			d.longRunMu.Lock()
			tracked := !d.longRun[id].workingSince.IsZero()
			d.longRunMu.Unlock()
			if tracked != tc.wantTracking {
				t.Fatalf("long-run tracked=%v, want %v", tracked, tc.wantTracking)
			}
			broadcasts := characterizationEventCount(capture.snapshot(), protocol.EventSessionStateChanged, id)
			if got := broadcasts > 0; got != tc.wantBroadcast {
				t.Fatalf("state broadcast=%v (%d events), want %v", got, broadcasts, tc.wantBroadcast)
			}
		})
	}
}

func TestSessionStateDoor_MissingSessionHasNoEffects(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
	capture := captureBroadcasts(d)

	if d.applyState(sessionStateChange{
		sessionID: "missing",
		state:     protocol.StateWorking,
		cause:     liveSignal{},
	}) {
		t.Fatal("applyState(missing) = true, want false")
	}
	d.longRunMu.Lock()
	_, tracked := d.longRun["missing"]
	d.longRunMu.Unlock()
	if tracked {
		t.Fatal("missing-session transition created long-run tracking")
	}
	if events := capture.snapshot(); len(events) != 0 {
		t.Fatalf("missing-session transition broadcast events: %+v", events)
	}
}

func TestSessionStateDoor_IsOnlyDaemonStoreStateWriter(t *testing.T) {
	stateMethods := map[string]bool{
		"UpdateState":              true,
		"UpdateStateWithTimestamp": true,
		"ApplyAgentDriverState":    true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "session_state.go" {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !stateMethods[selector.Sel.Name] {
				return true
			}
			position := fset.Position(call.Pos())
			t.Errorf("session state store mutation %s is outside session_state.go at %s", selector.Sel.Name, position)
			return true
		})
	}
}
