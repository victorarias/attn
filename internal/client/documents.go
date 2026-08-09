package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/victorarias/attn/internal/protocol"
)

// The document store's client surface. Every call goes through the daemon, never
// the database directly: a write must publish its change fact or live queries drift.

// DocDefine declares a collection from its schema.
func (c *Client) DocDefine(schema protocol.DocumentCollectionSchema) (*protocol.DocDefineResult, error) {
	resp, err := c.send(protocol.DocDefineMessage{Cmd: protocol.CmdDocDefine, Schema: schema})
	if err != nil {
		return nil, err
	}
	return resp.DocDefineResult, nil
}

// DocUndefine removes a collection.
func (c *Client) DocUndefine(namespace, collection string) (*protocol.DocUndefineResult, error) {
	resp, err := c.send(protocol.DocUndefineMessage{
		Cmd: protocol.CmdDocUndefine, Namespace: namespace, Collection: collection,
	})
	if err != nil {
		return nil, err
	}
	return resp.DocUndefineResult, nil
}

// DocCollections lists the defined collections.
func (c *Client) DocCollections() (*protocol.DocCollectionsResult, error) {
	resp, err := c.send(protocol.DocCollectionsMessage{Cmd: protocol.CmdDocCollections})
	if err != nil {
		return nil, err
	}
	return resp.DocCollectionsResult, nil
}

// DocPut writes a document. expectedRev nil writes unconditionally; see
// DocPutMessage for what a value means.
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

// DocGet fetches one document by id.
func (c *Client) DocGet(namespace, collection, id string) (*protocol.DocGetResult, error) {
	resp, err := c.send(protocol.DocGetMessage{
		Cmd: protocol.CmdDocGet, Namespace: namespace, Collection: collection, ID: id,
	})
	if err != nil {
		return nil, err
	}
	return resp.DocGetResult, nil
}

// DocDelete deletes a document; expectedRev nil deletes unconditionally.
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

// DocQuery runs a one-shot document query.
func (c *Client) DocQuery(query protocol.DocumentQuery) (*protocol.DocQueryResult, error) {
	resp, err := c.send(protocol.DocQueryMessage{Cmd: protocol.CmdDocQuery, Query: query})
	if err != nil {
		return nil, err
	}
	return resp.DocQueryResult, nil
}

// DocCount reports how many documents a query matches, without fetching
// bodies. Sort and limit are ignored.
func (c *Client) DocCount(query protocol.DocumentQuery) (*protocol.DocCountResult, error) {
	resp, err := c.send(protocol.DocCountMessage{Cmd: protocol.CmdDocCount, Query: query})
	if err != nil {
		return nil, err
	}
	return resp.DocCountResult, nil
}

// DocWindow is one delivery of a live query, already applied — a caller renders
// Documents with no merging logic of its own. AsOfSeq is the log position the
// window was true at (comparable to a write's seq); Changed names arrived bodies.
type DocWindow struct {
	Delivery  int
	AsOfSeq   int64
	Documents []protocol.StoredDocument
	Changed   []string
}

// revisions is what a window declares to a resuming subscription. DocSubscribe
// takes whole documents so no caller can declare a revision without its body.
func revisions(held []protocol.StoredDocument) []protocol.DocumentRevision {
	out := make([]protocol.DocumentRevision, 0, len(held))
	for _, doc := range held {
		out = append(out, protocol.DocumentRevision{ID: doc.ID, Rev: doc.Rev})
	}
	return out
}

// DocSubscriptionEnded is a live query the daemon stopped serving. Code is the
// protocol error code (`collection_undefined`, `collection_redeclared`), empty
// when the connection went; a type so hosts don't route by matching English.
type DocSubscriptionEnded struct {
	Code    string
	Message string

	// lost marks the one ending safe to retry — the connection went. Not the
	// same as an empty Code: an unappliable delivery also carries no code, and
	// resubscribing after one repeats it forever. Unexported on purpose.
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

// DocConnectionLost reports whether a subscription ended because the
// connection went — the only ending worth resubscribing to; the others recur.
func DocConnectionLost(err error) bool {
	var ended *DocSubscriptionEnded
	return errors.As(err, &ended) && ended.lost
}

// DocSubscribe opens a live query and calls onWindow per delivery, returning
// when onWindow says stop or the subscription ends. held resumes from a prior
// window's Documents (nil for fresh). Ending is always an error — a stopped
// live query returning success is a watcher exiting 0 over a frozen list.
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

// applyDocDelivery is the client rule: render order, body from upsert else
// cache, forget everything not in order. Replacing the cache bounds a
// subscription's memory by the window, not its history.
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
			// The daemon believes this subscriber holds a body it does not;
			// continuing would render a window with a hole in it.
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
