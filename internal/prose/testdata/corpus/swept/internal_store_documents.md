SQLite persistence for the document store; query semantics and SQL
compilation live in internal/docstore. One table per collection
(`doc_<registry-id>`); a declared field is an indexed VIRTUAL generated
column over the body. Every identifier spliced into SQL here comes from
docstore — derived from an integer or a validated field name, never caller
text. Design: docs/plans/2026-08-03-ext-a3.1-doc-store-physical-schema.md.

documentColumns is the read projection; body is returned byte for byte, and
generated columns are never selected.

DefineDocumentCollection records a collection's declaration and brings its
table into line with it, creating it on first declaration; registry row and
DDL commit together. The bool reports a redeclaration (which has watchers to
wake) — only the define's own transaction can tell without racing.

DocumentCollection returns a collection's declaration with its table filled
in; the bool is false with a nil error when it was never declared.

DeleteDocumentCollection removes a declaration and every document under it,
reporting how many documents went; table drop and registry delete commit
together.

PutDocument writes a document, creating or fully replacing it, and returns
its new revision; created_at survives a replacement. expected: nil writes
unconditionally, docstore.ExpectAbsent only if nothing is there, a revision
only if the document is still at it — a failed assertion is a
*docstore.ConflictError and nothing is written. Each form checks and writes
in ONE statement; a separate read-then-write would reopen the race the check
exists to close.

DO NOTHING so a conflicting insert refuses via "no row came back",
the same signal the conditional update gives.

No insert path: expecting a revision is expecting a document, so a
missing one is refused rather than created.

Only an expectation can refuse: the unconditional upsert always writes a
row, so ErrNoRows from it is a broken statement, not a conflict.

DeleteDocument removes a document, reporting whether one was there. expected
asserts which version is being removed, as in PutDocument; a stale revision
is a *docstore.ConflictError and nothing is deleted.

Treating "expect absent" as unconditional would delete a document the
caller was trying to protect.

documentConflictWith describes a refused write, re-reading to name the
revision that won. The re-read must run on whatever refused the write —
inside a composite, its transaction — or it reads a state the refusal never
saw.

DocumentWrite is one document mutation: a put of Body, or a removal when
Delete is set. Expected is as in PutDocument and DeleteDocument.

DocumentWriteResult reports a committed write: Changed says whether the
store moved, Seq is the fact's log position (0 exactly when Changed is
false), Rev is the new revision (0 for a delete).

CommitDocumentWrite writes a document and appends the fact describing it in
ONE transaction — a fact that cannot be made durable fails the whole write,
so the data and the log cannot diverge. The store stays fact-agnostic: the
caller builds the fact. A write that changed nothing appends NO fact and
returns no seq.

Also rolls back the no-error paths: a delete that removed nothing has
nothing to commit.

queryDocuments runs an already-compiled query; only docstore-checked
identifiers are spliced in, every caller value is a bound argument.
Unexported: a compiled query is only correct against the state it was
compiled from, so ReadQuery owns both halves and is the way in from outside.

QueryRead is one answer to one query: the documents, the declaration they
were computed against, and the log position they were true at.

readAsOfSeq is the log position an answer was true at: MAX(seq) in
bus_events, read inside the same transaction as the rows — outside it the
number names a state the rows were never in. Empty log yields 0 ("before
everything"); compaction never removes the newest row, so it cannot lower
MAX(seq).

DocumentRead is one answer to one document read: the document if it is there,
and the log position the answer was true at.

ReadDocument answers a get in a single read transaction, for the same reason
ReadQuery does; the bool is false with a nil error when the collection was
never declared.

CountRead is one answer to a count: how many documents match, and the log
position that was true at.

CountQuery answers "how many match" with the same compile the query itself
uses, so a count and the page it describes cannot disagree; only the filter
half is used, and an after cursor counts what follows the anchor.

ReadQuery answers a query in a single read transaction, and is the only way
to run one from outside the store. Declaration read, anchor read, compile,
and SELECT must share one transaction: split across transactions the
statement can compile against one state and execute against another, which
silently returned wrong pages. found reports whether the collection is
declared, as in DocumentCollection.

QueryPlan returns SQLite's plan for a compiled query, one row per step, so a
test can assert a query reaches an index rather than scanning.

documentTable resolves the table a document operation runs against, refusing
a schema that did not come from a read of the registry.

createCollectionTable builds a collection's storage: stored columns, indexes
for the reserved ordering columns, and a generated column plus index per
declared field. WITHOUT ROWID clusters the row on the id it is looked up by;
rev is deliberately unindexed — it serves no query.

created_at and updated_at are queryable without being declared, so they
are indexed unconditionally.

alterCollectionTable brings an existing table into line with a new
declaration; a field whose type changed is dropped and re-added so its
column carries the new affinity.

VIRTUAL, not STORED: the index already materialises the compared values,
and SQLite refuses to add a STORED column to an existing table.

createFieldIndex indexes one column with the id tiebreaker beside it — the
tuple every query orders and cursors by; SQLite walks it backwards for DESC.

quoteIdent renders an already-validated identifier for SQL; quoting makes a
field named like a keyword harmless.

execQuerier is a write that may have to read to explain itself: a refused
delete re-reads the revision that won.
