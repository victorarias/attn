# Plan: Store single implementation (delete the map fallback)

From the 2026-07-01 architecture review. Line references are as of commit
`80c62f6b` — re-anchor by symbol name.

## Goal

`internal/store` carries a complete second implementation: 116
`if s.db == nil` branches across 10 files switch between SQLite bodies and a
parallel map-based store (`sessions`, `agentMetadata`, `profileRoles`,
`workspaces`, `recentLocations`, `agentDriverRuns` fields). The map path only
executes when `OpenDB(":memory:")` fails inside `New()` (`store.go` ~41) — i.e.
when SQLite itself is broken, which no test exercises and production never hits
(`store.New()` already IS the in-memory SQLite store; the daemon's
"persistence degraded" fallback at `daemon.go` ~454 calls `New()`, not the maps).

Delete the parallel universe. Every method keeps one body. This is a deletion
that concentrates nothing elsewhere — the textbook pass of the deletion test.

**Behavior-preserving**: no caller sees a different result. Only the
never-executed branch dies.

## Architecture Map

```text
Current:
store.New() -> OpenDB(":memory:") ok?  -> Store{db}          (every real run)
                                  fail? -> Store{maps...}     (dead: needs broken SQLite)
each method: if s.db == nil { map body } else { SQLite body }   x116

Target:
store.New() -> OpenDB(":memory:") ok?  -> Store{db}
                                  fail? -> panic("attn: cannot open in-memory sqlite: <err>")
each method: one SQLite body

Unchanged:
daemon.New: NewWithDB(dbPath) fail -> store.New() + persistence-degraded warning
```

Panic is correct here: `:memory:` failing means the compiled-in SQLite driver is
broken — the binary cannot function, and limping along with a silently
non-persistent half-store is worse than a clear crash at startup.

## Boundaries

- `New() *Store` keeps its signature (callers: `testharness.go`, several daemon
  tests, `daemon.go` fallback, `NewForTesting`/`NewWithGitHubClient`). Do not
  change it to return an error — that fans out to every test for zero value.
- `NewWithDB` / `NewWithPersistence` / `OpenDB` are untouched.
- Migrations (`sqlite.go`) are untouched by this plan.

## Implementation Steps

- [ ] Single PR (mechanical, large-but-boring diff is expected and fine):
  - [ ] In `New()` (`store.go` ~41): replace the map-construction fallback with
        `panic(fmt.Sprintf("attn: cannot open in-memory sqlite: %v", err))`.
  - [ ] Delete the map fields from the `Store` struct (`sessions`,
        `agentDriverRuns`, `agentMetadata`, `profileRoles`, `workspaces`,
        `recentLocations`) and any now-unused mutexes that existed only for them.
        Keep fields that the SQLite path also uses — check each field's readers
        before deleting (`grep -n '<field>' internal/store/*.go`).
  - [ ] For each of the 116 `if s.db == nil` branches: delete the map arm, keep
        the SQLite arm, unindent. Work file by file; after each file run
        `go build ./internal/store && go test ./internal/store -count=1`.
  - [ ] Delete map-only helpers that lose their last caller (compiler +
        `go vet` will name them; also run a final
        `grep -rn 'sessions\[' internal/store` style sweep per deleted field).
  - [ ] Fix the AGENTS.md architecture snapshot line "`internal/store`:
        SQLite-backed state with in-memory cache" → "SQLite-backed state
        (`:memory:` when no DB path)". It was never a cache.

## Decisions

- Panic over error-return in `New()`: keeps ~15 call sites unchanged; the
  failure mode is "binary is broken", not a runtime condition to handle.
- Role interfaces (`SessionStore`, `TicketStore`, …) and store change events are
  deliberately NOT in this plan — they need the design conversation from the
  review's grilling loop. This plan is the mechanical, unambiguous slice.

## Verification

```bash
go build ./...
go test ./internal/store/... -count=1
go test ./internal/daemon -count=1     # no -race on the whole package (known
                                       # TestGitStatusScheduler race; scope with -run)
make test                              # full Go suite
```

Structural asserts (must hold at the end):

```bash
grep -rc 'if s.db == nil' internal/store/*.go | awk -F: '{s+=$2} END {print s}'   # -> 0
grep -n 'sessions\s*map\[string\]\*protocol.Session' internal/store/store.go      # -> no output
```

Behaviors that must survive:

1. `store.New()` still yields a fully working store for every test that uses it
   (the whole daemon test suite is the regression net).
2. Daemon persistence-degraded startup: `NewWithDB` failure still produces a
   working in-memory store + `warnPersistenceDegraded` warning (there are
   existing tests around startup warnings — they must stay green unmodified).
3. Clone-on-read semantics: `Get` still returns copies, never internal pointers
   (existing tests cover this; don't touch `cloneSession`).

## Follow-ups

- Role interfaces at point of use (the `ticketnotify` 2-method `EventStore` is
  the exemplar) — do after this lands; the surface is easier to see with one body.
- Typed change events from the store + a single daemon broadcast pump (replaces
  ~40 `broadcast*` helpers). Needs design; see the review's store card.
