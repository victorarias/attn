// Package diag exposes an opt-in, loopback-only diagnostics endpoint
// (net/http/pprof profiles plus a /debug/vars JSON snapshot) used to measure
// attn's memory and CPU footprint. It is disabled unless ATTN_PPROF is set (see
// config.PprofAddr) and binds 127.0.0.1 only, so it adds no remote attack
// surface.
//
// Importing net/http/pprof registers handlers on http.DefaultServeMux, but the
// daemon serves its own mux and nothing in the process serves the default mux,
// so that registration stays inert; this package re-registers the same handlers
// on a private mux it actually serves.
package diag

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"time"
)

// Stats is the live daemon snapshot the endpoint reports under /debug/vars,
// alongside Go runtime and heap numbers. The worker PTY backend runs one
// subprocess per session, so WorkerPIDs lets a measurement script sum
// per-session RSS (ps/vmmap) — the dominant memory locus. It is empty for the
// embedded backend, which has no separate worker processes.
type Stats struct {
	Sessions   int            `json:"sessions"`
	PtyBackend string         `json:"pty_backend"`
	WorkerPIDs map[string]int `json:"worker_pids,omitempty"`
}

// StatsFunc returns the current daemon stats. It is invoked on each /debug/vars
// request, so it must be cheap and safe for concurrent use. May be nil.
type StatsFunc func() Stats

// Server is a running diagnostics endpoint. The zero value is not usable; obtain
// one from Start.
type Server struct {
	httpServer *http.Server
	addr       string
	stats      StatsFunc
}

// Start binds a loopback HTTP server at addr and serves net/http/pprof plus a
// /debug/vars JSON snapshot on a private mux (http.DefaultServeMux is left
// untouched). addr should be a loopback address; callers resolve it via
// config.PprofAddr, which always yields 127.0.0.1. Pass "127.0.0.1:0" to bind an
// ephemeral port and read it back with Addr. stats may be nil.
func Start(addr string, stats StatsFunc) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("diag: listen %s: %w", addr, err)
	}
	s := &Server{addr: ln.Addr().String(), stats: stats}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/vars", s.handleVars)
	mux.HandleFunc("/", s.handleIndex)

	s.httpServer = &http.Server{Handler: mux}
	go func() { _ = s.httpServer.Serve(ln) }()
	return s, nil
}

// Addr is the resolved loopback address the endpoint is bound to (host:port).
func (s *Server) Addr() string {
	if s == nil {
		return ""
	}
	return s.addr
}

// Close gracefully shuts the endpoint down. Safe to call on a nil *Server.
func (s *Server) Close() error {
	if s == nil || s.httpServer == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleVars(w http.ResponseWriter, _ *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	var stats Stats
	if s.stats != nil {
		stats = s.stats()
	}

	out := map[string]any{
		"pid":         os.Getpid(),
		"goroutines":  runtime.NumGoroutine(),
		"gomaxprocs":  runtime.GOMAXPROCS(0),
		"num_cpu":     runtime.NumCPU(),
		"go_version":  runtime.Version(),
		"sessions":    stats.Sessions,
		"pty_backend": stats.PtyBackend,
		"worker_pids": stats.WorkerPIDs,
		"memstats": map[string]any{
			"heap_alloc":        ms.HeapAlloc,
			"heap_sys":          ms.HeapSys,
			"heap_idle":         ms.HeapIdle,
			"heap_inuse":        ms.HeapInuse,
			"heap_released":     ms.HeapReleased,
			"heap_objects":      ms.HeapObjects,
			"sys":               ms.Sys,
			"next_gc":           ms.NextGC,
			"num_gc":            ms.NumGC,
			"gc_pause_total_ns": ms.PauseTotalNs,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "attn diagnostics\n\n"+
		"/debug/vars    runtime + session/worker snapshot (JSON)\n"+
		"/debug/pprof/  Go pprof index (heap, goroutine, profile, trace, ...)\n")
}
