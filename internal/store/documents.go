package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// SQLite persistence for the document store (migration 88). This file is only
// persistence: what a query means, and the SQL a validated one compiles to, live
// in internal/docstore, which reaches nothing — the same split internal/bus and
// bus.go use.
//
// Two tables. `documents` holds the records; `document_collections` holds each
// collection's declaration, durable so that every surface — the daemon, the CLI,
// and later an extension's manifest apply — agrees on what a collection allows
// without one of them being the live owner of that knowledge.
//
// Isolation is structural rather than a check: namespace and collection are part
// of the primary key and of every statement below, so there is no read or write
// that is not already scoped to one namespace.

// documentColumns is the read projection; body is returned byte for byte.
const documentColumns = `id, body, created_at, updated_at`

// DefineDocumentCollection records a collection's declaration, replacing any
// previous one. Redeclaring is how a collection gains a queryable field, and it
// is deliberately not a migration: documents are untouched, an added field
// becomes queryable for documents that happen to carry it, and a removed one
// stops being queryable without anything being rewritten.
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
	_, err = s.db.Exec(
		`INSERT INTO document_collections (namespace, collection, fields_json, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(namespace, collection) DO UPDATE SET
		   fields_json=excluded.fields_json,
		   updated_at=excluded.updated_at`,
		schema.Namespace, schema.Collection, string(fields), now.UTC().Format(docstore.TimeFormat))
	if err != nil {
		return fmt.Errorf("store: defining %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	return nil
}

// DocumentCollection returns a collection's declaration. The bool is false with
// a nil error when the collection was never declared — a caller must tell "no
// such collection" apart from a read failure, because the first is what every
// query against an undeclared collection has to report.
func (s *Store) DocumentCollection(namespace, collection string) (*docstore.CollectionSchema, bool, error) {
	if s.db == nil {
		return nil, false, fmt.Errorf("store: no database")
	}
	row := s.db.QueryRow(
		`SELECT fields_json FROM document_collections WHERE namespace = ? AND collection = ?`,
		namespace, collection)
	var fields string
	switch err := row.Scan(&fields); {
	case err == sql.ErrNoRows:
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("store: reading %s/%s: %w", namespace, collection, err)
	}
	schema := docstore.CollectionSchema{Namespace: namespace, Collection: collection}
	if err := json.Unmarshal([]byte(fields), &schema.Fields); err != nil {
		return nil, false, fmt.Errorf("store: decoding fields for %s/%s: %w", namespace, collection, err)
	}
	return &schema, true, nil
}

// ListDocumentCollections returns every declaration, namespace-major. It is the
// operator's index of what exists.
func (s *Store) ListDocumentCollections() ([]docstore.CollectionSchema, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	rows, err := s.db.Query(
		`SELECT namespace, collection, fields_json FROM document_collections ORDER BY namespace, collection`)
	if err != nil {
		return nil, fmt.Errorf("store: listing document collections: %w", err)
	}
	defer rows.Close()
	var out []docstore.CollectionSchema
	for rows.Next() {
		var schema docstore.CollectionSchema
		var fields string
		if err := rows.Scan(&schema.Namespace, &schema.Collection, &fields); err != nil {
			return nil, fmt.Errorf("store: scanning document collection: %w", err)
		}
		if err := json.Unmarshal([]byte(fields), &schema.Fields); err != nil {
			return nil, fmt.Errorf("store: decoding fields for %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
		out = append(out, schema)
	}
	return out, rows.Err()
}

// DeleteDocumentCollection removes a declaration and every document under it,
// reporting how many documents went. Both halves in one transaction: a
// declaration without its documents would leave records nothing can name, and
// documents without their declaration would leave records nothing can query.
func (s *Store) DeleteDocumentCollection(namespace, collection string) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: deleting %s/%s: %w", namespace, collection, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`DELETE FROM documents WHERE namespace = ? AND collection = ?`, namespace, collection)
	if err != nil {
		return 0, fmt.Errorf("store: deleting documents in %s/%s: %w", namespace, collection, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(
		`DELETE FROM document_collections WHERE namespace = ? AND collection = ?`, namespace, collection); err != nil {
		return 0, fmt.Errorf("store: deleting declaration for %s/%s: %w", namespace, collection, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: committing deletion of %s/%s: %w", namespace, collection, err)
	}
	return int(n), nil
}

// PutDocument writes a document, creating or fully replacing it. created_at
// survives a replacement — it is when the record first appeared, which is what a
// "newest first" query means by it — while updated_at moves on every write.
func (s *Store) PutDocument(namespace, collection, id string, body []byte, now time.Time) error {
	if s.db == nil {
		return fmt.Errorf("store: no database")
	}
	ts := now.UTC().Format(docstore.TimeFormat)
	_, err := s.db.Exec(
		`INSERT INTO documents (namespace, collection, id, body, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(namespace, collection, id) DO UPDATE SET
		   body=excluded.body,
		   updated_at=excluded.updated_at`,
		namespace, collection, id, string(body), ts, ts)
	if err != nil {
		return fmt.Errorf("store: writing %s/%s/%s: %w", namespace, collection, id, err)
	}
	return nil
}

// GetDocument returns one document by its address.
func (s *Store) GetDocument(namespace, collection, id string) (*docstore.Document, bool, error) {
	if s.db == nil {
		return nil, false, fmt.Errorf("store: no database")
	}
	row := s.db.QueryRow(
		`SELECT `+documentColumns+` FROM documents WHERE namespace = ? AND collection = ? AND id = ?`,
		namespace, collection, id)
	doc, err := scanDocument(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: reading %s/%s/%s: %w", namespace, collection, id, err)
	}
	return doc, true, nil
}

// DeleteDocument removes a document, reporting whether one was there. The caller
// needs the difference: a delete that removed nothing must not announce a change
// that did not happen.
func (s *Store) DeleteDocument(namespace, collection, id string) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("store: no database")
	}
	res, err := s.db.Exec(
		`DELETE FROM documents WHERE namespace = ? AND collection = ? AND id = ?`, namespace, collection, id)
	if err != nil {
		return false, fmt.Errorf("store: deleting %s/%s/%s: %w", namespace, collection, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// QueryDocuments runs a compiled query. The compiled fragments are built from a
// validated query against a stored declaration, so the only strings spliced into
// the statement are ones docstore produced from identifiers it checked; every
// caller-supplied value arrives as a bound argument.
func (s *Store) QueryDocuments(c docstore.Compiled) ([]docstore.Document, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	rows, err := s.db.Query(
		`SELECT `+documentColumns+` FROM documents WHERE `+c.Where+` ORDER BY `+c.Order+` LIMIT ?`,
		append(append([]any{}, c.Args...), c.Limit)...)
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
// slow-query log reports alongside a duration, so the day a scan gets slow the
// receipt for adding an index is already written down.
func (s *Store) CountDocuments(namespace, collection string) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM documents WHERE namespace = ? AND collection = ?`, namespace, collection).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: counting %s/%s: %w", namespace, collection, err)
	}
	return n, nil
}

func scanDocument(sc rowScanner) (*docstore.Document, error) {
	var (
		doc                    docstore.Document
		body                   string
		createdStr, updatedStr string
	)
	if err := sc.Scan(&doc.ID, &body, &createdStr, &updatedStr); err != nil {
		return nil, err
	}
	doc.Body = json.RawMessage(body)
	doc.CreatedAt = parseStoreTime(createdStr)
	doc.UpdatedAt = parseStoreTime(updatedStr)
	return &doc, nil
}
