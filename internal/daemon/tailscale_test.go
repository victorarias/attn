package daemon

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fakeTailscaleCLI struct {
	calls [][]string
	run   func(args []string) ([]byte, error)
}

func (f *fakeTailscaleCLI) Run(_ context.Context, args ...string) ([]byte, error) {
	call := append([]string(nil), args...)
	f.calls = append(f.calls, call)
	if f.run == nil {
		return nil, nil
	}
	return f.run(call)
}

func TestDaemon_ReconcileTailscaleServe_EnableUsesExistingDevice(t *testing.T) {
	cli := &fakeTailscaleCLI{}
	serveStatusCalls := 0
	cli.run = func(args []string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "status --json":
			return []byte(`{"BackendState":"Running","Self":{"DNSName":"macbook.tail1bfe77.ts.net."}}`), nil
		case "serve status --json":
			serveStatusCalls++
			if serveStatusCalls == 1 {
				return []byte(`{}`), nil
			}
			return []byte(`{"Web":{"macbook.tail1bfe77.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9849"}}}}}`), nil
		case "serve --bg --https=443 --set-path=/ 127.0.0.1:9849":
			return []byte("ok"), nil
		default:
			t.Fatalf("unexpected tailscale command: %q", strings.Join(args, " "))
			return nil, nil
		}
	}

	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.tailscale = newTailscaleRuntimeWithCLI(cli)

	snapshot := d.reconcileTailscaleServe(true)
	if snapshot.status != tailscaleStatusRunning {
		t.Fatalf("snapshot.status = %s, want %s", snapshot.status, tailscaleStatusRunning)
	}
	if snapshot.domain != "macbook.tail1bfe77.ts.net" {
		t.Fatalf("snapshot.domain = %q, want device DNS name", snapshot.domain)
	}
	if len(cli.calls) != 5 {
		t.Fatalf("tailscale call count = %d, want 5", len(cli.calls))
	}
}

func TestDaemon_ReconcileTailscaleServe_DisableClearsOnlyAttnRoot(t *testing.T) {
	cli := &fakeTailscaleCLI{}
	serveStatusCalls := 0
	cli.run = func(args []string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "status --json":
			return []byte(`{"BackendState":"Running","Self":{"DNSName":"macbook.tail1bfe77.ts.net."}}`), nil
		case "serve status --json":
			serveStatusCalls++
			if serveStatusCalls == 1 {
				return []byte(`{"Web":{"macbook.tail1bfe77.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9849"}}}}}`), nil
			}
			return []byte(`{}`), nil
		case "serve --https=443 --set-path=/ off":
			return []byte("ok"), nil
		default:
			t.Fatalf("unexpected tailscale command: %q", strings.Join(args, " "))
			return nil, nil
		}
	}

	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.tailscale = newTailscaleRuntimeWithCLI(cli)

	snapshot := d.reconcileTailscaleServe(false)
	if snapshot.status != tailscaleStatusDisabled {
		t.Fatalf("snapshot.status = %s, want %s", snapshot.status, tailscaleStatusDisabled)
	}
	if len(cli.calls) != 5 {
		t.Fatalf("tailscale call count = %d, want 5", len(cli.calls))
	}
}

func TestDaemon_ReconcileTailscaleServe_DetectsConflict(t *testing.T) {
	cli := &fakeTailscaleCLI{}
	cli.run = func(args []string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "status --json":
			return []byte(`{"BackendState":"Running","Self":{"DNSName":"macbook.tail1bfe77.ts.net."}}`), nil
		case "serve status --json":
			return []byte(`{"Web":{"macbook.tail1bfe77.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`), nil
		default:
			t.Fatalf("unexpected tailscale command: %q", strings.Join(args, " "))
			return nil, nil
		}
	}

	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.tailscale = newTailscaleRuntimeWithCLI(cli)

	snapshot := d.reconcileTailscaleServe(true)
	if snapshot.status != tailscaleStatusConflict {
		t.Fatalf("snapshot.status = %s, want %s", snapshot.status, tailscaleStatusConflict)
	}
	if !strings.Contains(snapshot.lastError, "127.0.0.1:3000") {
		t.Fatalf("snapshot.lastError = %q, want conflicting target", snapshot.lastError)
	}
	if len(cli.calls) != 2 {
		t.Fatalf("tailscale call count = %d, want 2", len(cli.calls))
	}
}

func TestTailscaleSnapshotFromError_MissingCLI(t *testing.T) {
	snapshot := tailscaleSnapshotFromError(exec.ErrNotFound)
	if snapshot.status != tailscaleStatusUnavailable {
		t.Fatalf("snapshot.status = %s, want %s", snapshot.status, tailscaleStatusUnavailable)
	}
}

func TestTailscaleSnapshotFromError_ReportsOtherFailures(t *testing.T) {
	snapshot := tailscaleSnapshotFromError(errors.New("boom"))
	if snapshot.status != tailscaleStatusError {
		t.Fatalf("snapshot.status = %s, want %s", snapshot.status, tailscaleStatusError)
	}
	if snapshot.lastError != "boom" {
		t.Fatalf("snapshot.lastError = %q, want boom", snapshot.lastError)
	}
}
