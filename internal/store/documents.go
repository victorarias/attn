package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// SQLite persistence for the document store; query semantics and SQL
// compilation live in internal/docstore. One table per collection
// (`doc_<registry-id>`); a declared field is an indexed VIRTUAL generated
// column over the body. Every identifier spliced into SQL here comes from
// docstore — derived from an integer or a validated field name, never caller
// text. Design: docs/plans/2026-08-03-ext-a3.1-doc-store-physical-schema.md.

// documentColumns is the read projection; body is returned byte for byte, and
// generated columns are never selected.
const documentColumns = `id, body, rev, created_at, updated_at`

// DefineDocumentCollection records a collection's declaration and brings its
// table into line with it, creating it on first declaration; registry row and
// DDL commit together. The bool reports a redeclaration (which has watchers to
// wake) — only the define's own transaction can tell without racing.
func (s *Store) DefineDocumentCollection(schema docstore.CollectionSchema, now time.Time) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("store: no database")
	}
	if err := schema.Validate(); err != nil {
		return false, err
	}
	fields, err := json.Marshal(schema.Fields)
	if err != nil {
		return false, fmt.Errorf("store: encoding fields for %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	ts := now.UTC().Format(docstore.TimeFormat)

	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("store: defining %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, table, found, err := readCollectionTx(tx, schema.Namespace, schema.Collection)
	if err != nil {
		return false, err
	}

	if !found {
		res, err := tx.Exec(
			`INSERT INTO document_collections (namespace, collection, fields_json, updated_at) VALUES (?, ?, ?, ?)`,
			schema.Namespace, schema.Collection, string(fields), ts)
		if err != nil {
			return false, fmt.Errorf("store: defining %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return false, fmt.Errorf("store: defining %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
		table = docstore.TableName(id)
		if err := createCollectionTable(tx, table, schema.Fields); err != nil {
			return false, fmt.Errorf("store: creating storage for %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
	} else {
		if err := alterCollectionTable(tx, table, existing.Fields, schema.Fields); err != nil {
			return false, fmt.Errorf("store: redeclaring %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
		if _, err := tx.Exec(
			`UPDATE document_collections SET fields_json = ?, updated_at = ? WHERE namespace = ? AND collection = ?`,
			string(fields), ts, schema.Namespace, schema.Collection); err != nil {
			return false, fmt.Errorf("store: redeclaring %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: committing declaration of %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	return found, nil
}

// DocumentCollection returns a collection's declaration with its table filled
// in; the bool is false with a nil error when it was never declared.
func (s *Store) DocumentCollection(namespace, collection string) (*docstore.CollectionSchema, bool, error) {
	if s.db == nil {
		return nil, false, fmt.Errorf("store: no database")
	}
	schema, table, found, err := readCollection(s.db, namespace, collection)
	if err != nil || !found {
		return nil, false, err
	}
	schema.Table = table
	return &schema, true, nil
}

// ListDocumentCollections returns every declaration, namespace-major.
func (s *Store) ListDocumentCollections() ([]docstore.CollectionSchema, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	rows, err := s.db.Query(
		`SELECT id, namespace, collection, fields_json FROM document_collections ORDER BY namespace, collection`)
	if err != nil {
		return nil, fmt.Errorf("store: listing document collections: %w", err)
	}
	defer rows.Close()
	var out []docstore.CollectionSchema
	for rows.Next() {
		var (
			id     int64
			schema docstore.CollectionSchema
			fields string
		)
		if err := rows.Scan(&id, &schema.Namespace, &schema.Collection, &fields); err != nil {
			return nil, fmt.Errorf("store: scanning document collection: %w", err)
		}
		if err := json.Unmarshal([]byte(fields), &schema.Fields); err != nil {
			return nil, fmt.Errorf("store: decoding fields for %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
		schema.Table = docstore.TableName(id)
		out = append(out, schema)
	}
	return out, rows.Err()
}

// DeleteDocumentCollection removes a declaration and every document under it,
// reporting how many documents went; table drop and registry delete commit
// together.
func (s *Store) DeleteDocumentCollection(namespace, collection string) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: deleting %s/%s: %w", namespace, collection, err)
	}
	defer func() { _ = tx.Rollback() }()

	_, table, found, err := readCollectionTx(tx, namespace, collection)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}

	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: counting %s/%s: %w", namespace, collection, err)
	}
	if _, err := tx.Exec(`DROP TABLE ` + table); err != nil {
		return 0, fmt.Errorf("store: dropping storage for %s/%s: %w", namespace, collection, err)
	}
	if _, err := tx.Exec(
		`DELETE FROM document_collections WHERE namespace = ? AND collection = ?`, namespace, collection); err != nil {
		return 0, fmt.Errorf("store: deleting declaration for %s/%s: %w", namespace, collection, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: committing deletion of %s/%s: %w", namespace, collection, err)
	}
	return n, nil
}

// PutDocument writes a document, creating or fully replacing it, and returns
// its new revision; created_at survives a replacement. expected: nil writes
// unconditionally, docstore.ExpectAbsent only if nothing is there, a revision
// only if the document is still at it — a failed assertion is a
// *docstore.ConflictError and nothing is written. Each form checks and writes
// in ONE statement; a separate read-then-write would reopen the race the check
// exists to close.
func (s *Store) PutDocument(schema docstore.CollectionSchema, id string, body []byte, now time.Time, expected *int64) (int64, error) {
	table, err := s.documentTable(schema)
	if err != nil {
		return 0, err
	}
	return putDocumentWith(s.db, schema, table, id, body, now, expected)
}

func putDocumentWith(q rowQuerier, schema docstore.CollectionSchema, table, id string, body []byte, now time.Time, expected *int64) (int64, error) {
	ts := now.UTC().Format(docstore.TimeFormat)

	var (
		stmt string
		args []any
	)
	switch {
	case expected == nil:
		stmt = `INSERT INTO ` + table + ` (id, body, rev, created_at, updated_at)
		        VALUES (?, ?, ?, ?, ?)
		        ON CONFLICT(id) DO UPDATE SET
		          body=excluded.body,
		          rev=` + table + `.rev + 1,
		          updated_at=excluded.updated_at
		        RETURNING rev`
		args = []any{id, string(body), docstore.FirstRev, ts, ts}
	case *expected == docstore.ExpectAbsent:
		// DO NOTHING so a conflicting insert refuses via "no row came back",
		// the same signal the conditional update gives.
		stmt = `INSERT INTO ` + table + ` (id, body, rev, created_at, updated_at)
		        VALUES (?, ?, ?, ?, ?)
		        ON CONFLICT(id) DO NOTHING
		        RETURNING rev`
		args = []any{id, string(body), docstore.FirstRev, ts, ts}
	default:
		// No insert path: expecting a revision is expecting a document, so a
		// missing one is refused rather than created.
		stmt = `UPDATE ` + table + ` SET body = ?, rev = rev + 1, updated_at = ?
		        WHERE id = ? AND rev = ?
		        RETURNING rev`
		args = []any{string(body), ts, id, *expected}
	}

	var rev int64
	err := q.QueryRow(stmt, args...).Scan(&rev)
	// Only an expectation can refuse: the unconditional upsert always writes a
	// row, so ErrNoRows from it is a broken statement, not a conflict.
	if err == sql.ErrNoRows && expected != nil {
		return 0, documentConflictWith(q, schema, table, id, *expected)
	}
	if err != nil {
		return 0, fmt.Errorf("store: writing %s/%s/%s: %w", schema.Namespace, schema.Collection, id, err)
	}
	return rev, nil
}

// GetDocument returns one document by its address.
func (s *Store) GetDocument(schema docstore.CollectionSchema, id string) (*docstore.Document, bool, error) {
	table, err := s.documentTable(schema)
	if err != nil {
		return nil, false, err
	}
	return getDocumentWith(s.db, schema.Namespace, schema.Collection, table, id)
}

func getDocumentWith(q rowQuerier, namespace, collection, table, id string) (*docstore.Document, bool, error) {
	row := q.QueryRow(`SELECT `+documentColumns+` FROM `+table+` WHERE id = ?`, id)
	doc, err := scanDocument(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: reading %s/%s/%s: %w", namespace, collection, id, err)
	}
	return doc, true, nil
}

// DeleteDocument removes a document, reporting whether one was there. expected
// asserts which version is being removed, as in PutDocument; a stale revision
// is a *docstore.ConflictError and nothing is deleted.
func (s *Store) DeleteDocument(schema docstore.CollectionSchema, id string, expected *int64) (bool, error) {
	table, err := s.documentTable(schema)
	if err != nil {
		return false, err
	}
	return deleteDocumentWith(s.db, schema, table, id, expected)
}

func deleteDocumentWith(x execQuerier, schema docstore.CollectionSchema, table, id string, expected *int64) (bool, error) {
	if expected != nil && *expected == docstore.ExpectAbsent {
		// Treating "expect absent" as unconditional would delete a document the
		// caller was trying to protect.
		return false, fmt.Errorf("store: deleting %s/%s/%s: rev %d means the document must not exist, which a delete cannot expect; pass the revision you read, or none to delete unconditionally",
			schema.Namespace, schema.Collection, id, docstore.ExpectAbsent)
	}

	stmt := `DELETE FROM ` + table + ` WHERE id = ?`
	args := []any{id}
	if expected != nil {
		stmt += ` AND rev = ?`
		args = append(args, *expected)
	}
	res, err := x.Exec(stmt, args...)
	if err != nil {
		return false, fmt.Errorf("store: deleting %s/%s/%s: %w", schema.Namespace, schema.Collection, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 && expected != nil {
		return false, documentConflictWith(x, schema, table, id, *expected)
	}
	return n > 0, nil
}

// documentConflictWith describes a refused write, re-reading to name the
// revision that won. The re-read must run on whatever refused the write —
// inside a composite, its transaction — or it reads a state the refusal never
// saw.
func documentConflictWith(q rowQuerier, schema docstore.CollectionSchema, table, id string, expected int64) error {
	conflict := &docstore.ConflictError{
		Namespace: schema.Namespace, Collection: schema.Collection, ID: id, Expected: expected,
	}
	var rev int64
	switch err := q.QueryRow(`SELECT rev FROM `+table+` WHERE id = ?`, id).Scan(&rev); {
	case err == nil:
		conflict.Found = true
		conflict.Actual = rev
	case err != sql.ErrNoRows:
		return fmt.Errorf("store: %s/%s/%s was refused, and re-reading it to say why also failed: %w",
			schema.Namespace, schema.Collection, id, err)
	}
	return conflict
}

// DocumentWrite is one document mutation: a put of Body, or a removal when
// Delete is set. Expected is as in PutDocument and DeleteDocument.
type DocumentWrite struct {
	Schema   docstore.CollectionSchema
	ID       string
	Body     []byte
	Delete   bool
	Expected *int64
}

// DocumentWriteResult reports a committed write: Changed says whether the
// store moved, Seq is the fact's log position (0 exactly when Changed is
// false), Rev is the new revision (0 for a delete).
type DocumentWriteResult struct {
	Rev     int64
	Seq     int64
	Changed bool
}

// CommitDocumentWrite writes a document and appends the fact describing it in
// ONE transaction — a fact that cannot be made durable fails the whole write,
// so the data and the log cannot diverge. The store stays fact-agnostic: the
// caller builds the fact. A write that changed nothing appends NO fact and
// returns no seq.
func (s *Store) CommitDocumentWrite(w DocumentWrite, fact BusEvent, now time.Time) (DocumentWriteResult, error) {
	table, err := s.documentTable(w.Schema)
	if err != nil {
		return DocumentWriteResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return DocumentWriteResult{}, fmt.Errorf("store: writing %s/%s/%s: %w",
			w.Schema.Namespace, w.Schema.Collection, w.ID, err)
	}
	// Also rolls back the no-error paths: a delete that removed nothing has
	// nothing to commit.
	defer func() { _ = tx.Rollback() }()

	var out DocumentWriteResult
	if w.Delete {
		existed, err := deleteDocumentWith(tx, w.Schema, table, w.ID, w.Expected)
		if err != nil {
			return DocumentWriteResult{}, err
		}
		out.Changed = existed
	} else {
		rev, err := putDocumentWith(tx, w.Schema, table, w.ID, w.Body, now, w.Expected)
		if err != nil {
			return DocumentWriteResult{}, err
		}
		out.Rev = rev
		out.Changed = true
	}

	if !out.Changed {
		return out, nil
	}

	seq, err := appendBusEventWith(tx, fact, now)
	if err != nil {
		return DocumentWriteResult{}, fmt.Errorf("store: announcing the write to %s/%s/%s: %w",
			w.Schema.Namespace, w.Schema.Collection, w.ID, err)
	}
	out.Seq = seq

	if err := tx.Commit(); err != nil {
		return DocumentWriteResult{}, fmt.Errorf("store: committing the write to %s/%s/%s: %w",
			w.Schema.Namespace, w.Schema.Collection, w.ID, err)
	}
	return out, nil
}

// queryDocuments runs an already-compiled query; only docstore-checked
// identifiers are spliced in, every caller value is a bound argument.
// Unexported: a compiled query is only correct against the state it was
// compiled from, so ReadQuery owns both halves and is the way in from outside.
func (s *Store) queryDocuments(c docstore.Compiled) ([]docstore.Document, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	return queryDocumentsWith(s.db, c)
}

func queryDocumentsWith(q rowsQuerier, c docstore.Compiled) ([]docstore.Document, error) {
	if err := docstore.ValidateTableName(c.Table); err != nil {
		return nil, err
	}
	stmt := `SELECT ` + documentColumns + ` FROM ` + c.Table
	if c.Where != "" {
		stmt += ` WHERE ` + c.Where
	}
	stmt += ` ORDER BY ` + c.Order + ` LIMIT ?`

	rows, err := q.Query(stmt, append(append([]any{}, c.Args...), c.Limit)...)
	if err != nil {
		return nil, fmt.Errorf("store: querying documents: %w", err)
	}
	defer rows.Close()
	out := []docstore.Document{}
	for rows.Next() {
		doc, err := scanDocument(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning document: %w", err)
		}
		out = append(out, *doc)
	}
	return out, rows.Err()
}

// QueryRead is one answer to one query: the documents, the declaration they
// were computed against, and the log position they were true at.
type QueryRead struct {
	Schema    docstore.CollectionSchema
	Documents []docstore.Document
	AsOfSeq   int64
}

// readAsOfSeq is the log position an answer was true at: MAX(seq) in
// bus_events, read inside the same transaction as the rows — outside it the
// number names a state the rows were never in. Empty log yields 0 ("before
// everything"); compaction never removes the newest row, so it cannot lower
// MAX(seq).
func readAsOfSeq(q rowQuerier) (int64, error) {
	var seq int64
	if err := q.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM bus_events`).Scan(&seq); err != nil {
		return 0, fmt.Errorf("store: reading the log position of this answer: %w", err)
	}
	return seq, nil
}

// DocumentRead is one answer to one document read: the document if it is there,
// and the log position the answer was true at.
type DocumentRead struct {
	Document *docstore.Document
	Found    bool
	AsOfSeq  int64
}

// ReadDocument answers a get in a single read transaction, for the same reason
// ReadQuery does; the bool is false with a nil error when the collection was
// never declared.
func (s *Store) ReadDocument(namespace, collection, id string) (DocumentRead, bool, error) {
	if s.db == nil {
		return DocumentRead{}, false, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return DocumentRead{}, false, fmt.Errorf("store: reading %s/%s/%s: %w", namespace, collection, id, err)
	}
	defer func() { _ = tx.Rollback() }()

	schema, table, found, err := readCollectionTx(tx, namespace, collection)
	if err != nil || !found {
		return DocumentRead{}, false, err
	}
	doc, ok, err := getDocumentWith(tx, schema.Namespace, schema.Collection, table, id)
	if err != nil {
		return DocumentRead{}, false, err
	}
	asOf, err := readAsOfSeq(tx)
	if err != nil {
		return DocumentRead{}, false, err
	}
	return DocumentRead{Document: doc, Found: ok, AsOfSeq: asOf}, true, nil
}

// CountRead is one answer to a count: how many documents match, and the log
// position that was true at.
type CountRead struct {
	Schema  docstore.CollectionSchema
	Count   int
	AsOfSeq int64
}

// CountQuery answers "how many match" with the same compile the query itself
// uses, so a count and the page it describes cannot disagree; only the filter
// half is used, and an after cursor counts what follows the anchor.
func (s *Store) CountQuery(q docstore.Query) (CountRead, bool, error) {
	if s.db == nil {
		return CountRead{}, false, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return CountRead{}, false, fmt.Errorf("store: counting %s/%s: %w", q.Namespace, q.Collection, err)
	}
	defer func() { _ = tx.Rollback() }()

	schema, table, found, err := readCollectionTx(tx, q.Namespace, q.Collection)
	if err != nil || !found {
		return CountRead{}, false, err
	}
	var anchor *docstore.Document
	if q.After != "" {
		doc, ok, err := getDocumentWith(tx, schema.Namespace, schema.Collection, table, q.After)
		if err != nil {
			return CountRead{}, false, err
		}
		if ok {
			anchor = doc
		}
	}
	compiled, err := q.Compile(schema, anchor)
	if err != nil {
		return CountRead{}, false, err
	}
	if err := docstore.ValidateTableName(compiled.Table); err != nil {
		return CountRead{}, false, err
	}
	stmt := `SELECT COUNT(*) FROM ` + compiled.Table
	if compiled.Where != "" {
		stmt += ` WHERE ` + compiled.Where
	}
	var n int
	if err := tx.QueryRow(stmt, compiled.Args...).Scan(&n); err != nil {
		return CountRead{}, false, fmt.Errorf("store: counting %s/%s: %w", q.Namespace, q.Collection, err)
	}
	asOf, err := readAsOfSeq(tx)
	if err != nil {
		return CountRead{}, false, err
	}
	return CountRead{Schema: schema, Count: n, AsOfSeq: asOf}, true, nil
}

// ReadQuery answers a query in a single read transaction, and is the only way
// to run one from outside the store. Declaration read, anchor read, compile,
// and SELECT must share one transaction: split across transactions the
// statement can compile against one state and execute against another, which
// silently returned wrong pages. found reports whether the collection is
// declared, as in DocumentCollection.
func (s *Store) ReadQuery(q docstore.Query) (QueryRead, bool, error) {
	if s.db == nil {
		return QueryRead{}, false, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return QueryRead{}, false, fmt.Errorf("store: reading %s/%s: %w", q.Namespace, q.Collection, err)
	}
	defer func() { _ = tx.Rollback() }()

	schema, table, found, err := readCollectionTx(tx, q.Namespace, q.Collection)
	if err != nil || !found {
		return QueryRead{}, false, err
	}

	var anchor *docstore.Document
	if q.After != "" {
		// A missing anchor stays nil; Compile refuses that case by name.
		doc, ok, err := getDocumentWith(tx, schema.Namespace, schema.Collection, table, q.After)
		if err != nil {
			return QueryRead{}, false, err
		}
		if ok {
			anchor = doc
		}
	}

	compiled, err := q.Compile(schema, anchor)
	if err != nil {
		return QueryRead{}, false, err
	}
	docs, err := queryDocumentsWith(tx, compiled)
	if err != nil {
		return QueryRead{}, false, err
	}
	asOf, err := readAsOfSeq(tx)
	if err != nil {
		return QueryRead{}, false, err
	}
	return QueryRead{Schema: schema, Documents: docs, AsOfSeq: asOf}, true, nil
}

// CountDocuments reports how many documents a collection holds.
func (s *Store) CountDocuments(schema docstore.CollectionSchema) (int, error) {
	table, err := s.documentTable(schema)
	if err != nil {
		return 0, err
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: counting %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	return n, nil
}

// QueryPlan returns SQLite's plan for a compiled query, one row per step, so a
// test can assert a query reaches an index rather than scanning.
func (s *Store) QueryPlan(c docstore.Compiled) ([]string, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	if err := docstore.ValidateTableName(c.Table); err != nil {
		return nil, err
	}
	stmt := `SELECT ` + documentColumns + ` FROM ` + c.Table
	if c.Where != "" {
		stmt += ` WHERE ` + c.Where
	}
	stmt += ` ORDER BY ` + c.Order + ` LIMIT ?`

	rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+stmt, append(append([]any{}, c.Args...), c.Limit)...)
	if err != nil {
		return nil, fmt.Errorf("store: explaining query: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			return nil, fmt.Errorf("store: scanning query plan: %w", err)
		}
		out = append(out, detail)
	}
	return out, rows.Err()
}

// documentTable resolves the table a document operation runs against, refusing
// a schema that did not come from a read of the registry.
func (s *Store) documentTable(schema docstore.CollectionSchema) (string, error) {
	if s.db == nil {
		return "", fmt.Errorf("store: no database")
	}
	if err := docstore.ValidateTableName(schema.Table); err != nil {
		return "", fmt.Errorf("store: %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	return schema.Table, nil
}

// ---------------------------------------------------------------------------
// DDL
// ---------------------------------------------------------------------------

// createCollectionTable builds a collection's storage: stored columns, indexes
// for the reserved ordering columns, and a generated column plus index per
// declared field. WITHOUT ROWID clusters the row on the id it is looked up by;
// rev is deliberately unindexed — it serves no query.
func createCollectionTable(tx *sql.Tx, table string, fields []docstore.FieldSpec) error {
	if err := docstore.ValidateTableName(table); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`CREATE TABLE %s (
    id         TEXT NOT NULL PRIMARY KEY,
    body       TEXT NOT NULL,
    rev        INTEGER NOT NULL DEFAULT %d,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) WITHOUT ROWID`, table, docstore.FirstRev)); err != nil {
		return err
	}
	// created_at and updated_at are queryable without being declared, so they
	// are indexed unconditionally.
	for _, col := range []string{docstore.FieldCreatedAt, docstore.FieldUpdatedAt} {
		if err := createFieldIndex(tx, table, col); err != nil {
			return err
		}
	}
	for _, f := range fields {
		if err := addFieldColumn(tx, table, f); err != nil {
			return err
		}
	}
	return nil
}

// alterCollectionTable brings an existing table into line with a new
// declaration; a field whose type changed is dropped and re-added so its
// column carries the new affinity.
func alterCollectionTable(tx *sql.Tx, table string, before, after []docstore.FieldSpec) error {
	if err := docstore.ValidateTableName(table); err != nil {
		return err
	}
	old := make(map[string]docstore.FieldSpec, len(before))
	for _, f := range before {
		old[f.Name] = f
	}
	want := make(map[string]docstore.FieldSpec, len(after))
	for _, f := range after {
		want[f.Name] = f
	}

	for _, f := range before {
		next, kept := want[f.Name]
		if kept && next.Type == f.Type {
			continue
		}
		if err := dropFieldColumn(tx, table, f); err != nil {
			return err
		}
	}
	for _, f := range after {
		prev, existed := old[f.Name]
		if existed && prev.Type == f.Type {
			continue
		}
		if err := addFieldColumn(tx, table, f); err != nil {
			return err
		}
	}
	return nil
}

func addFieldColumn(tx *sql.Tx, table string, f docstore.FieldSpec) error {
	col := quoteIdent(docstore.FieldColumn(f.Name))
	// VIRTUAL, not STORED: the index already materialises the compared values,
	// and SQLite refuses to add a STORED column to an existing table.
	_, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s GENERATED ALWAYS AS (%s) VIRTUAL`,
		table, col, docstore.ColumnAffinity(f.Type), docstore.FieldExpression(f.Name)))
	if err != nil {
		return err
	}
	return createFieldIndex(tx, table, docstore.FieldColumn(f.Name))
}

func dropFieldColumn(tx *sql.Tx, table string, f docstore.FieldSpec) error {
	column := docstore.FieldColumn(f.Name)
	// The index goes first: SQLite refuses to drop an indexed column.
	if _, err := tx.Exec(`DROP INDEX IF EXISTS ` + quoteIdent(fieldIndexName(table, column))); err != nil {
		return err
	}
	_, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s`, table, quoteIdent(column)))
	return err
}

// createFieldIndex indexes one column with the id tiebreaker beside it — the
// tuple every query orders and cursors by; SQLite walks it backwards for DESC.
func createFieldIndex(tx *sql.Tx, table, column string) error {
	_, err := tx.Exec(fmt.Sprintf(`CREATE INDEX %s ON %s (%s, id)`,
		quoteIdent(fieldIndexName(table, column)), table, quoteIdent(column)))
	return err
}

// fieldIndexName is unique across the database because the table name is.
func fieldIndexName(table, column string) string {
	return table + "_" + column
}

// quoteIdent renders an already-validated identifier for SQL; quoting makes a
// field named like a keyword harmless.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// ---------------------------------------------------------------------------
// Registry reads
// ---------------------------------------------------------------------------

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

type rowsQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// execQuerier is a write that may have to read to explain itself: a refused
// delete re-reads the revision that won.
type execQuerier interface {
	execer
	rowQuerier
}

// readCollection reads a declaration and the table minted for it.
func readCollection(q rowQuerier, namespace, collection string) (docstore.CollectionSchema, string, bool, error) {
	schema := docstore.CollectionSchema{Namespace: namespace, Collection: collection}
	var (
		id     int64
		fields string
	)
	err := q.QueryRow(
		`SELECT id, fields_json FROM document_collections WHERE namespace = ? AND collection = ?`,
		namespace, collection).Scan(&id, &fields)
	switch {
	case err == sql.ErrNoRows:
		return schema, "", false, nil
	case err != nil:
		return schema, "", false, fmt.Errorf("store: reading %s/%s: %w", namespace, collection, err)
	}
	if err := json.Unmarshal([]byte(fields), &schema.Fields); err != nil {
		return schema, "", false, fmt.Errorf("store: decoding fields for %s/%s: %w", namespace, collection, err)
	}
	table := docstore.TableName(id)
	schema.Table = table
	return schema, table, true, nil
}

func readCollectionTx(tx *sql.Tx, namespace, collection string) (docstore.CollectionSchema, string, bool, error) {
	return readCollection(tx, namespace, collection)
}

func scanDocument(sc rowScanner) (*docstore.Document, error) {
	var (
		doc                    docstore.Document
		body                   string
		createdStr, updatedStr string
	)
	if err := sc.Scan(&doc.ID, &body, &doc.Rev, &createdStr, &updatedStr); err != nil {
		return nil, err
	}
	doc.Body = json.RawMessage(body)
	doc.CreatedAt = parseStoreTime(createdStr)
	doc.UpdatedAt = parseStoreTime(updatedStr)
	return &doc, nil
}
