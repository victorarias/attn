package client

import (
	"encoding/json"
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

// DocSubscribe opens a live query and calls onResult for every delivery,
// beginning with the current result set. Unlike every other call here it holds
// its connection open, so it returns only when onResult asks it to stop, the
// daemon closes the connection, or a delivery cannot be read.
//
// Each delivery is the whole current result set; a caller renders what it is
// handed and never accumulates state across deliveries.
func (c *Client) DocSubscribe(query protocol.DocumentQuery, onResult func(*protocol.DocSubscribeResult) bool) error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return explainConnectError(c.socketPath, err)
	}
	defer conn.Close()

	msg := protocol.DocSubscribeMessage{Cmd: protocol.CmdDocSubscribe, Query: query}
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return fmt.Errorf("send subscribe: %w", err)
	}

	decoder := json.NewDecoder(conn)
	for {
		var resp protocol.Response
		if err := decoder.Decode(&resp); err != nil {
			// The daemon closing the connection ends the subscription; it is not
			// the caller's error to report.
			return nil
		}
		if !resp.Ok {
			return fmt.Errorf("daemon error: %s", protocol.Deref(resp.Error))
		}
		if resp.DocSubscribeResult == nil {
			continue
		}
		if !onResult(resp.DocSubscribeResult) {
			return nil
		}
	}
}
