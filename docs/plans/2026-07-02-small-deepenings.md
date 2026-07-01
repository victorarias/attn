# Plan: Small deepenings (dead code, duplicated primitives, migration dispatch)

From the 2026-07-01 architecture review — the bounded wins that don't warrant
their own plan. Each item is independent; land them as separate small PRs (or
batch A+B, which are trivial). Line references as of `80c62f6b` — re-anchor by
symbol name.

## A. Delete the dead journal writers

`internal/notebook/store.go` `AppendJournal` (~153) and
`AppendJournalEntryOnce` (~173) have **zero production callers** — verify first:

```bash
grep -rn 'AppendJournal' --include='*.go' internal/ cmd/ | grep -v _test | grep -v 'internal/notebook/store.go'
# must print nothing; if it prints something, STOP — the premise changed
```

Their doc comments describe dispatch-outcome wiring that doesn't exist. The
journal is agent-written by design (the keeper's narrate duty writes via a
headless agent; see `internal/daemon/notebook_narration.go` ~472 and
`docs/glossary.md`).

- [ ] Delete both methods + their tests in `internal/notebook/store_test.go`
      (~159-422 region; delete only the tests for these two methods).
- [ ] Keep `AppendInbox` — it has a live caller (`internal/daemon/notebook.go` ~352).
- [ ] Add one sentence to the `notebook` package doc: the journal is written by
      agents through the daemon's narration path, not through a store API.

Verify: `go build ./... && go test ./internal/notebook -count=1`.

## B. One `writeAtomic`

Three verbatim copies: `internal/notebook/store.go`,
`internal/fsdoc/store.go` (~290, self-described as "a focused copy"),
`internal/tasks/store.go` (~204).

- [ ] Create `internal/atomicfile` with the one function (same signature as the
      existing copies; keep the tmp-file + rename semantics EXACTLY — atomic
      rename is what makes editor/agent concurrent access safe).
- [ ] Point all three stores at it; delete the copies.

Verify:

```bash
go test ./internal/notebook ./internal/fsdoc ./internal/tasks -count=1
grep -rn 'func writeAtomic' internal/ --include='*.go' | grep -v atomicfile   # -> no output
```

## C. Migration versions live once

`internal/store/sqlite.go`: the `migrations` slice (~100, 59 entries) pairs with
a ~20-branch `else if m.version == N` dispatch chain (~599-720). A complex
migration needs its number to agree in three places; the slice's `sql` field is
dead (`"SELECT 1"`, `""`) for chain-dispatched versions. Burned-version scar
tissue (33, 49/50/51/52 — see comments ~450-456) makes renumbering forbidden.

Target shape:

```go
type migration struct {
    version int
    name    string
    sql     string                 // simple migrations
    apply   func(tx *sql.Tx) error // complex migrations; wins over sql when set
}
// runner: one loop —
// if m.apply != nil { m.apply(tx) } else if m.sql != "" { tx.Exec(m.sql) }
```

- [ ] Add the `apply` field; convert each dispatch-chain branch into the
      corresponding slice entry's `apply` (move the body of `applyMigrationN`
      into a named func referenced from the entry).
- [ ] Delete the version if/else chain entirely.
- [ ] Do NOT renumber, merge, or "clean up" any migration, including the burned
      ones — empty/`SELECT 1` entries stay as entries (with their comments).
      The `49 || 50` merged branch becomes two entries pointing at the same
      `apply` func.

Verify (this one deserves care — it touches every future DB):

```bash
go test ./internal/store/... -count=1        # migration + legacy tests must pass unmodified
# fresh-DB smoke: create a store on a temp path and confirm schema version:
go test ./internal/store -run 'Migrat|Legacy|Schema' -v -count=1
```

And on a real dev profile: `make dev`, confirm the dev daemon starts cleanly and
`~/.attn-dev/attn.db` reports the same `MAX(version)` in `schema_migrations` as
before the change (`sqlite3 ~/.attn-dev/attn.db 'select max(version) from schema_migrations'`).

## D. Sweep the retired "janitor" vocabulary

`internal/agent/driver.go` comments (~311, 315, 326, 370, 375, 378) still
narrate the headless contract in terms of "the janitor", a persona
`docs/glossary.md` retired in favor of **the keeper**. Persisted values already
migrated (`workspace_keeper.go` ~26/131).

- [ ] s/janitor/keeper/ in those comments (comments only — no identifiers, no
      persisted strings; the migration code that references the OLD
      `attn-janitor` value must keep referencing it, that's its job).

Verify: `grep -rn 'janitor' internal/ --include='*.go' | grep -v _test` — the
remaining hits should only be the persisted-value migration in
`workspace_keeper.go` and its test.

## Decisions

- Deliberately excluded from this plan: deriving `ProtocolVersion` from a schema
  hash (needs a design pass — hash-of-generated-file churns on formatting), and
  the `transcript.Extract*` options-struct collapse (touch it when next in that
  package). Both stay on the review's list.

## Verification (whole plan)

```bash
make test          # after each item lands
```
