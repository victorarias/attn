SQLite persistence for the document store. This file is only persistence:
what a query means, and the SQL a validated one compiles to, live in
internal/docstore, which reaches nothing — the same split internal/bus and
bus.go use.

ONE TABLE PER COLLECTION. `document_collections` is the registry: one row per
declared collection, whose row id mints the name of the table holding its
documents (`doc_<id>`). A declared field is an indexed VIRTUAL generated
column over the body in that table, so a query reads an index instead of
scanning, while the body is still stored and returned byte for byte and
declaring a field rewrites no document. The measurement behind the shape is
in docs/plans/2026-08-03-ext-a3.1-doc-store-physical-schema.md.

Isolation is structural rather than a check: a collection's documents are the
only rows in its table, so there is no read or write here that could reach
another namespace even if its predicate were wrong.

Every identifier spliced into the SQL below comes from docstore — a table
name derived from an integer, a column name derived from a field name that
matched a validating pattern. None of it is caller text.

documentColumns is the read projection; body is returned byte for byte. The
generated columns are never selected: they exist to be filtered and ordered
on, and the body already carries what they compute.

rev is in the projection rather than fetched on demand because it is the token
a read-modify-write hands back, and a caller that had to ask for it separately
would be reading a version it did not read the body of.

DefineDocumentCollection records a collection's declaration and brings its
table into line with it, creating the table on first declaration. Redeclaring
is how a collection gains or loses a queryable field: an added field is a new
generated column plus its index, a removed one drops both, and a field whose
type changed is replaced so its column carries the new affinity. Documents are
never rewritten — a VIRTUAL column computes from the body, so it applies to
every document already stored the moment it exists.

Registry row and DDL commit together. A declaration whose table did not get
built, or a table no declaration names, would each be a collection that
cannot be queried or cannot be found.

The returned bool reports whether this was a redeclaration of an existing
collection. Only the define's own transaction can tell — a caller checking
existence first would race a concurrent define — and the caller needs the
distinction because a redeclare has watchers to wake and a first declaration
cannot.

DocumentCollection returns a collection's declaration with its table filled
in. The bool is false with a nil error when the collection was never declared
— a caller must tell "no such collection" apart from a read failure, because
the first is what every query against an undeclared collection has to report.

ListDocumentCollections returns every declaration, namespace-major. It is the
operator's index of what exists.

DeleteDocumentCollection removes a declaration and every document under it,
reporting how many documents went. Dropping the table is what returns the
space, and it happens in the same transaction as the registry delete: a
declaration without its table would leave a collection nothing can query, and
a table without its declaration would leave rows nothing can name.

PutDocument writes a document, creating or fully replacing it, and returns the
revision it now has. created_at survives a replacement — it is when the record
first appeared, which is what a "newest first" query means by it — while
updated_at and rev move on every write.

The schema names the table, which is why every caller reads the declaration
first: an undeclared collection has no storage, and that has to be an error a
caller reports rather than a table appearing by surprise.

expected is the caller's assertion about what it is overwriting: nil writes
unconditionally, docstore.ExpectAbsent writes only if nothing is there, and a
revision writes only if the document is still at it. A failed assertion is a
*docstore.ConflictError and nothing is written.

THE WRITE ITSELF IS ONE STATEMENT, which is what makes the check atomic: each
form below refuses in the same statement that would have written. Reading the
revision and then writing would be two, and the whole point of this is the
window between them. Whether that statement runs in SQLite's implicit
transaction (here) or inside one that also appends a fact
(CommitDocumentWrite) changes nothing about the check — the same statement
runs either way. The re-read below happens only on the failure path, to say
what the write lost to.

DO NOTHING rather than letting the primary key raise: a conflicting
insert then returns no row, which is the same "no row came back" signal
the conditional update gives, so both refusals arrive one way.

No insert path at all: expecting a revision is expecting a document,
so a missing one must be refused rather than created.

No row came back is how every refusal arrives — but only an expectation can
refuse one. The unconditional upsert always writes a row, so no row from it
is a broken statement rather than a conflict, and reporting it as one would
tell a caller to retry something that will never succeed.

DeleteDocument removes a document, reporting whether one was there. The caller
needs the difference: a delete that removed nothing must not announce a change
that did not happen.

expected asserts which version is being removed, the same way PutDocument's
does — removing a record on the strength of a body you read is the same
lost-update hazard as overwriting one. A revision that no longer matches is a
*docstore.ConflictError and nothing is deleted.

"Delete this if it is not there" is not an assertion a delete can act
on, and silently treating it as unconditional would delete a document
the caller was trying to protect.

documentConflictWith describes a refused write. It reads the document again to
name the revision that won, because "your write was refused" without that is
an error a caller cannot act on. A read failure here is not worth losing the
conflict over — the refusal is the fact, the revision is the detail — so it
degrades to reporting the document as absent.

The re-read runs on whatever refused the write, which inside a composite is
its transaction: reading around it would read a state the refusal never saw.

DocumentWrite is one document mutation: a put of Body, or a removal when
Delete is set. Expected is the caller's assertion about the version being
replaced or removed, exactly as PutDocument and DeleteDocument take it.

Changed reports whether the store actually moved, and is what separates a
delete that removed a document from one that found nothing there. Seq is the
fact's position on the durable log and is 0 exactly when Changed is false —
nothing changed, so nothing was announced. Rev is the document's new revision
and is 0 for a delete, which has no version to report.

CommitDocumentWrite writes a document and appends the fact describing it in
ONE transaction, and is how every fact-bearing writer reaches the store.

