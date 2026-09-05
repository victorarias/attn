package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func configuredBuild(t *testing.T, d *Daemon) delegationprefs.Config {
	t.Helper()
	roles := prompts.DelegationRoleTemplates()
	roles[2].Choices[0].Selection = delegationprefs.Selection{Harness: "codex"}
	roles[2].Instructions = "Check {{literal}} carefully"
	cfg, err := d.store.SaveDelegationPreferences(delegationprefs.Config{Enabled: true, Roles: roles})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestDelegationRolesResponseAndDisabledPrivacy(t *testing.T) {
	d := newDaemonForTest(t)
	cfg := configuredBuild(t, d)
	for _, enabled := range []bool{true, false} {
		if !enabled {
			cfg.Enabled = false
			if _, err := d.store.SaveDelegationPreferences(cfg); err != nil {
				t.Fatal(err)
			}
		}
		server, client := net.Pipe()
		go func() { defer server.Close(); d.handleDelegationRoles(server) }()
		var got protocol.Response
		if err := json.NewDecoder(client).Decode(&got); err != nil {
			t.Fatal(err)
		}
		client.Close()
		if !got.Ok || got.DelegationRoles == nil {
			t.Fatalf("%+v", got)
		}
		if enabled && (len(got.DelegationRoles.Roles) != 1 || got.DelegationRoles.Roles[0].ID != "build" || !strings.Contains(got.DelegationRoles.Roles[0].Instructions, "{{literal}}")) {
			t.Fatalf("%+v", got.DelegationRoles)
		}
		if !enabled && (len(got.DelegationRoles.Roles) != 0 || got.DelegationRoles.Fallback != nil || got.DelegationRoles.Guidance != "") {
			t.Fatal("disabled roles leaked")
		}
	}
}

func TestDelegationRoleLaunchContainsOnlySelectedGuidance(t *testing.T) {
	d := newDaemonForTest(t)
	backend := &fakeSpawnBackend{}
	_, source, _ := setupDelegationSource(t, d, backend)
	cfg := configuredBuild(t, d)
	var prompt string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.InitialPromptFile == "" {
			return
		}
		raw, err := os.ReadFile(opts.InitialPromptFile)
		if err != nil {
			t.Fatal(err)
		}
		prompt = string(raw)
		if err := os.Remove(opts.InitialPromptFile); err != nil {
			t.Fatal(err)
		}
	}
	result, err := d.delegate(&protocol.DelegateMessage{SourceSessionID: source, Brief: "Implement this task", Role: protocol.Ptr("build"), PreferencesRevision: &cfg.Revision})
	if err != nil {
		t.Fatal(err)
	}
	spawn, ok := backend.LastSpawn()
	if !ok || spawn.ID != result.SessionID || spawn.Model != "" || spawn.Effort != "" {
		t.Fatalf("blank settings must keep harness defaults: %+v", spawn)
	}
	for _, part := range []string{"Implement this task", "Role: Build", "Check {{literal}} carefully", cfg.Roles[2].StoppingPoint} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("missing %q: %s", part, prompt)
		}
	}
	for _, absent := range []string{"Role: Scout", "--preferences-revision", cfg.Roles[0].Instructions} {
		if strings.Contains(prompt, absent) {
			t.Fatalf("other routing data leaked: %q", absent)
		}
	}
}

func TestDelegationDiscoveryAndEffortValidation(t *testing.T) {
	d := newDaemonForTest(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "queried")
	executable := filepath.Join(dir, "claude")
	script := "#!/bin/sh\ntouch '" + marker + "'\nread request\nprintf '%s\\n' '{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"attn-model-discovery\",\"response\":{\"models\":[{\"value\":\"known\",\"supportsEffort\":true,\"supportedEffortLevels\":[\"medium\",\"high\"]}]}}}'\ncat >/dev/null\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d.store.SetSetting(canonicalExecutableSettingKey("claude"), executable)
	if _, err := d.discoverDelegationModels(context.Background(), "claude"); err == nil {
		t.Fatal("discovered while disabled")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("disabled discovery launched a process")
	}
	cfg := configuredBuild(t, d)
	cfg.Roles[2].Choices[0].Selection = delegationprefs.Selection{Harness: "claude", Model: "known", Effort: "high"}
	if _, err := d.store.SaveDelegationPreferences(cfg); err != nil {
		t.Fatal(err)
	}
	catalog, err := d.discoverDelegationModels(context.Background(), "claude")
	if err != nil || len(catalog.Models) != 1 || catalog.Models[0].Access != "unknown" {
		t.Fatalf("%+v %v", catalog, err)
	}
	if _, err := d.resolveDelegationPreferences(&protocol.DelegateMessage{Role: protocol.Ptr("build"), Effort: protocol.Ptr("max")}); err == nil {
		t.Fatal("unsupported model effort accepted")
	}
	got, err := d.resolveDelegationPreferences(&protocol.DelegateMessage{Role: protocol.Ptr("build"), Effort: protocol.Ptr("medium")})
	if err != nil || got.Selection.Effort != "medium" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = d.resolveDelegationPreferences(&protocol.DelegateMessage{Role: protocol.Ptr("build"), Model: protocol.Ptr("custom")})
	if err != nil || got.Selection.Model != "custom" || got.Selection.Effort != "" {
		t.Fatalf("manual model: %+v %v", got, err)
	}
}

func TestDelegationInvalidRoleCreatesNoOperation(t *testing.T) {
	d := newDaemonForTest(t)
	configuredBuild(t, d)
	if _, err := d.startDelegation(&protocol.DelegateMessage{RequestID: "invalid-role", Role: protocol.Ptr("missing"), Brief: "Task"}); err == nil {
		t.Fatal("invalid role accepted")
	}
	if _, err := d.store.GetDelegationOperation("invalid-role"); err == nil {
		t.Fatal("invalid role created an operation")
	}
}

func TestDelegationPluginDiscoveryUsesRegisteredCapability(t *testing.T) {
	d := newDaemonForTest(t)
	configuredBuild(t, d)
	client, done := startPluginPipe(t, d, "catalog-fixture", nil)
	defer func() { _ = client.Close(); <-done }()
	registerTestPluginDriver(t, client, "fixture", map[string]bool{"initial_prompt": true, "model_pin": true, "effort_pin": true, "model_discovery": true})
	found := false
	for _, h := range d.delegationHarnesses() {
		if h.ID == "fixture" {
			found = h.Discovery
		}
	}
	if !found {
		t.Fatal("plugin discovery capability missing")
	}
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		for {
			request := decodeJSONRPCMessage(t, client)
			if request.Method == pluginHealthMethod {
				respondPluginRequest(t, client, request, pluginHealthResult{OK: true})
				continue
			}
			if request.Method != "driver.models" {
				t.Errorf("unexpected method %s", request.Method)
				return
			}
			respondPluginRequest(t, client, request, delegationModelCatalog{Models: []protocol.DelegationModel{{Harness: "fixture", Provider: "work", ID: "custom", EffortSupport: protocol.ModelCapabilitySupportSupported, EffortLevels: []string{"low", "high"}}}})
			return
		}
	}()
	catalog, err := d.discoverDelegationModels(context.Background(), "fixture")
	if err != nil || len(catalog.Models) != 1 || catalog.Models[0].Provider != "work" || catalog.Models[0].Access != "unknown" {
		t.Fatalf("%+v %v", catalog, err)
	}
	<-returned
}
