# Diagnostics: pprof + /debug/vars

attn ships an **opt-in, loopback-only** diagnostics endpoint for measuring memory
and CPU. It is **off by default** and binds `127.0.0.1` only, so it never adds
remote attack surface.

## Enable

Set `ATTN_PPROF` in the daemon's environment:

| `ATTN_PPROF`        | Effect                              |
| ------------------- | ----------------------------------- |
| unset / `0` / `off` | disabled (default)                  |
| `1` / `on` / `true` | enabled on `127.0.0.1:6060`         |
| `6061`, `:6061`     | enabled on that loopback port       |

The daemon logs `diagnostics endpoint listening on http://127.0.0.1:<port>/` on
startup. The simplest controlled setup for a measurement run is a manual daemon
on a throwaway profile (its own socket/port/data dir, never touches prod):

```bash
env ATTN_PROFILE=perf ATTN_PPROF=6060 attn daemon
```

To profile the live app daemon instead, set `ATTN_PPROF` in the environment it is
launched from.

## Endpoints

- `GET /debug/vars` — JSON snapshot: Go `runtime.MemStats` heap fields
  (`heap_alloc`, `heap_sys`, `heap_idle`, `heap_released`, `num_gc`, …),
  goroutine/CPU counts, PTY session count, the active `pty_backend`, and
  `worker_pids` (sessionID → worker subprocess PID).
- `GET /debug/pprof/` — standard Go pprof index (`heap`, `goroutine`, `profile`,
  `trace`, …).

## Measurement recipes

```bash
# Live heap/runtime snapshot
curl -s http://127.0.0.1:6060/debug/vars | jq

# Heap profile, symbolized
go tool pprof -top http://127.0.0.1:6060/debug/pprof/heap

# 30s CPU profile
go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=30
```

### Per-session RSS (the dominant memory locus)

The default `worker` PTY backend runs **one subprocess per session**, so the bulk
of per-session memory lives in those child PIDs, not the daemon heap. Sum the
daemon RSS plus every worker PID from `/debug/vars`:

```bash
# daemon + all worker subprocesses
curl -s http://127.0.0.1:6060/debug/vars \
  | jq -r '.pid, (.worker_pids[]|tostring)' \
  | xargs ps -o pid,rss,command -p
```

`rss` is in KiB. This is the measurement to capture before/after each memory
workstream (e.g. WS-1 terminal virtualization, WS-2 scrollback shrink) with a
fixed scenario such as 8 sessions, 2 streaming.
