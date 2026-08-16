package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

// Auto mode reaches a session through the launch, not through a live refresh: a
// spawn carries the config that is promoted at that moment, and a change after
// it applies to the next session.

func TestSpawnCarriesThePromotedAutoModeConfig(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	now := time.Now().UTC()
	proposal, err := d.store.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "", now)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, _, err := d.store.PromoteAutoModeProposal(proposal.ID, now); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if _, err := d.store.SetAutoModeEnvironment([]string{"never touch prod"}, now); err != nil {
		t.Fatalf("set environment: %v", err)
	}

	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{
		"launch_instructions": true, "auto_mode": true,
	})

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		request := decodeJSONRPCMessage(t, client)
		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode spawn params: %v", err)
			return
		}
		if params.AutoMode == nil {
			t.Error("spawn params carry no auto mode config")
			return
		}
		if len(params.AutoMode.Allow) != 1 || params.AutoMode.Allow[0] != "git push origin*" {
			t.Errorf("auto mode allow = %v, want the promoted pattern", params.AutoMode.Allow)
		}
		if len(params.AutoMode.Environment) != 1 {
			t.Errorf("auto mode environment = %v", params.AutoMode.Environment)
		}
		if params.AutoMode.ClassifierModel != automode.DefaultClassifierModel {
			t.Errorf("classifier model = %q", params.AutoMode.ClassifierModel)
		}
		// The payload IS plugins/attn-pi/automode/config.ts's raw shape, so the
		// wire keys are checked rather than only the decoded struct.
		var raw struct {
			AutoMode map[string]json.RawMessage `json:"auto_mode"`
		}
		if err := json.Unmarshal(request.Params, &raw); err != nil {
			t.Errorf("decode raw spawn params: %v", err)
			return
		}
		for _, key := range []string{"enabled_default", "environment", "allow", "hard_deny", "classifier_model", "escalation_model"} {
			if _, ok := raw.AutoMode[key]; !ok {
				t.Errorf("auto mode payload is missing %q", key)
			}
		}
		respondPluginRequest(t, client, request, pluginDriverSpawnResult{Argv: []string{"snipe"}})
	}()

	addTestWorkspace(d, "workspace-snipe", t.TempDir())
	ws := &wsClient{send: make(chan outboundMessage, 2), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleSpawnSession(ws, &protocol.SpawnSessionMessage{
		ID:          "snipe-session",
		Cwd:         t.TempDir(),
		WorkspaceID: "workspace-snipe",
		Agent:       "snipe",
		Cols:        80,
		Rows:        24,
	})
	<-requestDone
}

// A reload replaces the runtime in place, so it resolves the config the same
// way a spawn does — and that is where a session picks up a promotion made
// while it was running.
func TestReloadCarriesThePromotedAutoModeConfig(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"snipe-session"},
		info:    ptybackend.SessionInfo{Cols: 100, Rows: 32},
		params:  ptybackend.SessionLaunchParams{Recorded: true},
	}
	d := newReloadTestDaemon(t, backend)
	addTestWorkspace(d, "ws-snipe-session", t.TempDir())
	addReloadSession(d, "snipe-session", protocol.SessionAgent("snipe"), protocol.SessionStateIdle)
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())

	now := time.Now().UTC()
	proposal, err := d.store.CreateAutoModeProposal(automode.KindDeny, "", "curl *", "", now)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, _, err := d.store.PromoteAutoModeProposal(proposal.ID, now); err != nil {
		t.Fatalf("promote: %v", err)
	}

	plugin, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = plugin.Close()
		<-done
	}()
	registerTestPluginDriver(t, plugin, "snipe", map[string]bool{
		"resume": true, "launch_instructions": true, "auto_mode": true,
	})

	resumed := make(chan struct{})
	go func() {
		defer close(resumed)
		request := decodeJSONRPCMessage(t, plugin)
		if request.Method != "driver.resume" {
			t.Errorf("method = %q, want driver.resume", request.Method)
			return
		}
		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode resume params: %v", err)
			return
		}
		if params.AutoMode == nil {
			t.Error("resume params carry no auto mode config")
		} else if len(params.AutoMode.HardDeny) != 1 || params.AutoMode.HardDeny[0] != "curl *" {
			t.Errorf("auto mode hard deny = %v, want the promoted pattern", params.AutoMode.HardDeny)
		}
		respondPluginRequest(t, plugin, request, pluginDriverSpawnResult{Argv: []string{"snipe"}})
	}()

	d.reloadSessionAgent("snipe-session")

	select {
	case <-resumed:
	case <-time.After(2 * time.Second):
		t.Fatal("driver.resume was never requested")
	}
}

func TestSpawnOmitsAutoModeForADriverThatDoesNotAskForIt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"launch_instructions": true})

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		request := decodeJSONRPCMessage(t, client)
		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode spawn params: %v", err)
			return
		}
		if params.AutoMode != nil {
			t.Errorf("a driver without the auto_mode capability was handed %+v", params.AutoMode)
		}
		respondPluginRequest(t, client, request, pluginDriverSpawnResult{Argv: []string{"snipe"}})
	}()

	addTestWorkspace(d, "workspace-snipe", t.TempDir())
	ws := &wsClient{send: make(chan outboundMessage, 2), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleSpawnSession(ws, &protocol.SpawnSessionMessage{
		ID:          "snipe-session",
		Cwd:         t.TempDir(),
		WorkspaceID: "workspace-snipe",
		Agent:       "snipe",
		Cols:        80,
		Rows:        24,
	})
	<-requestDone
}
