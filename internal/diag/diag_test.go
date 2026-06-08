package diag

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body %s: %v", url, err)
	}
	return resp, body
}

func TestStart_ServesLoopbackPprofAndVars(t *testing.T) {
	srv, err := Start("127.0.0.1:0", func() Stats {
		return Stats{Sessions: 3, PtyBackend: "worker", WorkerPIDs: map[string]int{"s1": 111, "s2": 222}}
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Close()

	if !strings.HasPrefix(srv.Addr(), "127.0.0.1:") {
		t.Fatalf("endpoint not bound to loopback: %q", srv.Addr())
	}
	base := "http://" + srv.Addr()

	// /debug/vars returns the daemon snapshot merged with runtime heap stats.
	resp, body := get(t, base+"/debug/vars")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/debug/vars status = %d", resp.StatusCode)
	}
	var vars map[string]any
	if err := json.Unmarshal(body, &vars); err != nil {
		t.Fatalf("/debug/vars not JSON: %v\n%s", err, body)
	}
	if got := vars["sessions"]; got != float64(3) {
		t.Errorf("sessions = %v, want 3", got)
	}
	if got := vars["pty_backend"]; got != "worker" {
		t.Errorf("pty_backend = %v, want worker", got)
	}
	if _, ok := vars["worker_pids"].(map[string]any); !ok {
		t.Errorf("worker_pids missing/wrong type: %v", vars["worker_pids"])
	}
	ms, ok := vars["memstats"].(map[string]any)
	if !ok {
		t.Fatalf("memstats missing/wrong type: %v", vars["memstats"])
	}
	for _, k := range []string{"heap_alloc", "heap_sys", "heap_idle", "heap_released", "num_gc"} {
		if _, ok := ms[k]; !ok {
			t.Errorf("memstats.%s missing", k)
		}
	}

	// pprof index and a concrete profile both serve.
	if resp, _ := get(t, base+"/debug/pprof/"); resp.StatusCode != http.StatusOK {
		t.Errorf("/debug/pprof/ status = %d", resp.StatusCode)
	}
	if resp, _ := get(t, base+"/debug/pprof/heap"); resp.StatusCode != http.StatusOK {
		t.Errorf("/debug/pprof/heap status = %d", resp.StatusCode)
	}
}

func TestStart_NilStats(t *testing.T) {
	srv, err := Start("127.0.0.1:0", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Close()

	resp, body := get(t, "http://"+srv.Addr()+"/debug/vars")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/debug/vars status = %d", resp.StatusCode)
	}
	var vars map[string]any
	if err := json.Unmarshal(body, &vars); err != nil {
		t.Fatalf("/debug/vars not JSON: %v", err)
	}
	if got := vars["sessions"]; got != float64(0) {
		t.Errorf("sessions = %v, want 0 with nil stats", got)
	}
}

func TestStart_BadAddr(t *testing.T) {
	if _, err := Start("definitely-not-an-address", nil); err == nil {
		t.Fatal("expected error for invalid bind address")
	}
}

func TestClose_NilSafe(t *testing.T) {
	var s *Server
	if err := s.Close(); err != nil {
		t.Errorf("nil Close = %v, want nil", err)
	}
	if got := s.Addr(); got != "" {
		t.Errorf("nil Addr = %q, want empty", got)
	}
}
