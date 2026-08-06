package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/victorarias/attn/internal/protocol"
)

// The document store's client surface. Every call goes through the daemon rather
// than opening the database, unlike `attn bus`: a write has to publish its change
// fact, and a write that reached the table without one would leave every live
// query showing a result set the store no longer agrees with. One path, so there
// is no way to take the quiet one by accident.

func (c *Client) DocDefine(schema protocol.DocumentCollectionSchema) (*protocol.DocDefineResult, error) {
	resp, err := c.send(protocol.DocDefineMessage{Cmd: protocol.CmdDocDefine, Schema: schema})
	if err != nil {
		return nil, err
	}
	return resp.DocDefineResult, nil
}

func (c *Client) DocUndefine(namespace, collection string) (*protocol.DocUndefineResult, error) {
	resp, err := c.send(protocol.DocUndefineMessage{
		Cmd: protocol.CmdDocUndefine, Namespace: namespace, Collection: collection,
	})
	if err != nil {
		return nil, err
	}
	return resp.DocUndefineResult, nil
}

func (c *Client) DocCollections() (*protocol.DocCollectionsResult, error) {
	resp, err := c.send(protocol.DocCollectionsMessage{Cmd: protocol.CmdDocCollections})
	if err != nil {
		return nil, err
	}
	return resp.DocCollectionsResult, nil
}

// DocPut writes a document. expectedRev is the revision the caller believes it
// is replacing — nil writes unconditionally, and see DocPutMessage for what a
// value means.
func (c *Client) DocPut(namespace, collection, id, body string, expectedRev *int) (*protocol.DocPutResult, error) {
	resp, err := c.send(protocol.DocPutMessage{
		Cmd: protocol.CmdDocPut, Namespace: namespace, Collection: collection, ID: id, Body: body,
		ExpectedRev: expectedRev,
	})
	if err != nil {
		return nil, err
	}
	return resp.DocPutResult, nil
}

func (c *Client) DocGet(namespace, collection, id string) (*protocol.DocGetResult, error) {
	resp, err := c.send(protocol.DocGetMessage{
		Cmd: protocol.CmdDocGet, Namespace: namespace, Collection: collection, ID: id,
	})
	if err != nil {
		return nil, err
	}
	return resp.DocGetResult, nil
}

func (c *Client) DocDelete(namespace, collection, id string, expectedRev *int) (*protocol.DocDeleteResult, error) {
	resp, err := c.send(protocol.DocDeleteMessage{
		Cmd: protocol.CmdDocDelete, Namespace: namespace, Collection: collection, ID: id,
		ExpectedRev: expectedRev,
	})
	if err != nil {
		return nil, err
	}
	return resp.DocDeleteResult, nil
}

func (c *Client) DocQuery(query protocol.DocumentQuery) (*protocol.DocQueryResult, error) {
	resp, err := c.send(protocol.DocQueryMessage{Cmd: protocol.CmdDocQuery, Query: query})
	if err != nil {
		return nil, err
	}
	return resp.DocQueryResult, nil
}

// DocCount reports how many documents a query matches, without fetching their
// bodies. Sort and limit are ignored: they decide which matches come back,
// never how many there are.
func (c *Client) DocCount(query protocol.DocumentQuery) (*protocol.DocCountResult, error) {
	resp, err := c.send(protocol.DocCountMessage{Cmd: protocol.CmdDocCount, Query: query})
	if err != nil {
		return nil, err
	}
	return resp.DocCountResult, nil
}

// DocWindow is one delivery of a live query, already applied. The daemon sends
// an order plus the bodies the subscriber is missing; this is the result of
// following the client rule, so a caller renders Documents and holds no
// delivery-merging logic of its own. That rule lives here once, because the CLI,
// the tests and A4's SDK are all callers of this seam.
//
// AsOfSeq is the log position the window was true at — comparable against the
// seq a write reported, which is how a caller knows a delivery already includes
// its own write. Changed names the ids whose bodies arrived in this delivery,
// so a caller can see what actually travelled rather than infer it.
type DocWindow struct {
	Delivery  int
	AsOfSeq   int64
	Documents []protocol.StoredDocument
	Changed   []string
}

// revisions is what this window declares to a resuming subscription. The wire
// carries revisions alone, but DocSubscribe takes whole documents: a subscriber
// that declares a revision without holding its body has told the daemon not to
// send something it cannot render, and no caller should be able to express that
// by accident.
func revisions(held []protocol.StoredDocument) []protocol.DocumentRevision {
	out := make([]protocol.DocumentRevision, 0, len(held))
	for _, doc := range held {
		out = append(out, protocol.DocumentRevision{ID: doc.ID, Rev: doc.Rev})
	}
	return out
}

