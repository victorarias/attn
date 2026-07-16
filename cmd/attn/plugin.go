package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/protocol"
	"nhooyr.io/websocket"
)

type pluginCommandResult struct {
	OK              bool              `json:"ok"`
	Plugin          *plugins.Manifest `json:"plugin,omitempty"`
	PluginDir       string            `json:"plugin_dir,omitempty"`
	RestartRequired bool              `json:"restart_required,omitempty"`
	Name            string            `json:"name,omitempty"`
}

type pluginListResult struct {
	Plugins []protocol.PluginInfo  `json:"plugins"`
	Issues  []protocol.PluginIssue `json:"issues,omitempty"`
}

type pluginManifestIssue struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

func runPluginCommand() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: attn plugin <install|install-bundled|list|uninstall|remove> ...")
		os.Exit(1)
	}

	switch os.Args[2] {
	case "install":
		runPluginInstall()
	case "install-bundled":
		runPluginInstallBundled()
	case "list":
		runPluginList()
	case "uninstall":
		runPluginUninstall()
	case "remove":
		runPluginRemove()
	default:
		fmt.Fprintf(os.Stderr, "unknown plugin command: %s\n", os.Args[2])
		os.Exit(1)
	}
}

func runPluginInstall() {
	fs := flag.NewFlagSet("plugin install", flag.ExitOnError)
	path := fs.String("path", "", "local plugin directory")
	_ = fs.Parse(os.Args[3:])
	sourcePath := strings.TrimSpace(*path)
	if sourcePath == "" {
		fmt.Fprintln(os.Stderr, "plugin install: --path is required")
		os.Exit(1)
	}
	sourcePath, err := resolveCLIPath(sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin install: %v\n", err)
		os.Exit(1)
	}
	result, err := installPluginViaDaemon(sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin install: %v\n", err)
		os.Exit(1)
	}
	printJSON(result)
}

func runPluginList() {
	event, err := pluginDaemonRequest(map[string]any{"cmd": protocol.CmdListPlugins}, protocol.EventPluginsUpdated, "", 10*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin list: %v\n", err)
		os.Exit(1)
	}
	var result pluginListResult
	payload, _ := json.Marshal(event)
	if err := json.Unmarshal(payload, &result); err != nil {
		fmt.Fprintf(os.Stderr, "plugin list: decode response: %v\n", err)
		os.Exit(1)
	}
	printJSON(result)
}

func runPluginInstallBundled() {
	if len(os.Args) != 4 || strings.TrimSpace(os.Args[3]) == "" {
		fmt.Fprintln(os.Stderr, "usage: attn plugin install-bundled <name>")
		os.Exit(1)
	}
	name := strings.TrimSpace(os.Args[3])
	if _, err := pluginDaemonRequest(map[string]any{"cmd": protocol.CmdInstallBundledPlugin, "name": name}, protocol.EventPluginActionResult, "install_bundled", 30*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "plugin install-bundled: %v\n", err)
		os.Exit(1)
	}
	printJSON(pluginCommandResult{OK: true, Name: name})
}

func runPluginUninstall() {
	if len(os.Args) != 4 || strings.TrimSpace(os.Args[3]) == "" {
		fmt.Fprintln(os.Stderr, "usage: attn plugin uninstall <name>")
		os.Exit(1)
	}
	name := strings.TrimSpace(os.Args[3])
	if _, err := pluginDaemonRequest(map[string]any{"cmd": protocol.CmdUninstallPlugin, "name": name}, protocol.EventPluginActionResult, "uninstall", 30*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "plugin uninstall: %v\n", err)
		os.Exit(1)
	}
	printJSON(pluginCommandResult{OK: true, Name: name})
}

func runPluginRemove() {
	if len(os.Args) != 4 || strings.TrimSpace(os.Args[3]) == "" {
		fmt.Fprintln(os.Stderr, "usage: attn plugin remove <name>")
		os.Exit(1)
	}
	name := strings.TrimSpace(os.Args[3])
	result, err := removePluginViaDaemon(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin remove: %v\n", err)
		os.Exit(1)
	}
	printJSON(result)
}

func installPluginViaDaemon(sourcePath string) (pluginCommandResult, error) {
	event, err := pluginDaemonRequest(
		map[string]any{"cmd": protocol.CmdInstallPlugin, "source": sourcePath},
		protocol.EventPluginActionResult,
		"install",
		30*time.Second,
	)
	if err != nil {
		return pluginCommandResult{}, err
	}
	name, _ := event["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return pluginCommandResult{}, fmt.Errorf("daemon returned no installed plugin name")
	}
	manifest, err := plugins.LoadManifest(filepath.Join(config.PluginDir(), name, plugins.ManifestName))
	if err != nil {
		return pluginCommandResult{}, fmt.Errorf("load installed plugin %q: %w", name, err)
	}
	return pluginCommandResult{
		OK:        true,
		Plugin:    &manifest,
		PluginDir: config.PluginDir(),
		Name:      name,
	}, nil
}

func removePluginViaDaemon(name string) (pluginCommandResult, error) {
	if _, err := pluginDaemonRequest(
		map[string]any{"cmd": protocol.CmdRemovePlugin, "name": name},
		protocol.EventPluginActionResult,
		"remove",
		30*time.Second,
	); err != nil {
		return pluginCommandResult{}, err
	}
	return pluginCommandResult{OK: true, PluginDir: config.PluginDir(), Name: name}, nil
}

func resolveCLIPath(path string) (string, error) {
	switch {
	case path == "~":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		path = home
	case strings.HasPrefix(path, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	return resolved, nil
}

func pluginDaemonRequest(payload map[string]any, expectedEvent, expectedAction string, timeout time.Duration) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	url := "ws://" + net.JoinHostPort("127.0.0.1", config.WSPort()) + "/ws"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	conn.SetReadLimit(16 << 20)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if _, _, err := conn.Read(ctx); err != nil {
		return nil, fmt.Errorf("read daemon state: %w", err)
	}
	hello := map[string]any{
		"cmd":          protocol.CmdClientHello,
		"client_kind":  "attn-cli",
		"version":      "protocol-" + protocol.ProtocolVersion,
		"capabilities": []string{protocol.CapabilityWorkspaceSessions},
	}
	if err := writePluginDaemonMessage(ctx, conn, hello); err != nil {
		return nil, err
	}
	if err := writePluginDaemonMessage(ctx, conn, payload); err != nil {
		return nil, err
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("wait for daemon response: %w", err)
		}
		var event map[string]any
		if json.Unmarshal(data, &event) != nil || event["event"] != expectedEvent {
			continue
		}
		if expectedAction != "" && event["action"] != expectedAction {
			continue
		}
		if success, present := event["success"].(bool); present && !success {
			message, _ := event["error"].(string)
			if message == "" {
				message = "plugin action failed"
			}
			return nil, fmt.Errorf("%s", message)
		}
		return event, nil
	}
}

func writePluginDaemonMessage(ctx context.Context, conn *websocket.Conn, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		return fmt.Errorf("write daemon command: %w", err)
	}
	return nil
}
