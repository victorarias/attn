package preflight

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func passingProber(t *testing.T) prober {
	t.Helper()
	return prober{
		lookPath:     func(tool string) (string, error) { return "/tools/" + filepath.Base(tool), nil },
		writable:     func(string) error { return nil },
		pathIsSocket: func(string) error { return nil },
		goCachePaths: func(context.Context) ([]string, error) { return []string{"/cache/build", "/cache/mod"}, nil },
		appProtocol:  func(context.Context, string) (string, error) { return protocol.ProtocolVersion, nil },
		daemonHealth: func(context.Context, string) (daemonHealth, error) {
			return daemonHealth{
				Protocol: protocol.ProtocolVersion, Profile: config.ProfileLabel(),
				DataDir: config.DataDir(), SocketPath: config.SocketPath(), Port: config.WSPort(),
			}, nil
		},
		requiredTools: []string{"git", "go"},
	}
}

func TestRunPassesWithConsistentEnvironment(t *testing.T) {
	report := run(context.Background(), Options{Agent: "codex", Model: "gpt-test", Effort: "high", WorkingDir: t.TempDir()}, passingProber(t))
	if !report.OK() || report.Status != StatusPass {
		t.Fatalf("report status = %q, checks=%+v", report.Status, report.Checks)
	}
	if report.Launch.Model.Value != "gpt-test" || report.Launch.Effort.Value != "high" {
		t.Fatalf("launch = %+v", report.Launch)
	}
	assertCheck(t, report, "routing.daemon", StatusPass, "")
	assertCheck(t, report, "protocol.app_daemon", StatusPass, "")
}

func TestRunReportsRootCausesAndActions(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*prober)
		checkName string
		contains  string
	}{
		{
			name: "missing required tool",
			mutate: func(p *prober) {
				p.lookPath = func(tool string) (string, error) {
					if tool == "go" {
						return "", errors.New("missing")
					}
					return "/tools/" + tool, nil
				}
			},
			checkName: "tool.go", contains: "not found on PATH",
		},
		{
			name: "unwritable cache",
			mutate: func(p *prober) {
				p.writable = func(path string) error {
					if path == "/cache/build" {
						return errors.New("permission denied")
					}
					return nil
				}
			},
			checkName: "path.go_build_cache", contains: "permission denied",
		},
		{
			name: "profile routing mismatch",
			mutate: func(p *prober) {
				p.daemonHealth = func(context.Context, string) (daemonHealth, error) {
					return daemonHealth{Protocol: protocol.ProtocolVersion, Profile: "other", DataDir: "/other", SocketPath: "/other.sock", Port: "1"}, nil
				}
			},
			checkName: "routing.daemon", contains: "routing mismatch",
		},
		{
			name: "app daemon protocol mismatch",
			mutate: func(p *prober) {
				p.daemonHealth = func(context.Context, string) (daemonHealth, error) {
					return daemonHealth{Protocol: "older", Profile: config.ProfileLabel(), DataDir: config.DataDir(), SocketPath: config.SocketPath(), Port: config.WSPort()}, nil
				}
			},
			checkName: "protocol.app_daemon", contains: "does not match",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := passingProber(t)
			tt.mutate(&p)
			report := run(context.Background(), Options{Agent: "codex", WorkingDir: t.TempDir()}, p)
			if report.OK() || report.Status != StatusFail {
				t.Fatalf("report status = %q, want fail", report.Status)
			}
			assertCheck(t, report, tt.checkName, StatusFail, tt.contains)
		})
	}
}

func TestProbeWritableDirectoryDoesNotLeaveArtifact(t *testing.T) {
	dir := t.TempDir()
	if err := probeWritableDirectory(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("probe left artifacts: %+v", entries)
	}
}

func assertCheck(t *testing.T, report Report, name, status, contains string) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name != name {
			continue
		}
		if check.Status != status || (contains != "" && !strings.Contains(check.Summary, contains)) {
			t.Fatalf("check %s = %+v", name, check)
		}
		if status == StatusFail && check.Action == "" {
			t.Fatalf("failing check %s has no action", name)
		}
		return
	}
	t.Fatalf("check %s not found in %+v", name, report.Checks)
}