The composite exists because the two halves used to be separate commits: a
document write landed, and its fact was published afterwards and could fail
on its own (the bus logged and carried on). A crash between them left the
data changed and the log silent — permanently, since nothing re-derives facts
— which is the divergence B-track workflows would trigger on. After this,
a fact that cannot be made durable fails the whole write: the caller gets an
error and a retry instead of a store nobody was told about.

The store stays fact-agnostic. It does not know what `document.changed` means
or how a subject is spelled; the caller builds the fact and this appends it.

A write that changed nothing appends NO fact and returns no seq: a delete
that found nothing, or a refusal, is not a change, and waking every live
query on the collection to re-render an identical result set is the cost the
conditional write exists to avoid.

Rolled back on every path that does not commit, including the ones that
return no error: a delete that removed nothing has nothing to commit.

queryDocuments runs an already-compiled query. The compiled fragments are
built from a validated query against a stored declaration, so the only strings
spliced into the statement are ones docstore produced from identifiers it
checked; every caller-supplied value arrives as a bound argument.

Unexported on purpose. A compiled query carries a table name, a column
affinity and a cursor value taken from the declaration it was compiled
against, so running one is only correct against the state it was compiled
from. Handing that pairing to callers is what produced three silent wrong
answers; ReadQuery owns both halves and is the way in from outside.

QueryRead is one answer to one query: the documents, the declaration they
were computed against, and the log position they were true at. The
declaration is returned rather than assumed because the caller's copy may
already be out of date by the time it reads this — the one in here is the one
the answer actually means.

readAsOfSeq is the log position an answer was true at: the highest seq in
bus_events, read inside the same transaction as the rows.

Every document write commits its fact into that log, so this is exactly "the
last change this answer includes". Same transaction, not a second read: read
outside it and the number names a state the rows were never in.

An empty log yields 0, which is a valid position meaning "before everything"
rather than a missing answer. Compaction removes rows but never the newest,
so it cannot lower MAX(seq) and the watermark is immune to it.

DocumentRead is one answer to one document read: the document if it is there,
and the log position the answer was true at.

ReadDocument answers a get in a single read transaction — the declaration,
the row, and the log position, all against one state of the database, for the
same reason ReadQuery does. The bool is false with a nil error when the
collection was never declared.

CountRead is one answer to a count: how many documents match, and the log
position that was true at.

CountQuery answers "how many match" with the same compile the query itself
uses, so a count and the page it describes cannot disagree about what
matches. Only the filter half of the compiled query is used: ordering and the
limit decide which matches come back, never how many there are.

A query carrying an after cursor counts what follows the anchor, which is the
same thing paging through the rest of the answer would find.

ReadQuery answers a query in a single read transaction, and is the only way
to run one from outside the store.

Answering takes three reads: the declaration, which names the table and the
affinity of every column the SQL will compare; the cursor anchor, whose
presence and sort value decide which cursor clause gets compiled; and the
SELECT itself. Those used to be three separate transactions with the compile
in between, so the statement could be built against one state and executed
against another. That is not a narrow race — it produced a page that silently
came back empty when the anchor was deleted, a page that handed back the
anchor document itself when the anchor gained a sort value, and a filter that
silently matched nothing when a field's declared type changed underneath it.
None of the three reported an error, and each returned an answer that matched
no state the collection was ever in.

One transaction removes the class rather than narrowing it: compile-time and
execute-time are the same instant, so there is no second state to disagree
with. Nothing is committed — a read transaction here buys a consistent
snapshot, not atomic writes. It costs one contiguous SHARED lock in place of
three brief ones, which is the same total work; what it denies a writer is
exactly the gap that produced the wrong answers.

found reports whether the collection is declared at all, the same way
DocumentCollection does.

Rolled back rather than committed: a read transaction has nothing to
commit, and rolling back is what releases the snapshot.

A missing anchor stays nil, which is the case Compile refuses by name.
Inside this transaction that refusal is now truthful: the anchor really
is absent from the state the SELECT would have run against.

CountDocuments reports how many documents a collection holds. It is what the
slow-query log reports alongside a duration.

QueryPlan returns SQLite's plan for a compiled query, one row per step. It
exists so a test can assert that a filtered or sorted query reaches an index
rather than scanning the collection — the property this whole physical schema
is for, and one that no timing assertion could check without being flaky.

documentTable resolves the table a document operation runs against, refusing
a schema that did not come from a read of the registry.

createCollectionTable builds a collection's storage: the five stored columns,
an index for each reserved ordering column, and a generated column plus index
for every declared field.

WITHOUT ROWID because a document is addressed by its id and never by position:
clustering the row on the id it is looked up by removes a whole B-tree and the
hop through it.

rev is stored and not indexed. It is only ever read for a document already
being looked up by its primary key, or compared inside a write to that same
row, so an index over it would serve no query and cost every write.

created_at and updated_at are queryable without being declared, so they
are indexed unconditionally. Each index carries the id tiebreaker, which
is what makes it serve the whole ordering tuple rather than only its
first half — the same tuple the after cursor compares against.

alterCollectionTable brings an existing table into line with a new
declaration. A field whose type changed is dropped and re-added rather than
left alone: its column's affinity is how two stored values compare, so a
declaration that says "number" must not keep comparing as text.

VIRTUAL, not STORED: the index materialises the values that get compared,
so storing them in the row as well would pay for them twice. SQLite also
refuses to add a STORED column to an existing table, which is what would
make redeclaring a rewrite instead of a one-statement change.

createFieldIndex indexes one column with the id tiebreaker beside it, which is
the order every query asks for. One index serves both directions: SQLite walks
it backwards for a DESC ordering.

quoteIdent renders an identifier for SQL. Every identifier reaching it is
already derived from a validated name; the quoting is what makes a field named
like a keyword harmless.

execQuerier is a write that may have to read to explain itself: a refused
delete re-reads the revision that won.
