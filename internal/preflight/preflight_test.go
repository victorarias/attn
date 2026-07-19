package preflight

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func passingProber(t *testing.T) prober {
	t.Helper()
	dataDir, err := config.CanonicalRuntimePath(config.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	socketPath, err := config.CanonicalRuntimePath(config.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	return prober{
		lookPath:     func(tool string) (string, error) { return "/tools/" + filepath.Base(tool), nil },
		writable:     func(string) error { return nil },
		pathIsSocket: func(string) error { return nil },
		goCachePaths: func(context.Context) ([]string, error) { return []string{"/cache/build", "/cache/mod"}, nil },
		appProtocol:  func(context.Context, string) (string, error) { return protocol.ProtocolVersion, nil },
		daemonHealth: func(context.Context, string) (daemonHealth, error) {
			return daemonHealth{
				Protocol: protocol.ProtocolVersion, Profile: config.ProfileLabel(),
				DataDir: dataDir, SocketPath: socketPath, Port: config.WSPort(),
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
			name: "unreadable app protocol with healthy daemon",
			mutate: func(p *prober) {
				p.appProtocol = func(context.Context, string) (string, error) {
					return "", errors.New("app missing")
				}
			},
			checkName: "protocol.app_daemon", contains: "app protocol is unavailable",
		},
		{
			name: "app daemon protocol mismatch",
			mutate: func(p *prober) {
				dataDir, _ := config.CanonicalRuntimePath(config.DataDir())
				socketPath, _ := config.CanonicalRuntimePath(config.SocketPath())
				p.daemonHealth = func(context.Context, string) (daemonHealth, error) {
					return daemonHealth{Protocol: "older", Profile: config.ProfileLabel(), DataDir: dataDir, SocketPath: socketPath, Port: config.WSPort()}, nil
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

func TestRunRejectsSameRelativeRoutingOverridesFromDifferentWorkingDirectories(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	cliDir := t.TempDir()
	daemonDir := t.TempDir()
	for _, root := range []string{cliDir, daemonDir} {
		if err := os.Mkdir(filepath.Join(root, ".attn-qa"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ATTN_DATA_DIR", ".attn-qa")
	t.Setenv("ATTN_SOCKET_PATH", filepath.Join(".attn-qa", "attn.sock"))

	if err := os.Chdir(daemonDir); err != nil {
		t.Fatal(err)
	}
	daemonDataDir, err := config.CanonicalRuntimePath(config.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	daemonSocket, err := config.CanonicalRuntimePath(config.SocketPath())
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(cliDir); err != nil {
		t.Fatal(err)
	}
	p := passingProber(t)
	p.daemonHealth = func(context.Context, string) (daemonHealth, error) {
		return daemonHealth{
			Protocol: protocol.ProtocolVersion, Profile: config.ProfileLabel(),
			DataDir: daemonDataDir, SocketPath: daemonSocket, Port: config.WSPort(),
		}, nil
	}
	report := run(context.Background(), Options{Agent: "codex", WorkingDir: cliDir}, p)
	assertCheck(t, report, "routing.daemon", StatusFail, "routing mismatch")
	if report.Routing.DataDir == daemonDataDir || report.Routing.Socket == daemonSocket {
		t.Fatalf("relative overrides unexpectedly converged: cli=%+v daemon=%s/%s", report.Routing, daemonDataDir, daemonSocket)
	}
}

func TestRunKeepsProtocolCheckNamesWhenAppProtocolIsUnavailable(t *testing.T) {
	passing := run(context.Background(), Options{Agent: "codex", WorkingDir: t.TempDir()}, passingProber(t))
	p := passingProber(t)
	p.appProtocol = func(context.Context, string) (string, error) {
		return "", errors.New("app missing")
	}
	failing := run(context.Background(), Options{Agent: "codex", WorkingDir: t.TempDir()}, p)

	want := []string{"protocol.cli_app", "protocol.app_daemon"}
	for _, report := range []Report{passing, failing} {
		var got []string
		for _, check := range report.Checks {
			if strings.HasPrefix(check.Name, "protocol.") {
				got = append(got, check.Name)
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("protocol check names = %v, want %v", got, want)
		}
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
