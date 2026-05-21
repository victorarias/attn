package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/plugins"
)

type pluginCommandResult struct {
	OK              bool              `json:"ok"`
	Plugin          *plugins.Manifest `json:"plugin,omitempty"`
	PluginDir       string            `json:"plugin_dir,omitempty"`
	RestartRequired bool              `json:"restart_required,omitempty"`
}

type pluginListResult struct {
	Plugins []plugins.Manifest    `json:"plugins"`
	Issues  []pluginManifestIssue `json:"issues,omitempty"`
}

type pluginManifestIssue struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

func runPluginCommand() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: attn plugin <install|list|remove> ...")
		os.Exit(1)
	}

	switch os.Args[2] {
	case "install":
		runPluginInstall()
	case "list":
		runPluginList()
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
	manifest, err := plugins.InstallPath(sourcePath, config.PluginDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin install: %v\n", err)
		os.Exit(1)
	}
	printJSON(pluginCommandResult{
		OK:              true,
		Plugin:          &manifest,
		PluginDir:       config.PluginDir(),
		RestartRequired: true,
	})
}

func runPluginList() {
	manifests, issues := plugins.Discover(config.PluginDir())
	result := pluginListResult{
		Plugins: manifests,
	}
	for _, issue := range issues {
		result.Issues = append(result.Issues, pluginManifestIssue{
			Path:  issue.Path,
			Error: issue.Err.Error(),
		})
	}
	printJSON(result)
}

func runPluginRemove() {
	if len(os.Args) != 4 || strings.TrimSpace(os.Args[3]) == "" {
		fmt.Fprintln(os.Stderr, "usage: attn plugin remove <name>")
		os.Exit(1)
	}
	name := strings.TrimSpace(os.Args[3])
	if err := plugins.Remove(config.PluginDir(), name); err != nil {
		fmt.Fprintf(os.Stderr, "plugin remove: %v\n", err)
		os.Exit(1)
	}
	printJSON(pluginCommandResult{
		OK:              true,
		PluginDir:       config.PluginDir(),
		RestartRequired: true,
	})
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
