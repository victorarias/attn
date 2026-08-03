# A3 — Document store + live queries

Stage A3 of the [extension platform roadmap](2026-08-01-extension-platform-roadmap.md).
North star: [docs/vision/extension-platform.md](../vision/extension-platform.md).

Status: gate approved by Victor 2026-08-03. Implemented 2026-08-03.

The store is a generic daemon primitive — namespaced JSON documents, change
events on the bus, live queries — that extensions happen to be the first big
consumer of. The store itself does not know what an extension is.

An open-source survey preceded the gate; the conclusion was to build the thin
layer on our SQLite rather than adopt a dependency. SQLite remains the
database — durability, transactions, and indexing are its job. What this
stage writes is a query translator, a bus producer, and a subscription map.

## Gate answers

### 1. Shape: namespace → collection → document

Not in the roadmap's gate list, but it decides everything downstream. Every
real consumer stores more than one record kind (the approval gate alone has
requests and settings), so collections are first-class rather than a `type`
field convention inside one bucket. Indexes, queries, and subscriptions are
all per-collection. This is the Firestore/Convex shape.

### 2. Namespace naming: two-part `owner/name`

`ext/approval-gate`, `core/…`. The store treats the namespace as an opaque
string; **who may write where is enforced at grant time**, not by the store —
A4's registry hands an extension exactly its own `ext/<id>` prefix, and the
daemon may use `core/`. The hierarchy costs nothing now and gives isolation
classes without the store knowing what an extension is.

The flat alternative (`approval-gate`) was rejected because core and
extensions would share one namespace pool — a collision waiting for A4.

### 3. Index design: declared fields as the contract, scan-first as the implementation

A collection declares the fields that may be filtered and sorted on. That
declaration is the API contract and is what keeps the query surface honest.
Physically, v1 executes with a scan inside `(namespace, collection)` — no
secondary index structures yet.

Receipt: attn is single-user; a collection holds thousands of documents, not
millions, and a `json_extract` scan over that is milliseconds. We have no
measurement saying indexes are needed, so building them now would be a number
without a receipt. The tripwire: slow queries are logged with the collection
size, so the day a real consumer crosses it we have the measurement to design
against.

When that day comes, the upgrade is invisible — SQLite expression indexes on
`json_extract(body, '$.field')`, created by the store from the same
declaration. No author-facing change, no migration. The rejected alternative
was a generic `(namespace, collection, field, value) → doc id` side table,
which pays its write cost from day one and still needs intersection joins for
multi-predicate queries.

Record-shape evolution stays at the read boundary (zod parse, tolerant
rendering, per the vision). A document missing a declared field simply does
not match filters on it and sorts last.

### 4. Query surface: equality + range on declared fields, one sort field, limit

`{namespace, collection, filters, sort, limit}` as one JSON object. Filters
are equality and range (`<`, `<=`, `>`, `>=`) on declared fields only; sort
is a single declared field plus direction; limit is mandatory-bounded. Range
on the sort field gives cursor pagination for free.

The receipt is the two proof compositions: the approval gate's panel is
"requests where status=pending, newest first" (equality + sort + limit);
Present v2's records add "for presentation X after cursor Y" (equality +
range on the sort field). Both fit; neither needs anything richer. Operator
soup (contains, or-groups, joins) is speculative until a real extension asks.

**One query representation for every doorway.** Three surfaces will
eventually carry queries — workflow/handler code in the Bun sidecar (SDK →
IPC), extension UI in the app (live-query hooks → the generic WebSocket
envelopes), and the CLI. All three carry the same JSON query object; A4/A5
add transport, not semantics. Defining the query as Go function parameters
instead would force A4 to invent the serialization later.

### 5. Live-query contract: server re-runs, pushes the full result set

Subscribe returns the initial result set with the subscription — not
"subscribed, results will arrive" — because the vision's remount-not-HMR
story depends on re-hydration in milliseconds, which means subscriptions must
be cheap to create, serve immediately, and destroy. After that, any committed
write to the collection re-runs matching subscriptions (coalesced per
subscription, like `coalesceSnapshots`) and pushes fresh results. The
contract is "you receive the current result set."

Wiring is the existing A1/A2 architecture doing its job: a write publishes a
`doc.changed` fact (subject names the namespace/collection); the hub — an
ephemeral consumer — re-runs affected subscriptions in a projection and
pushes to the wire. The same fact gives B2 workflows durable at-least-once
wake-on-document-change later, with no new machinery.

Rejected alternatives: incremental patching (fast and subtly wrong at the
edges — a document falling out of a `limit 20` window forces a re-query to
find the 21st anyway; this is where live-query systems grow bugs) and
change-feed-only (every consumer reimplements re-run-and-race handling).
Because the contract is "current result set," patching can hide behind it
later if a measurement ever says it matters.

