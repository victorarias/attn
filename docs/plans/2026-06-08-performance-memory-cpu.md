# attn performance roadmap — memory (P1) + CPU (P2)

Produced 2026-06-08 by a multi-agent audit (8 subsystem finders → per-finding adversarial verification → synthesis; 42 findings confirmed, 0 rejected). Memory is priority 1, CPU priority 2.

**Live baseline (measured `ps`, prod app):** total attn ≈ **782 MB** — pty-workers **585 MB / ~75%** (avg 73 MB, peak 109 MB; idle ~14 MB), daemon ~77–111 MB, frontend ~85 MB. The prize is **per-session footprint**, not the daemon heap.

## Measured baseline — 2026-06-09 (dev app, post WS-1/2/4/7, shell sessions)

Captured with `app/scripts/real-app-harness/scenario-perf-baseline.mjs` (8 `shell`
sessions; shells isolate attn's OWN footprint from external claude/codex agent
memory) on commit `e9c61826`, via the WS-7 diag endpoint (`ATTN_PPROF`). Full
process-tree RSS including the launchd-reparented WebKit processes.

**IDLE @ 8 sessions = 805 MB.** attn-own (excludes the 8 shells = the user's
workload, 115 MB):

| class | RSS | note |
| --- | --- | --- |
| WebKit WebContent | **250 MB** | WASM terminal models + canvas + JS heap |
| WebKit GPU | **181 MB** | GL textures incl. 2048² atlas per live renderer |
| app (Tauri main) | 101 MB | |
| pty-workers (×8) | 115 MB | **14.3 MB each = Go-runtime floor, NOT scrollback** |
| daemon | **27 MB** | heap inuse only ~6 MB |
| WebKit networking | 16 MB | |

**Frontend (app + WebKit) = 548 MB ≈ 79% of attn-own RSS.** The per-session
prize is the **frontend terminal**, not the worker/daemon — the prod "75% in
pty-workers" figure was agent memory inside the worker PIDs, not attn structure.

**Streaming spike (NEW, biggest single effect):** streaming high-throughput PTY
output into 2 panes for 20 s drove **WebContent 250 → 1148 MB and it did NOT
recover** (805 → 1604 MB total, retained post-stream). Mechanism: the Ghostty
WASM scrollback heap grows and WASM linear memory never shrinks; only pane
teardown (WS-1) reclaims it. **Daemon stayed ~28 MB; daemon CPU during the
stream was 0.2 % (40 ms / 20 s) — WS-4 already flattened the daemon datapath.**

### What the data changes

- **WS-1 (frontend virtualization) is the whole memory game** and is already
  shipped. The remaining high-value memory work is all frontend: bound the
  streaming WASM-heap balloon, shrink the 2048² atlas (fe-term-2), and confirm
  WS-1's win with a warm-set A/B.
- **WS-3 (daemon GC cap): DROP for memory.** 27 MB RSS / 6 MB heap — nothing to
  reclaim. (A high `GOMEMLIMIT` safety ceiling is fine but is not a win.)
- **WS-2 step 2 (lazy ring buffer): DROP.** Worker RSS is the Go runtime floor;
  scrollback is already <1 MiB. Not worth a PR.
- **CPU (P2):** daemon streaming path already cheap post-WS-4. WS-5 (idle
  fork/wakeup gating) stays valid as an idle-battery win; WS-6 (transcript
  tail-read) is modest. The dominant streaming CPU is frontend rendering (out of
  scope of the Go daemon workstreams).

## Status tracker

