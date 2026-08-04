package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// SQLite persistence for the document store. This file is only persistence:
// what a query means, and the SQL a validated one compiles to, live in
// internal/docstore, which reaches nothing — the same split internal/bus and
// bus.go use.
//
// ONE TABLE PER COLLECTION. `document_collections` is the registry: one row per
// declared collection, whose row id mints the name of the table holding its
// documents (`doc_<id>`). A declared field is an indexed VIRTUAL generated
// column over the body in that table, so a query reads an index instead of
// scanning, while the body is still stored and returned byte for byte and
// declaring a field rewrites no document. The measurement behind the shape is
// in docs/plans/2026-08-03-ext-a3.1-doc-store-physical-schema.md.
//
// Isolation is structural rather than a check: a collection's documents are the
// only rows in its table, so there is no read or write here that could reach
// another namespace even if its predicate were wrong.
//
// Every identifier spliced into the SQL below comes from docstore — a table
// name derived from an integer, a column name derived from a field name that
// matched a validating pattern. None of it is caller text.

// documentColumns is the read projection; body is returned byte for byte. The
// generated columns are never selected: they exist to be filtered and ordered
// on, and the body already carries what they compute.
//
// rev is in the projection rather than fetched on demand because it is the token
// a read-modify-write hands back, and a caller that had to ask for it separately
// would be reading a version it did not read the body of.
const documentColumns = `id, body, rev, created_at, updated_at`

