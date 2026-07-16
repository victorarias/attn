package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nhooyr.io/websocket"
)

func TestInstallPluginViaDaemonUsesGuardedAction(t *testing.T) {
	pluginDir := t.TempDir()
	t.Setenv("ATTN_PLUGIN_DIR", pluginDir)
	writeCLIPluginManifest(t, pluginDir, "fixture-plugin")
	requests := startPluginCLIDaemon(t, map[string]any{
		"event":   "plugin_action_result",
		"action":  "install",
		"name":    "fixture-plugin",
		"success": true,
	})

	result, err := installPluginViaDaemon("/tmp/plugin-source")
	if err != nil {
		t.Fatalf("installPluginViaDaemon: %v", err)
	}
	request := <-requests
	if request["cmd"] != "install_plugin" || request["source"] != "/tmp/plugin-source" {
		t.Fatalf("request=%v, want guarded daemon install", request)
	}
	if !result.OK || result.Name != "fixture-plugin" || result.Plugin == nil || result.Plugin.Name != "fixture-plugin" || result.RestartRequired {
		t.Fatalf("result=%+v, want immediate daemon-backed install", result)
	}
}

func TestInstallPluginViaDaemonPropagatesBundledCollision(t *testing.T) {
	requests := startPluginCLIDaemon(t, map[string]any{
		"event":   "plugin_action_result",
		"action":  "install",
		"name":    "attn-opencode",
		"success": false,
		"error":   "uninstall bundled plugin before installing a user override",
	})

	_, err := installPluginViaDaemon("/tmp/attn-opencode")
	if err == nil || !strings.Contains(err.Error(), "uninstall bundled plugin") {
		t.Fatalf("error=%v, want bundled collision", err)
	}
	if request := <-requests; request["cmd"] != "install_plugin" {
		t.Fatalf("request=%v, want daemon install action", request)
	}
}

func TestRemovePluginViaDaemonPropagatesActiveRunGuard(t *testing.T) {
	requests := startPluginCLIDaemon(t, map[string]any{
		"event":   "plugin_action_result",
		"action":  "remove",
		"name":    "fixture-plugin",
		"success": false,
		"error":   "plugin owns 1 active delegated run(s)",
	})

	_, err := removePluginViaDaemon("fixture-plugin")
	if err == nil || !strings.Contains(err.Error(), "active delegated run") {
		t.Fatalf("error=%v, want active-run guard", err)
	}
	request := <-requests
	if request["cmd"] != "remove_plugin" || request["name"] != "fixture-plugin" {
		t.Fatalf("request=%v, want guarded daemon remove", request)
	}
}

func startPluginCLIDaemon(t *testing.T, response map[string]any) <-chan map[string]any {
	t.Helper()
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx := context.Background()
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"event":"initial_state"}`)); err != nil {
			t.Errorf("write initial state: %v", err)
			return
		}
		for i := 0; i < 2; i++ {
			_, payload, err := conn.Read(ctx)
			if err != nil {
				t.Errorf("read request %d: %v", i, err)
				return
			}
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Errorf("decode request %d: %v", i, err)
				return
			}
			if i == 1 {
				requests <- message
			}
		}
		payload, err := json.Marshal(response)
		if err != nil {
			t.Errorf("encode response: %v", err)
			return
		}
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	t.Setenv("ATTN_WS_PORT", parsed.Port())
	return requests
}

func writeCLIPluginManifest(t *testing.T, pluginDir, name string) {
	t.Helper()
	root := filepath.Join(pluginDir, name)
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "index.ts"), []byte("// fixture\n"), 0o644); err != nil {
		t.Fatalf("write entrypoint: %v", err)
	}
	manifest := "name = \"" + name + "\"\nversion = \"0.1.0\"\nattn_api_version = 4\n\n[plugin]\nentrypoint = \"src/index.ts\"\n"
	if err := os.WriteFile(filepath.Join(root, "attn-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}