- [x] **WS-2 step 1 — shrink PTY scrollback 8 MiB → 1 MiB** (`internal/pty/manager.go`). Measured **7 MiB/session idle, 14 MiB/session full**. Shipped 2026-06-08.
- [x] **pty-2 — debug PTY capture off by default** (`internal/ptyworker/debug_capture.go`). Saves up to ~16–22 MiB per claude/codex worker. Shipped 2026-06-08.
- [x] **WS-1 — virtualize off-screen terminal workspaces** (largest single lever, ~200+ MiB). Shipped 2026-06-08: keep active + N recent workspaces warm (default 3), tear down the rest, rehydrate via `same_app_remount` replay. Runtime-configurable via `window.attnSetWarmWorkspaces(n)` / localStorage. Unmount-only (frees WASM+WebGL); daemon-stream detach is a follow-up.
- [x] **WS-7 — guarded loopback pprof + expvar** (measurement enabler). Shipped 2026-06-08 (#283).
- [x] **WS-4 — kill per-PTY-chunk synchronous logging + preview allocs.** Shipped 2026-06-08 (#281). Verified: daemon CPU 0.2% under 2-session stream.
- [~] **NEW — bound the frontend streaming WASM-heap balloon** (biggest measured memory effect). **Cheap fix RULED OUT by experiment 2026-06-09:** lowering `TERMINAL_SCROLLBACK_LINES` 50000→1000 (50×) left the balloon essentially unchanged — `seq 1 20000000` into 2 panes still retained **1688 MB** (vs 1598 MB at 50000). Mechanism confirmed: ghostty-web 0.4.0 does **not** effectively trim/free WASM scrollback during ingestion (the daemon already paces output 1 s/chunk, yet it still balloons), and WASM linear memory is one-way. **Therefore the scrollback cap is not a lever, and teardown (WS-1) is the ONLY reclaim path.** This also means any long-lived pane grows unboundedly, not just runaway-output cases → elevates warm-set tuning. Remaining options: tighter warm set / reclaim inflated backgrounded panes (needs warm-set A/B), or a memory-pressure remount of inflated panes (high risk, #7).
- [x] **fe-term-2 — shrink 2048² atlas. SHIPPED 2026-06-09 (PR #286), grow-on-demand.** The atlas was eagerly + fully allocated per renderer — 2048² backing canvas + 2048² RGBA GL texture = ~32 MB/renderer fixed, independent of glyph count. Now starts at 1024² (~8 MB) and doubles (capped 2048²) only when a glyph-heavy session overflows it; `resetAtlas` reuses once at the cap. **Measured (8 shell sessions, idle, warm-set 3): total RSS 825→724 MB, WebKit 457→355 MB, GPU 181→90 MB (−91 MB GPU / −101 MB total).** Verified rendering through a real grow (3000 distinct CJK glyphs → grow to 2048², all render correctly). Render-only; invariants #6/#7 untouched. Still TODO: confirm WS-1 win via warm-set A/B.
- [ ] WS-5 — gate idle background loops on client presence (idle-battery win; not a throughput lever).
- [ ] WS-6 — transcript tail-read instead of full-file scan (modest per-classification CPU).
- [~] ~~WS-3 — daemon GC cap~~ **DROPPED for memory by 2026-06-09 data** (daemon 27 MB RSS / 6 MB heap).
- [~] ~~WS-2 step 2 — lazy ring buffer~~ **DROPPED by data** (worker RSS is Go-runtime floor, not scrollback).

---

## Executive summary

attn's biggest memory wins are NOT in the long-lived daemon heap — they're in **per-session footprint** that multiplies across many concurrent sessions, in two places: (1) the **frontend keeps a full Ghostty WASM model + WebGL renderer (≈32 MiB fixed atlas+texture, plus growing scrollback) mounted for every session**, even though only one is visible; and (2) each **PTY worker subprocess eagerly commits an 8 MiB ring buffer (plus a second buffer that grows to 8 MiB) to ever serve at most 256 KiB**. Both scale linearly with session count, which the daemon is explicitly built to host many of.

The daemon heap itself is dominated by *transient* garbage (PR poll, broadcast snapshots, transcript scans), so it benefits from a soft GC cap (`GOMEMLIMIT`) more than from removing any single retained structure — but that cap is unmeasured today and must be sized against a real live-set, which means **we should land measurement (pprof/expvar) before tuning**.

On CPU, the cleanest universal wins are **removing per-PTY-chunk synchronous disk logging and string/preview allocations** that run regardless of `DEBUG` (daemon side and worker side), and **gating idle background work** (branch monitor forking 3N git processes every 15s with zero clients; markdown/transcript pollers waking forever). The single highest-frequency CPU amplifier is the per-output-chunk log+preview on the PTY datapath.

A recurring theme across the audit: several "daemon RSS" headline numbers were **misattributed** — the default `worker` PTY backend means scrollback and debug-capture buffers live in **separate per-session child processes**, not the daemon. The roadmap reflects the corrected locus.

---

## Biggest levers (memory, then CPU)

**Single biggest MEMORY lever: virtualize off-screen terminals (fe-term-1 + fe-term-2 combined).**
Every open session keeps a live `GhosttyTerminal` (WASM model with up to 50k-line scrollback) + a `WebGlTerminalRenderer` whose constructor unconditionally allocates a 2048×2048 RGBA atlas canvas (~16 MiB process bitmap) **and** a 2048×2048 GPU texture (~16 MiB VRAM) — verified at `GhosttyWebGlRenderer.ts:234-245`, mounted per pane at `SessionTerminalWorkspace/index.tsx:677-688` with inactive workspaces only hidden via `display:none` (`App.css:196`). At ~8 sessions / 7 backgrounded that's ≈224 MiB of fixed atlas+texture alone (≈half RSS, ≈half VRAM), plus variable scrollback. This is the largest reclaimable per-session footprint in the whole app, and the code already supports teardown+replay rehydration. Shrinking the atlas default (term-2) is a cheaper partial win that also helps the documented WKWebView WebGL-context-starvation freezes.

**Runner-up MEMORY lever: shrink PTY scrollback buffers (pty-1).**
Each worker subprocess does `NewRingBuffer(8 MiB)` (eager `make([]byte, size)`, `ringbuffer.go:19`) **and** `NewReplayLog(8 MiB)` (lazy, grows under output) — `manager.go:215-216` — to serve a downstream clip of 256 KiB. Lowering `DefaultScrollbackSize` (`manager.go:25`) to ~512 KiB–1 MiB removes ~7–7.5 MiB committed per session on spawn, across all worker processes. Low-risk first step; no protocol/consumer changes.

**Single biggest CPU lever: stop per-PTY-chunk disk logging + preview allocation (ws-1 + pty-3 + hub-1, same root pattern).**
On the highest-frequency path (one event per output chunk per attached client), the daemon calls `d.logf("pty_output forward: ... preview=%q", ..., previewBinaryForLog(...))` unconditionally — `ws_pty.go:692` — and `d.logf`→`Info`→`log()` is **not level-gated** (`logging.go:75-84`), so it takes the global log mutex and does a synchronous `os.File` write for every chunk even with `DEBUG` unset. The same un-gated per-chunk pattern repeats in the worker subprocess (`ptyworker/runtime.go:668-676`, whose stderr is an unrotated per-session `.log`) and in the hub relay (`internal/hub/manager.go:686,705` — a *second* full JSON unmarshal + disk write per relayed chunk). Gating these behind a cached debug flag removes a mutex-held disk write + a `string(data)`/preview allocation per chunk and de-contends the global log mutex.

---

## Ranked workstreams

Ordered biggest-memory-first. Each is independently shippable and testable.

### WS-1 — Virtualize off-screen terminal workspaces (MEMORY, top priority)
- **Impact (memory):** Highest in the app. Frees ≈32 MiB fixed (atlas canvas RSS + GPU texture) **per backgrounded pane**, plus that pane's WASM scrollback. ~7 backgrounded sessions ≈ 200+ MiB reclaimed; scales linearly. Also relieves WKWebView live-WebGL-context starvation (a known freeze cause, per the code comment at `GhosttyTerminal.tsx:743-756` and commit `5735c2a1`).
- **Effort:** Large. **Risk:** Medium-high (touches focus + PTY-resize authority).
- **Files:** `app/src/App.tsx` (2910 map), `app/src/components/SessionTerminalWorkspace/index.tsx` (677-688 mount gate), `app/src/pty/useGhosttyPaneRuntime.ts` (attach policy `same_app_remount`), `app/src/components/GhosttyTerminal.tsx` (unmount loseContext already present, 757-762).
- **Sketch:** Gate the `<GhosttyTerminal>` mount on `isActiveSession || isRecentlyActive` (keep active + optionally 1 MRU live); render a lightweight placeholder `<div>` otherwise. On re-select, the existing `forceResizeBeforeAttach='same_app_remount'` + replay path rehydrates the pane from daemon scrollback. Drive from `isActiveSession`/`isSessionViewVisible` already passed in.
- **Invariants to preserve (FLAGGED):** Terminal Focus Ownership (AGENTS.md #6) — only focus the newly-active pane, respect utility-vs-main. PTY single-authority + replay-is-provisional (#7) — only the newly-active pane drives `pty_resize` (existing `!isActiveSessionRef.current` guard at `useGhosttyPaneRuntime.ts:162`); keep `suppressResponses` for `source==='attach_replay'` (line 68) so replayed queries don't generate live PTY input. Accept known loss: backgrounded pane's local scroll offset.
- **Tests:** Render test asserting only active (+N MRU) workspaces mount a `GhosttyTerminal`, others render a placeholder. E2E (`utility-terminal-realpty.spec.ts` pattern, `VITE_MOCK_PTY=0 VITE_FORCE_REAL_PTY=1`): switch-away/switch-back rehydrates content via replay, types into the correct PTY without an extra click, emits no stray `pty_resize` from a backgrounded pane.

### WS-2 — Shrink PTY scrollback buffers (MEMORY, high value-to-effort)
- **Impact (memory):** ~7–7.5 MiB removed per session immediately on spawn (incl. probe/short shells), across worker subprocesses. ~10–20 sessions ≈ 70–150 MiB of system RSS spread across processes.
- **Effort:** Small (step 1) / Medium (step 2). **Risk:** Low (step 1).
- **Files:** `internal/pty/manager.go` (25 const), and for step 2: `internal/pty/ringbuffer.go`, `internal/pty/replaylog.go`, `internal/daemon/ws_pty.go` (227-279 flat-replay consumers), `internal/ptyworker/runtime.go` (731 `info.Scrollback`), `internal/pty/session.go` (616-622 startup-query flush).
- **Sketch:** **Step 1 (ship first, low-risk):** lower `DefaultScrollbackSize` from `8*1024*1024` to ~`512*1024`–`1024*1024`. Must stay `>= maxAgentRawReplayBytes` (256 KiB, `ws_pty.go:54`) so clip/`ScrollbackTruncated` semantics are unchanged and Codex fresh-spawn startup-query replay still fits. **Step 2 (optional, later):** make `RingBuffer` allocate lazily (grow to size) so idle/short sessions don't pre-commit; or eliminate the redundancy by keeping only `ReplayLog` (it carries per-segment cols/rows required by the geometry invariant) and redirecting every `info.Scrollback` consumer to a `ReplayLog`-derived flat byte stream.
- **Invariants to preserve (FLAGGED):** PTY-geometry/replay-is-provisional — if dropping `RingBuffer`, keep `ReplayLog` for per-segment cols/rows. Cap must stay ≥256 KiB.
- **Tests:** Go test asserting per-session buffer bound (and, for step 2, that eager allocation no longer occurs). Keep `ringbuffer_test`/`replaylog_test` green. E2E attach-replay (Codex fresh spawn) and relaunch-restore still rehydrate with the smaller cap.

### WS-3 — Daemon GC cap + idle scavenge (MEMORY, depends on WS-7 measurement)
- **Impact (memory):** Single-digit to low-double-digit % steady-state daemon RSS. Caps the GOGC=100 ~2× heap ratchet from transient spikes (PR poll JSON, `store.List` snapshots, transcript reads); idle `FreeOSMemory` returns reserved pages while the app is closed. Honest caveat: modern Go on macOS already scavenges spike pages, so this lowers the *ceiling*, it doesn't rescue a permanent floor. Magnitude unmeasured — **do WS-7 first**.
- **Effort:** Small. **Risk:** Medium (a too-tight cap causes GC thrash = CPU regression).
- **Files:** `cmd/attn/main.go` (296-308 `runDaemon`) or a `package daemon` init.
- **Sketch:** `debug.SetMemoryLimit(softCap)` driven by env `ATTN_MEMLIMIT` with a generous default (≈512 MiB–1 GiB, sized *above* the WS-7-measured live set), and moderate `debug.SetGCPercent(50–75)` (not 10). Optional idle scavenge: piggy-back on the existing 5-min ticker (`daemon.go:1381`), rate-limited, calling `debug.FreeOSMemory()` only when truly idle.
- **Invariants to preserve (FLAGGED):** Idle gate must exclude in-flight classification, not just `wsHub.ClientCount()==0` — clients are routinely absent (app closed) while classifier/transcript/review-loop goroutines run; pausing mid-classification interacts badly with classifier-timestamp work. Gate on zero clients AND no `working` sessions AND no pending classification.
- **Tests:** Wiring test that `SetMemoryLimit` is invoked at bootstrap; unit test that the idle-scavenge gate returns false when a session is `working` or classification is pending. Don't assert runtime GC behavior. Use WS-7's pprof/expvar to measure RSS before/after.

### WS-4 — Kill per-PTY-chunk logging + preview allocations (CPU, top CPU priority)
- **Impact (CPU):** Removes, per output chunk per attached client, a mutex-held synchronous disk write + a `fmt.Sprintf` + a preview build (`string(data)` + 3× `strings.ReplaceAll`), and de-contends the single global `logging.mu` that serializes ALL daemon logging. On a focused streaming agent that's tens-to-hundreds of writes/sec eliminated. Worker side additionally stops an unbounded per-session `.log` from growing forever (a real disk leak). Mostly affects active/attached streams, not idle RSS.
- **Effort:** Small. **Risk:** Low.
- **Files:** `internal/daemon/ws_pty.go` (692-698 output, 717 desync, 741-754 input), `internal/daemon/logging.go` (add gate / `Debugf` route), `internal/ptyworker/runtime.go` (668-676 + wire `cfg.Logf` in `cmd/attn/main.go:278` to honor DEBUG + add `.log` rotation/cap), `internal/daemon/worker.go` (649 eager `previewBytesForLog` on input; 455/469-470 log file open).
- **Sketch:** Cache `d.debugLogging := config.DebugLevel() >= LogDebug` at init; wrap each per-chunk verbose `logf` in `if d.debugLogging { ... }` **before** building args (Go evaluates args eagerly, so the `if` is mandatory — a lazy closure isn't enough). Keep error/lifecycle logs (`pty_output marshal/send failed`, desync, attach/detach) unconditional. For the worker: make `cfg.Logf` a no-op when debug off, keep startup/lifecycle/error lines, add size-based rotation for `<sessionID>.log`. Note: the `base64` on this path is the **required wire payload**, not removed by gating — only the preview/`string(data)`/`Fprintf`/write go away.
- **Invariants to preserve (FLAGGED):** None of the PTY/protocol invariants — these are log-only edits. Do NOT alter the `sendOutboundBlocking`/backpressure/`stream.Close` logic or base64 payload forwarding.
- **Tests:** With `DEBUG` unset, attaching a subscriber and pushing output produces no growth in `daemon.log` / worker `.log` and no `pty_output`/`pty_input` lines, but send/marshal failures still record. A rotation test that the worker log is capped. Avoid tests that restate that `Fprintf` writes bytes.

### WS-5 — Gate idle background loops on client presence (CPU, strong)
- **Impact (CPU):** Branch monitor (`daemon.go:3092-3132`, 15s ticker) forks **3 git subprocesses per session** + a full `store.List` SQL scan every 15s **with no client gate** — ~3N forks every 15s forever (~17,280·N/day) producing a state change only on a rare manual `git checkout`. Markdown watcher (`tilecontent.go:452`, 750ms) wakes ~115k times/day doing N+1 SQL even with no markdown tile open and no client. PR poll (`daemon.go:2554`, 90s) does 3 GitHub Search HTTP calls/host + detail refresh with no client gate (~2,880/day/host). Gating these collapses idle wakeups/forks/network toward zero when the app is closed.
- **Effort:** Medium (branch monitor) + Small (markdown, PR poll). **Risk:** Low-medium.
- **Files:** `internal/daemon/daemon.go` (branch monitor 3092-3132; PR poll 2554-2659; recompute-on-connect via `scheduleInitialState`/`websocket.go:663`), `internal/daemon/tilecontent.go` (449-505), `internal/daemon/git/git.go` (optional 3→1 fork collapse).
- **Sketch:** Branch monitor — skip the tick when `wsHub.ClientCount()==0`; recompute on client (re)connect and after daemon-driven git ops; back off to ~60s when clients present; optionally collapse the 3 forks into one `git rev-parse --is-inside-work-tree --abbrev-ref HEAD --git-dir`. Markdown — interim: skip the pass when `ClientCount()==0`; better: stop the ticker when zero markdown subscribers and re-arm on `open_markdown`/subscribe. PR poll — **idle backoff, not hard stop** (see flag below): poll 90s when clients present, ~10–15 min when none, immediate poll on first connect.
- **Invariants to preserve (FLAGGED):** PR poll — attn is a **notification app**; before fully gating PR polling, confirm background PR/CI notifications don't depend on polling while no UI client is attached (removing that would be a user-facing functionality removal, an AGENTS.md violation). Prefer idle backoff. Branch monitor — keep detached-HEAD fallback (`--abbrev-ref` returns literal `HEAD`; retain `symbolic-ref`→`rev-parse --short HEAD`) and the `worktrees` substring worktree detection. Recompute on connect is the correctness contract for gating.
- **Tests:** With zero ws clients assert the branch monitor / markdown pass does not enumerate the store (store call-counter); on connect assert a branch recompute + `EventSessionsUpdated` fires when on-disk branch differs. PR poll: assert suppressed/backed-off when `ClientCount==0`, fires once on first connect, resumes 90s while connected — via observable broadcast/call counts on a fake gh client, not internal fields.

### WS-6 — Transcript tail-read instead of full-file scan (CPU, moderate; some GC relief)
- **Impact (CPU):** Each classification (once per assistant turn per session) reads the **entire** transcript from offset 0 and JSON-unmarshals every line several times — `parser.go:172-220`. Real files reach ~23 MiB (Claude) / ~6.8 MiB (Codex); the Claude path can re-read up to ~20× in a 2s retry loop (`claude.go:176-200`). Tail-read (seek to `size-256KB` with full-scan fallback) collapses the common case to a sub-MB read + a few hundred lines: ~10–90× less I/O and allocation per classification. Transient GC relief, not retained RSS — so it sits below the memory workstreams.
- **Effort:** Medium. **Risk:** Medium (boundary correctness). **Bundle with:** tc-2 (stat-skip when size/mtime unchanged), tc-3 (one combined-struct unmarshal/line), tc-5 (single-block fast path) — they're cheap riders once the scan is touched.
- **Files:** `internal/transcript/parser.go` (126-148, 172-220, 339-363), `internal/agent/claude.go` (176-200), reuse seek pattern from `internal/daemon/transcript_watcher.go:320`.
- **Sketch:** Seek to `max(0, size-window)` (window ~256 KiB matching existing `BootstrapBytes`), discard the first partial line, scan the suffix for last assistant + last user within window; widen/fall back to full scan if the window lacks a user OR assistant boundary. Stat-skip the re-parse in the retry loop when size+mtime unchanged. Collapse per-line multi-unmarshal into one combined struct (Claude `message`, Codex `payload`, Copilot `data` — distinct top-level keys, no collision).
- **Invariants to preserve (FLAGGED):** Classifier semantics — last-assistant-only-after-last-user (`parser.go:205`), `minAssistantTimestamp` staleness (`parser.go:208`), Claude UUID dedup / `ErrNoNewAssistantTurn` (`claude.go:185-198`). Classifier timestamp protection is daemon-side (`classificationStartTime` captured at `daemon.go:1921`) and must remain untouched. Multi-line-aware partial-line handling like `readTranscriptDelta`.
- **Tests:** Keep `parser_test.go:254-345` boundary tests green. ADD: last assistant beyond the tail window returns the same result as full scan (proves fallback); a single assistant message spanning > window; a stable file parsed at most once across the retry window; combined-struct output equivalence vs current per-format parsing. A Go benchmark over a large fixture to prove the I/O/unmarshal reduction and guard regression (legitimate benchmark, not a compile-time restatement).

### WS-7 — Measurement: guarded pprof + expvar (ENABLER — do early, before WS-3)
- **Impact:** None directly, but it's the prerequisite to *verify* every memory claim (there is zero pprof/expvar/GOMEMLIMIT today — confirmed: 0 hits across non-test Go). Without it WS-3's cap is a guess and we can't prove WS-1/WS-2 wins.
- **Effort:** Small. **Risk:** Low (must be opt-in / loopback-guarded).
- **Files:** `cmd/attn/main.go` or `package daemon` init; optionally a tiny `attn` CLI subcommand to fetch/dump.
- **Sketch:** Behind an env flag (e.g. `ATTN_PPROF=1`, default off) start `net/http/pprof` on `127.0.0.1:<port>` (loopback only). Add an `expvar` (or a one-line `/debug/vars`) publishing `runtime.MemStats` (HeapAlloc, HeapSys, HeapIdle, NumGC) + session count + per-backend worker count. Optionally a `attn debug heapdump` that hits the local endpoint and writes a profile. Measure RSS via `ps`/`vmmap` against the worker PIDs for per-session footprint (WS-1/WS-2).
- **Invariants to preserve:** None. Keep it strictly loopback + opt-in (no new attack surface). macOS-only fine.
- **Tests:** Start the daemon with the flag, assert the endpoint serves and is bound to loopback; assert it's absent when the flag is unset.

---

## Measurement gap & how to close it

There is **no pprof, no expvar, no `GOMEMLIMIT`/`GOGC` tuning, and no `debug.FreeOSMemory`** anywhere in non-test Go (verified: zero grep hits across `internal/`, `cmd/`). We are currently flying blind on the exact thing prioritized #1. Two specifics make this worse:

1. **Per-session memory lives in child processes.** The default `worker` backend (`daemon.go:506-507`) spawns one `pty-worker` subprocess per session, so the 8 MiB scrollback and debug-capture buffers are in those PIDs, not the daemon. Any RSS measurement must sum the daemon + all worker PIDs (`ps`/`vmmap`), or WS-2's win will look like it "did nothing" to the daemon.
2. **VRAM is invisible to RSS.** Half of WS-1/WS-2's win is GPU texture memory; measure it via the WKWebView/WebGL side (Instruments / `Activity Monitor` GPU, or count live contexts), not just process RSS.

**Smallest change to unblock:** WS-7 (guarded loopback pprof + an `expvar` of `MemStats`). Land it first so WS-1, WS-2, WS-3 can be verified rather than assumed. Then for each memory workstream, capture daemon RSS + summed worker RSS before/after with a fixed scenario (e.g. 8 sessions, 2 streaming) and attach the numbers to the PR.

---

## Lower-priority / deferred

These are real but low severity (mostly transient CPU/GC with no steady-state RSS benefit, or gated behind off-by-default features). Defer unless they ride along a touched file.

- **pty-2 (debug PTY capture ON by default):** trivial — default `ATTN_DEBUG_PTY_CAPTURE` empty to OFF; removes a per-chunk base64 alloc + O(n) prune in worker processes. Update `debug_capture_test.go:11-29`. Worth bundling with WS-4 (same worker-CPU theme). FLAG: keep the opt-in.
- **pty-4 / pty-5 (readLoop chunk alloc, fanOut copy):** per-read/per-chunk transient allocations; CPU-only, modest. Optional rider on any `session.go` work. FLAG (pty-5): preserve the embedded backend's own copy and document the "subscriber send must not retain the slice" invariant.
- **pty-6 (state detector full pipeline per chunk):** CPU-only; applies to **claude/copilot** (NOT codex — it has no detector). Implement as a raw-byte working-signature fast-path short-circuit. FLAG: do NOT blanket-throttle `Observe` — it's incremental and would drop transition frames; only short-circuit unambiguous working pulses inside the pulse window.
- **store-1 (SQLite pool/PRAGMAs):** magnitude is a few hundred KB (live DB is ~356 KiB), not 6–10 MB. SAFE subset only: `SetMaxOpenConns(2–4)`, `SetMaxIdleConns(1)`, `SetConnMaxIdleTime`. FLAG: do NOT add `_foreign_keys=on` (would activate currently-inert `ON DELETE CASCADE` and change delete semantics) or WAL — those need their own audited PR.
- **store-2/3/4/5 (prepared statements, SetPRs tx, handleTodos→Get, session_id index):** CPU/GC only. Highest-value slice is `handleTodos` calling `store.List("")` then linear-scanning for one id → replace with `Get(msg.ID)` (`daemon.go:2421-2437`). `SetPRs` single-tx + drop the redundant trailing `ListPRs("")` is a clean idle-IO win. FLAG (store-2/4): use prepared-statement caching, NOT a read-through Get cache (would violate classifier-timestamp protection at `store.go:489`).
- **ws-2/ws-3/ws-4/ws-5 (per-client encode dedup, fat WebSocketEvent marshal, send-buffer bound, hub relay re-parse):** ws-3 (purpose-built 4-field struct replacing the 71-field `WebSocketEvent` marshal for `EventPtyOutput`) is the best of these — real GC relief on the hot outbound path; FLAG: produce byte-identical JSON (no ProtocolVersion bump) and do NOT pool the payload buffer naively (it's enqueued on a 256-cap channel and outlives the call — data race). ws-2 is neutral for the single-desktop user (M=1). ws-4 is "document the bound" more than a code change.
- **hub-1/hub-2/hub-3 (remote relay):** only active with SSH endpoints configured (off by default). hub-1 (gate the per-chunk relay log+unmarshal) is the worthwhile one if remote is used — pairs with WS-4. hub-3 route caches are bounded-in-practice (reset on reconnect).
- **fe-socket-1/fe-socket-4/fe-term-5/fe-term-6 (React re-render storm):** the *stated* fe-socket-1 fix (scope App's selectors) is near-useless because the hot event mutates `daemonSessions` which App legitimately forwards. The **real** lever is `React.memo` on `Sidebar`/`Dashboard`/`SessionTerminalWorkspace` + pushing store slices into memoized leaves + identity-preserving `syncFromDaemonSessions` (return prior array ref when all elements are reference-equal) — a larger, careful refactor of the 3207-line `App.tsx`. CPU-only, bursty (per state transition, NOT per byte — pty_output bypasses React), no memory benefit. fe-term-3 (background panes still WebGL-render on output) is a good small CPU win and is **subsumed by WS-1** once off-screen panes are torn down; if WS-1 keeps an MRU pane warm, still gate `renderSurface` on `isActiveSession`. FLAG: any memo work must preserve Focus Ownership (#6) and the `setWorkspaceRef(id)` callback-ref identity churn.
- **fe-socket-2 (gitOperations dead state):** trivial cleanup — unbounded-but-rare (one tiny object per worktree delete), zero consumers. Remove the state + return field; update `useDaemonSocket.test.tsx:305,328`.
- **fe-socket-3 (ptyPerf per-message alloc):** trivial — gate `recordWsJsonParse` behind a default-off flag; tiny gen-0 allocations only.
- **fe-socket-5 (rate_limited uncanceled setTimeout):** trivial — store the timer in a ref, clear-before-set (also dedups the 90s re-broadcasts), clear in effect cleanup. Bounded by the rate-limit window.
- **tc-4/tc-6 (watcher delta string copy; discovery walks):** CPU-only, modest; tc-4's "halve the unmarshal" claim is false for Claude (its `HandleLine` is a no-op). tc-6 is cold-path (launch window only).

---

## Invariant watch-list (for implementers)

Findings whose fixes touch a protected invariant — read before implementing:

- **WS-1 (terminal virtualization):** Focus Ownership (#6) + PTY single-authority/replay-is-provisional (#7). Only the newly-active pane drives `pty_resize`; keep `suppressResponses` for `attach_replay`.
- **WS-2 step 2 (drop a PTY buffer):** PTY-geometry — `ReplayLog` must remain (per-segment cols/rows). Cap ≥256 KiB to keep clip/`ScrollbackTruncated` semantics.
- **WS-3 (idle scavenge):** Don't pause mid-classification — gate on no clients AND no `working` session AND no pending classification (indirectly protects classifier-timestamp work).
- **WS-5 (PR poll gating):** Do NOT remove background notification behavior (user-facing functionality / AGENTS.md). Prefer idle backoff. Branch monitor: preserve detached-HEAD fallback + worktree detection.
- **WS-6 (transcript tail-read):** Preserve last-assistant-after-last-user, `minAssistantTimestamp` staleness, Claude UUID dedup. Classifier-timestamp protection stays untouched (daemon-side).
- **store-1 deferred:** Do NOT enable `_foreign_keys=on` or WAL casually — changes delete semantics; separate audited PR with cascade tests.
- **store-2/store-4 deferred:** Prepared-statement caching, not a read-through Get cache (classifier-timestamp protection reads live `state_updated_at`).
- **ws-3 deferred:** Byte-identical JSON (no ProtocolVersion bump); do NOT naively pool the marshal buffer (outlives the call on a 256-cap channel).
- **fe-term-5/6 deferred (memo):** Preserve Focus Ownership; memoize the `setWorkspaceRef(id)` ref callback or the imperative handle churns.
- **General:** No finding here requires a protocol message-shape change, so **no `ProtocolVersion` bump is needed** for any workstream as scoped. If any implementation drifts into changing a protocol shape, stop and follow the versioning steps in AGENTS.md.