// DefineDocumentCollection records a collection's declaration and brings its
// table into line with it, creating the table on first declaration. Redeclaring
// is how a collection gains or loses a queryable field: an added field is a new
// generated column plus its index, a removed one drops both, and a field whose
// type changed is replaced so its column carries the new affinity. Documents are
// never rewritten — a VIRTUAL column computes from the body, so it applies to
// every document already stored the moment it exists.
//
// Registry row and DDL commit together. A declaration whose table did not get
// built, or a table no declaration names, would each be a collection that
// cannot be queried or cannot be found.
func (s *Store) DefineDocumentCollection(schema docstore.CollectionSchema, now time.Time) error {
	if s.db == nil {
		return fmt.Errorf("store: no database")
	}
	if err := schema.Validate(); err != nil {
		return err
	}
	fields, err := json.Marshal(schema.Fields)
	if err != nil {
		return fmt.Errorf("store: encoding fields for %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	ts := now.UTC().Format(docstore.TimeFormat)

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: defining %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, table, found, err := readCollectionTx(tx, schema.Namespace, schema.Collection)
	if err != nil {
		return err
	}

	if !found {
		res, err := tx.Exec(
			`INSERT INTO document_collections (namespace, collection, fields_json, updated_at) VALUES (?, ?, ?, ?)`,
			schema.Namespace, schema.Collection, string(fields), ts)
		if err != nil {
			return fmt.Errorf("store: defining %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("store: defining %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
		table = docstore.TableName(id)
		if err := createCollectionTable(tx, table, schema.Fields); err != nil {
			return fmt.Errorf("store: creating storage for %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
	} else {
		if err := alterCollectionTable(tx, table, existing.Fields, schema.Fields); err != nil {
			return fmt.Errorf("store: redeclaring %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
		if _, err := tx.Exec(
			`UPDATE document_collections SET fields_json = ?, updated_at = ? WHERE namespace = ? AND collection = ?`,
			string(fields), ts, schema.Namespace, schema.Collection); err != nil {
			return fmt.Errorf("store: redeclaring %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing declaration of %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	return nil
}

// DocumentCollection returns a collection's declaration with its table filled
// in. The bool is false with a nil error when the collection was never declared
// — a caller must tell "no such collection" apart from a read failure, because
// the first is what every query against an undeclared collection has to report.
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

// ListDocumentCollections returns every declaration, namespace-major. It is the
// operator's index of what exists.
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
// reporting how many documents went. Dropping the table is what returns the
// space, and it happens in the same transaction as the registry delete: a
// declaration without its table would leave a collection nothing can query, and
// a table without its declaration would leave rows nothing can name.
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

// PutDocument writes a document, creating or fully replacing it, and returns the
// revision it now has. created_at survives a replacement — it is when the record
// first appeared, which is what a "newest first" query means by it — while
// updated_at and rev move on every write.
//
// The schema names the table, which is why every caller reads the declaration
// first: an undeclared collection has no storage, and that has to be an error a
// caller reports rather than a table appearing by surprise.
//
// expected is the caller's assertion about what it is overwriting: nil writes
// unconditionally, docstore.ExpectAbsent writes only if nothing is there, and a
// revision writes only if the document is still at it. A failed assertion is a
// *docstore.ConflictError and nothing is written.
//
// EACH FORM IS ONE STATEMENT, which is what makes the check atomic without a
// transaction: SQLite runs a bare statement in its own. Reading the revision and
// then writing would be two, and the whole point of this is the window between
// them. The re-read below happens only on the failure path, to say what the
// write lost to.
func (s *Store) PutDocument(schema docstore.CollectionSchema, id string, body []byte, now time.Time, expected *int64) (int64, error) {
	table, err := s.documentTable(schema)
	if err != nil {
		return 0, err
	}
	ts := now.UTC().Format(docstore.TimeFormat)

	var (
		stmt string
		args []any
	)
	switch {
	case expected == nil:
		// Upsert: the insert path starts at FirstRev, the update path advances.
		stmt = `INSERT INTO ` + table + ` (id, body, rev, created_at, updated_at)
		        VALUES (?, ?, ?, ?, ?)
		        ON CONFLICT(id) DO UPDATE SET
		          body=excluded.body,
		          rev=` + table + `.rev + 1,
		          updated_at=excluded.updated_at
		        RETURNING rev`
		args = []any{id, string(body), docstore.FirstRev, ts, ts}
	case *expected == docstore.ExpectAbsent:
		// DO NOTHING rather than letting the primary key raise: a conflicting
		// insert then returns no row, which is the same "no row came back" signal
		// the conditional update gives, so both refusals arrive one way.
		stmt = `INSERT INTO ` + table + ` (id, body, rev, created_at, updated_at)
		        VALUES (?, ?, ?, ?, ?)
		        ON CONFLICT(id) DO NOTHING
		        RETURNING rev`
		args = []any{id, string(body), docstore.FirstRev, ts, ts}
	default:
		// No insert path at all: expecting a revision is expecting a document,
		// so a missing one must be refused rather than created.
		stmt = `UPDATE ` + table + ` SET body = ?, rev = rev + 1, updated_at = ?
		        WHERE id = ? AND rev = ?
		        RETURNING rev`
		args = []any{string(body), ts, id, *expected}
	}

	var rev int64
	err = s.db.QueryRow(stmt, args...).Scan(&rev)
	// No row came back is how every refusal arrives — but only an expectation can
	// refuse one. The unconditional upsert always writes a row, so no row from it
	// is a broken statement rather than a conflict, and reporting it as one would
	// tell a caller to retry something that will never succeed.
	if err == sql.ErrNoRows && expected != nil {
		return 0, s.documentConflict(schema, table, id, *expected)
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
	row := s.db.QueryRow(`SELECT `+documentColumns+` FROM `+table+` WHERE id = ?`, id)
	doc, err := scanDocument(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: reading %s/%s/%s: %w", schema.Namespace, schema.Collection, id, err)
	}
	return doc, true, nil
}

// DeleteDocument removes a document, reporting whether one was there. The caller
// needs the difference: a delete that removed nothing must not announce a change
// that did not happen.
//
// expected asserts which version is being removed, the same way PutDocument's
// does — removing a record on the strength of a body you read is the same
// lost-update hazard as overwriting one. A revision that no longer matches is a
// *docstore.ConflictError and nothing is deleted.
func (s *Store) DeleteDocument(schema docstore.CollectionSchema, id string, expected *int64) (bool, error) {
	table, err := s.documentTable(schema)
	if err != nil {
		return false, err
	}
	if expected != nil && *expected == docstore.ExpectAbsent {
		// "Delete this if it is not there" is not an assertion a delete can act
		// on, and silently treating it as unconditional would delete a document
		// the caller was trying to protect.
		return false, fmt.Errorf("store: deleting %s/%s/%s: rev %d means the document must not exist, which a delete cannot expect; pass the revision you read, or none to delete unconditionally",
			schema.Namespace, schema.Collection, id, docstore.ExpectAbsent)
	}

	stmt := `DELETE FROM ` + table + ` WHERE id = ?`
	args := []any{id}
	if expected != nil {
		stmt += ` AND rev = ?`
		args = append(args, *expected)
	}
	res, err := s.db.Exec(stmt, args...)
	if err != nil {
		return false, fmt.Errorf("store: deleting %s/%s/%s: %w", schema.Namespace, schema.Collection, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 && expected != nil {
		return false, s.documentConflict(schema, table, id, *expected)
	}
	return n > 0, nil
}

// documentConflict describes a refused write. It reads the document again to
// name the revision that won, because "your write was refused" without that is
// an error a caller cannot act on. A read failure here is not worth losing the
// conflict over — the refusal is the fact, the revision is the detail — so it
// degrades to reporting the document as absent.
func (s *Store) documentConflict(schema docstore.CollectionSchema, table, id string, expected int64) error {
	conflict := &docstore.ConflictError{
		Namespace: schema.Namespace, Collection: schema.Collection, ID: id, Expected: expected,
	}
	var rev int64
	switch err := s.db.QueryRow(`SELECT rev FROM `+table+` WHERE id = ?`, id).Scan(&rev); {
	case err == nil:
		conflict.Found = true
		conflict.Actual = rev
	case err != sql.ErrNoRows:
		return fmt.Errorf("store: %s/%s/%s was refused, and re-reading it to say why also failed: %w",
			schema.Namespace, schema.Collection, id, err)
	}
	return conflict
}

// QueryDocuments runs a compiled query. The compiled fragments are built from a
// validated query against a stored declaration, so the only strings spliced into
// the statement are ones docstore produced from identifiers it checked; every
// caller-supplied value arrives as a bound argument.
func (s *Store) QueryDocuments(c docstore.Compiled) ([]docstore.Document, error) {
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

	rows, err := s.db.Query(stmt, append(append([]any{}, c.Args...), c.Limit)...)
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

// CountDocuments reports how many documents a collection holds. It is what the
// slow-query log reports alongside a duration.
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

// QueryPlan returns SQLite's plan for a compiled query, one row per step. It
// exists so a test can assert that a filtered or sorted query reaches an index
// rather than scanning the collection — the property this whole physical schema
// is for, and one that no timing assertion could check without being flaky.
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

// createCollectionTable builds a collection's storage: the five stored columns,
// an index for each reserved ordering column, and a generated column plus index
// for every declared field.
//
// WITHOUT ROWID because a document is addressed by its id and never by position:
// clustering the row on the id it is looked up by removes a whole B-tree and the
// hop through it.
//
// rev is stored and not indexed. It is only ever read for a document already
// being looked up by its primary key, or compared inside a write to that same
// row, so an index over it would serve no query and cost every write.
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
	// are indexed unconditionally. Each index carries the id tiebreaker, which
	// is what makes it serve the whole ordering tuple rather than only its
	// first half — the same tuple the after cursor compares against.
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
// declaration. A field whose type changed is dropped and re-added rather than
// left alone: its column's affinity is how two stored values compare, so a
// declaration that says "number" must not keep comparing as text.
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
	// VIRTUAL, not STORED: the index materialises the values that get compared,
	// so storing them in the row as well would pay for them twice. SQLite also
	// refuses to add a STORED column to an existing table, which is what would
	// make redeclaring a rewrite instead of a one-statement change.
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

// createFieldIndex indexes one column with the id tiebreaker beside it, which is
// the order every query asks for. One index serves both directions: SQLite walks
// it backwards for a DESC ordering.
func createFieldIndex(tx *sql.Tx, table, column string) error {
	_, err := tx.Exec(fmt.Sprintf(`CREATE INDEX %s ON %s (%s, id)`,
		quoteIdent(fieldIndexName(table, column)), table, quoteIdent(column)))
	return err
}

// fieldIndexName is unique across the database because the table name is.
func fieldIndexName(table, column string) string {
	return table + "_" + column
}

// quoteIdent renders an identifier for SQL. Every identifier reaching it is
// already derived from a validated name; the quoting is what makes a field named
// like a keyword harmless.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// ---------------------------------------------------------------------------
// Registry reads
// ---------------------------------------------------------------------------

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
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