### Assumptions accepted alongside the gate

- **Main `attn.db`, not a separate file.** A workflow activity writing a
  document atomically with its job-state commit is load-bearing for B2's
  exactly-once story. Separate-file isolation solves a bloat problem we do
  not have.
- **First consumer: a thin `attn doc` CLI** (put/get/query/watch). It makes
  the exit criteria live-verifiable before A4 exists, and it is the operator
  surface the bus got with `attn bus status`. Migrating a core feature onto
  the store is a door left open, not an A3 goal.
- The vision keeps namespaced SQL tables as a documented escape hatch for
  later; nothing in A3 touches that.

## Sequence note

Depends on A1 (change events ride the bus), already live. A4 depends on this
stage; B2 depends on A4. A3 is the only stage that can start and it unblocks
both tracks.

## Exit criteria

From the roadmap: write → change event → subscribed query update, durable
across restart; namespace isolation enforced.

## Implementation plan

Written during implementation (2026-08-03), after A1, A2, and B1 merged.

### Where the pieces live

The stage splits along the seam `internal/bus` ↔ `internal/store/bus.go`
already established: meaning in a leaf package, execution next to the database.

- `internal/docstore` — the query language. Field types, operators, the `Query`
  object, and `Query.Compile(schema) → Compiled{Where, Args, Order, Limit}`.
  It holds no database handle, so every rule about what a query means is
  testable without one.
- `internal/store/documents.go` — the two tables (migration **88**), the CRUD,
  and `QueryDocuments(Compiled)`. It executes SQL it did not decide.
- `internal/daemon/documents.go` — the IPC handlers, the change fact, and the
  live-query subscriptions.
- `internal/client/documents.go`, `cmd/attn/doc.go` — the operator surface.

### Storage

Two tables, both keyed by `(namespace, collection, …)`:

```
documents            (namespace, collection, id, body, created_at, updated_at)
document_collections (namespace, collection, fields_json, updated_at)
```

The declaration is a row, not a schema: redeclaring replaces `fields_json` and
touches no document. That is what "no migrations for authors" means concretely
— adding a queryable field is a write to one row, and every stored document is
queryable by it immediately, because filters read the body through
`json_extract` at query time rather than a materialized column.

Scan-first, as the gate decided. A declared field compiles to
`json_extract(body, '$.<name>')`; `created_at`/`updated_at` compile to their
columns and are queryable without being declared (declaring either is an
error — the name is taken). Index-per-declared-field is the door left open, and
nothing about the query surface changes when it opens.

### Bounds

`DefaultLimit` 100, `MaxLimit` 1000. Receipt (2026-08-03, production `~/.attn`):
the lists attn already pushes whole are tickets 7, sessions 11, notifications 8,
workspaces 8. The default is an order of magnitude past that working set and the
ceiling two — a tripwire. Asking past the ceiling is an error naming the limit
and the ask, never a silent truncation.

Every sort is made total by an `id ASC` tiebreaker, so a range filter on the
sort field is a correct cursor. That is the whole pagination story; there is no
offset and no opaque page token.

### Live queries

A change to a collection publishes `document.changed` on the bus, subject
`namespace/collection/id`. It is deliberately **not** in `wireProjections`: it
produces no WebSocket traffic. An ephemeral bus subscriber beside the hub reads
it and pokes the subscriptions whose target matches.

Each subscription owns a goroutine and a one-slot `wake chan struct{}`. A full
channel means "already told, not yet served", and dropping the extra poke is
correct because every delivery is a whole result set that supersedes the last —
so coalescing is free and the bus's publish lock is never held across a socket
write. `TestWritesDoNotBlockOnASubscriberThatIsNotReading` is the guard: if the
fan-out ever writes the socket directly, that test hangs rather than fails.

`doc_subscribe` registers before running its first query. That order can only
produce one redundant delivery; the other order can miss a write.

### Transport

The IPC socket, with `doc_subscribe` holding its connection open. A5 owns the
generic protocol envelopes, and nothing here pre-empts that gate: no document
traffic reaches the WebSocket in this stage.

### Deliberately not built

- **Indexes.** Scan-first was the gate's answer; the working set above is the
  receipt. The declaration is what makes adding them later invisible to callers.
- **Grants.** Namespaces are `owner/name` and enforced as a shape; *who* may
  claim one is A4's registry, which is the thing that knows about authors.
- **A frontend surface.** A5 owns the UI host. The store's consumers today are
  the CLI and, next, the extension runtime.
