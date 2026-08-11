Package docstore is attn's document primitive: JSON documents addressed by
(namespace, collection, id), queried through one serializable query object.
It owns what a query MEANS and the SQL it compiles to, but no database
handle — internal/store executes what is compiled here. It also owns the
physical naming (TableName, FieldColumn); the store builds its DDL from
those names and invents none.

Result-set limits. Measured (2026-08-03, production ~/.attn): the lists attn
pushes whole are tickets 7, sessions 11, notifications 8, workspaces 8.
DefaultLimit is an order of magnitude past that, MaxLimit two — a tripwire.

Reserved field names: real columns the store stamps, always filterable and
sortable without being declared. A collection may not declare a body field
under either name.

FieldType decides how a filter's bound is bound (a number field against a
string bound silently matches nothing) and the column's affinity.

Op is a filter comparison; pagination is deliberately not built out of these
— see Query.After.

CollectionSchema declares which fields may be filtered and sorted on; the body
stays arbitrary JSON. Table is minted by the store from the declaration's row
id and filled in on read, never written by a caller — Compile refuses a
non-minted Table, the whole defence for the one identifier not derived from a
validated field name.

Sort names the ordering field; Compile appends the document id as a tiebreaker
in the sort's direction, so the order is always total.

Query is the one representation every surface carries; a zero Limit means
DefaultLimit, never "unbounded". After (the previous page's last id) is part
of the query rather than a filter: a filter can constrain only one of (sort
field, id), so it either skips ties or repeats the anchor.

Document is one stored record. Body is byte-for-byte as written — shape
evolution is handled by tolerant readers, never by migrating documents.

A revision counts writes to one document, starting at FirstRev. ExpectAbsent
is unambiguous because revisions start at 1: expecting rev zero is expecting
no document, so create-only falls out of the same field. int64 because an
int32 overflow (~7 years at 10 writes/s to one document) would silently make
a stale check pass — the exact failure this exists to prevent.

ConflictError is a write refused because the document was not at the expected
revision — a distinct type so surfaces can tell "read again and retry" from
"broken".

Found reports whether a document was there at all; Actual is meaningless
when Found is false.

InvalidQueryError wraps a refusal: the message is what an agent fixes the
query from, the type is what a program branches on.

InvalidQuery marks an error as a refusal; exported because a query can be
refused before it reaches Compile.

Compiled is a validated query as SQL fragments the store splices into its own
SELECT, Args binding Where's placeholders in order. Where is empty when
nothing constrains the query — the table holds exactly one collection.

A namespace is `owner/name`: the owner segment is the isolation class a
grant hands out (`ext`, `core`), the name segment identifies the holder.

These names are spliced into SQL as identifiers; each derives from something
already checked (an integer row id, or a field name matching fieldNameRe).

fieldColumnPrefix keeps a declared field (`id`, `body`) from shadowing the
columns the store owns.

TableName is minted from the declaration's row id, so no identifier is ever a
function of caller text.

ValidateTableName accepts a minted table name; Compile calls it before
splicing, so a schema from anywhere but the store's read path fails loudly.

FieldExpression is what that column computes; the column is VIRTUAL, which is
why a declaration rewrites no document.

ColumnAffinity maps a declared type to a SQLite affinity: "5" in a NUMERIC
column orders as the number 5, while an array or object keeps its JSON text
and stays orderable rather than erroring.

quoteIdent is belt-and-braces over the validating patterns; it makes a
keyword-named field harmless.

ValidateNamespace accepts a well-formed `owner/name`; which owners exist is
not this package's concern.

Validate checks a declaration. Field names must be plain identifiers because a
declared field becomes both a JSON path and an executed column name.

declaredNames lists the queryable fields, reserved included, so a rejected
query says what it could have asked for.

Compile validates q against the declaration and returns it as SQL; every
rejection is an *InvalidQueryError, typed once here. anchor is the document
q.After names, read by the caller (this package holds no DB handle); nil with
q.After set is an error, not an empty page.

No namespace/collection predicate: the table IS the collection, so the
isolation is structural.

The id tiebreaker runs in the sort's own direction, so the visible order
is one uniformly directed tuple — what After compares against.

afterTuple compiles the After cursor as "strictly past the anchor in the
visible order (sort field, id)":

A missing or JSON-null sort value is a real case; NULL compares as nothing,
so it is branched on rather than bound. SQLite sorts NULL first: in ASC every
non-NULL row is past a NULL anchor, in DESC none is.

No sort: the whole order is id ASC, so the cursor is one comparison.

The anchor's sort value is read back through the same column the ORDER BY
uses, not bound from Go: a "number" field may hold an array or object with
no bindable Go equivalent, and the column's own affinity must govern the
comparison. The subquery is uncorrelated, evaluated once per statement.

anchorSortIsNull reports whether the cursor's sort field is absent or JSON
null — what json_extract yields as SQL NULL.

fieldExpr resolves a field reference to SQL: a reserved name literally,
anything else through its declared generated column. use names whether the
filter or the sort is wrong.

bindValue refuses a bound that cannot compare with the field's type: a number
field against "5" would silently match nothing.

A reserved field is a timestamp compared as text; re-encode to TimeFormat
because a raw "…T10:00:00Z" bound sorts above every stamp in that second.

TimeFormat is the stored timestamp encoding. Stamps are ordered as text, so
the fraction must be fixed-width (nine digits, always present) and the zone
always "Z": RFC3339Nano strips trailing zeros, which made same-second stamps
compare wrongly and "changed since" filters drop rows in silence. Migration
91 rewrote the stored stamps.

ParseTime decodes a stamp in any RFC3339 form — including the pre-migration-91
trailing-zero-stripped form this store once handed out — normalized to UTC.

Target is the subject collection changes publish under and subscriptions match
on. Collection-grained: a live query's result set can change because a document
it does not contain started matching.

Address is the subject a change to one document is published under;
subscription matching uses Target, carried alongside.

ValidateBody accepts any JSON object; objects only, because a declared field
is read with a JSON path a bare array or scalar lacks.
