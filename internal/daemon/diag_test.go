package daemon

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestMaybeStartDiagServer_DisabledByDefault(t *testing.T) {
	t.Setenv("ATTN_PPROF", "")
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	d.maybeStartDiagServer()
	if d.diagServer != nil {
		_ = d.diagServer.Close()
		t.Fatal("diag server started with ATTN_PPROF unset")
	}
}

func TestMaybeStartDiagServer_EnabledServesLoopback(t *testing.T) {
	port := freeLoopbackPort(t)
	t.Setenv("ATTN_PPROF", strconv.Itoa(port))
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	d.maybeStartDiagServer()
	if d.diagServer == nil {
		t.Fatal("diag server did not start with ATTN_PPROF set")
	}
	defer d.diagServer.Close()

	addr := d.diagServer.Addr()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("diag server not on loopback: %q", addr)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/debug/vars")
	if err != nil {
		t.Fatalf("GET /debug/vars: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/debug/vars status = %d", resp.StatusCode)
	}
	var vars map[string]any
	if err := json.Unmarshal(body, &vars); err != nil {
		t.Fatalf("/debug/vars not JSON: %v", err)
	}
	// NewForTesting uses the embedded backend → no worker subprocesses.
	if vars["pty_backend"] != "embedded" {
		t.Errorf("pty_backend = %v, want embedded", vars["pty_backend"])
	}
	if vars["sessions"] != float64(0) {
		t.Errorf("sessions = %v, want 0", vars["sessions"])
	}
}

func TestDiagStats_EmbeddedBackend(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	stats := d.diagStats()
	if stats.PtyBackend != "embedded" {
		t.Errorf("PtyBackend = %q, want embedded", stats.PtyBackend)
	}
	if stats.Sessions != 0 {
		t.Errorf("Sessions = %d, want 0", stats.Sessions)
	}
	if len(stats.WorkerPIDs) != 0 {
		t.Errorf("WorkerPIDs = %v, want empty", stats.WorkerPIDs)
	}
}