// DocSubscriptionEnded is a live query the daemon stopped serving. Code is the
// protocol error code when the daemon named one — `collection_undefined` and
// `collection_redeclared` are the two that mean the collection moved out from
// under an accepted subscription — and is empty when the connection simply went
// away. It is a type rather than a message because a UI host has to tell "kill
// this tile" from "reconnect", and must not do it by matching English.
type DocSubscriptionEnded struct {
	Code    string
	Message string

	// lost distinguishes the one ending that is safe to retry — the connection
	// went — from every other one. It is not the same fact as an empty Code: a
	// delivery this client cannot apply also carries no daemon code, and
	// resubscribing after one repeats it with the same declared revisions,
	// forever. Set here rather than exported so no caller can claim it.
	lost bool
}

func (e *DocSubscriptionEnded) Error() string {
	if e.Code == "" {
		return "document subscription ended: " + e.Message
	}
	return "document subscription ended (" + e.Code + "): " + e.Message
}

// DocSubscriptionCode returns the protocol error code a subscription ended with,
// and whether the error was a subscription ending at all.
func DocSubscriptionCode(err error) (string, bool) {
	var ended *DocSubscriptionEnded
	if errors.As(err, &ended) {
		return ended.Code, true
	}
	return "", false
}

// DocConnectionLost reports whether a subscription ended because the connection
// went. That is the only ending worth resubscribing to: a daemon that refused
// the query and a delivery this client could not apply both recur on the next
// attempt, so retrying either is a loop that reports nothing.
func DocConnectionLost(err error) bool {
	var ended *DocSubscriptionEnded
	return errors.As(err, &ended) && ended.lost
}

// DocSubscribe opens a live query and calls onWindow for every delivery,
// beginning with the query's current window. Unlike every other call here it
// holds its connection open, so it returns only when onWindow asks it to stop,
// or when the subscription ends.
//
// held is what the caller already holds — nil for a fresh subscribe, and a
// previous window's Documents to resume one. The daemon is told their revisions
// and sends bodies only for what has changed since; everything else is carried
// by order and taken from the cache seeded here.
//
// Ending is always an error, including the plain end-of-connection case. A live
// query that stops is a caller who has stopped seeing changes, and returning
// success there is how a watcher exits 0 while showing a frozen list.
func (c *Client) DocSubscribe(query protocol.DocumentQuery, held []protocol.StoredDocument, onWindow func(DocWindow) bool) error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return explainConnectError(c.socketPath, err)
	}
	defer conn.Close()

	msg := protocol.DocSubscribeMessage{Cmd: protocol.CmdDocSubscribe, Query: query, Have: revisions(held)}
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return fmt.Errorf("send subscribe: %w", err)
	}

	// The cache the client rule reads from, seeded with what the caller declared.
	// An id the daemon believes is held and that is not here is a broken
	// subscription, reported rather than rendered with a hole in it.
	cache := make(map[string]protocol.StoredDocument, len(held))
	for _, doc := range held {
		cache[doc.ID] = doc
	}
	decoder := json.NewDecoder(conn)
	for {
		var resp protocol.Response
		if err := decoder.Decode(&resp); err != nil {
			return &DocSubscriptionEnded{Message: "the daemon closed the connection", lost: true}
		}
		if !resp.Ok {
			return &DocSubscriptionEnded{
				Code:    protocol.Deref(resp.ErrorCode),
				Message: protocol.Deref(resp.Error),
			}
		}
		if resp.DocSubscribeResult == nil {
			continue
		}
		window, err := applyDocDelivery(cache, resp.DocSubscribeResult)
		if err != nil {
			return err
		}
		if !onWindow(window) {
			return nil
		}
	}
}

// applyDocDelivery is the client rule: render order, take each body from upsert
// if it is there and from the cache otherwise, forget everything not in order.
// The cache is replaced rather than added to, which is what keeps a long-lived
// subscription's memory bounded by the window rather than by its history.
func applyDocDelivery(cache map[string]protocol.StoredDocument, result *protocol.DocSubscribeResult) (DocWindow, error) {
	window := DocWindow{
		Delivery:  result.Delivery,
		AsOfSeq:   int64(result.AsOfSeq),
		Documents: make([]protocol.StoredDocument, 0, len(result.Order)),
		Changed:   make([]string, 0, len(result.Upsert)),
	}
	arrived := make(map[string]protocol.StoredDocument, len(result.Upsert))
	for _, doc := range result.Upsert {
		arrived[doc.ID] = doc
		window.Changed = append(window.Changed, doc.ID)
	}
	next := make(map[string]protocol.StoredDocument, len(result.Order))
	for _, id := range result.Order {
		doc, ok := arrived[id]
		if !ok {
			doc, ok = cache[id]
		}
		if !ok {
			// The daemon believes this subscriber holds a body it does not. The
			// subscription cannot be applied, and continuing would render a
			// window with a hole in it.
			return DocWindow{}, &DocSubscriptionEnded{Message: fmt.Sprintf(
				"delivery %d ordered %q without sending its body, and it is not held; resubscribe without a resume token",
				result.Delivery, id)}
		}
		next[id] = doc
		window.Documents = append(window.Documents, doc)
	}
	clear(cache)
	for id, doc := range next {
		cache[id] = doc
	}
	return window, nil
}
