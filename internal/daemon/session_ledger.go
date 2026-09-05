package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The one query both doors build. A page fetched with `before` carries no
// facets: the filter choices belong to the query, not to the page.
func ledgerQuery(msg *protocol.SessionListMessage, wantFacets bool) (store.SessionLedgerQuery, error) {
	scope := store.SessionLedgerLive
	switch {
	case protocol.Deref(msg.All):
		scope = store.SessionLedgerAll
	case protocol.Deref(msg.Closed):
		scope = store.SessionLedgerClosed
	}

	before := strings.TrimSpace(protocol.Deref(msg.Before))
	query := store.SessionLedgerQuery{
		Scope:       scope,
		Limit:       protocol.Deref(msg.Limit),
		Before:      before,
		WorkspaceID: strings.TrimSpace(protocol.Deref(msg.WorkspaceID)),
		Repository:  strings.TrimSpace(protocol.Deref(msg.Repository)),
		Facets:      wantFacets && before == "",
	}

	since, err := ledgerInstantArg("since", protocol.Deref(msg.Since))
	if err != nil {
		return store.SessionLedgerQuery{}, err
	}
	until, err := ledgerInstantArg("until", protocol.Deref(msg.Until))
	if err != nil {
		return store.SessionLedgerQuery{}, err
	}
	if !since.IsZero() && !until.IsZero() && !until.After(since) {
		return store.SessionLedgerQuery{}, fmt.Errorf("since %s is not before until %s; the window is half-open and would hold nothing",
			since.Format(time.RFC3339), until.Format(time.RFC3339))
	}
	query.Since, query.Until = since, until
	return query, nil
}

func ledgerInstantArg(name, raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is not an RFC3339 instant like 2026-09-05T00:00:00Z", name, raw)
	}
	return at, nil
}

func (d *Daemon) sessionLedgerPage(msg *protocol.SessionListMessage, wantFacets bool) (*protocol.SessionListResult, error) {
	query, err := ledgerQuery(msg, wantFacets)
	if err != nil {
		return nil, err
	}

	page, err := d.store.SessionLedger(query)
	if err != nil {
		var unknownCursor *store.ErrUnknownLedgerCursor
		var tooLarge *store.ErrLedgerLimitTooLarge
		if errors.As(err, &unknownCursor) || errors.As(err, &tooLarge) {
			return nil, err
		}
		d.logf("session ledger read failed: %v", err)
		return nil, errors.New("session ledger unavailable")
	}

	result := &protocol.SessionListResult{Entries: page.Entries, Omitted: page.Omitted, Facets: page.Facets}
	if result.Entries == nil {
		result.Entries = []protocol.SessionLedgerEntry{}
	}
	if page.NextBefore != "" {
		result.NextBefore = protocol.Ptr(page.NextBefore)
	}
	return result, nil
}

func (d *Daemon) handleSessionList(conn net.Conn, msg *protocol.SessionListMessage) {
	result, err := d.sessionLedgerPage(msg, false)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, SessionListResult: result})
}

func (d *Daemon) handleSessionShow(conn net.Conn, msg *protocol.SessionShowMessage) {
	entry := d.store.SessionLedgerEntry(strings.TrimSpace(msg.SessionID))
	if entry == nil {
		d.sendError(conn, "session_not_found")
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                true,
		SessionShowResult: &protocol.SessionShowResult{Entry: *entry},
	})
}

func (d *Daemon) sendSessionListWSResult(client *wsClient, msg *protocol.SessionListMessage) {
	result, err := d.sessionLedgerPage(msg, true)
	reply := protocol.SessionListResultMessage{
		Event:     protocol.EventSessionListResult,
		RequestID: protocol.Deref(msg.RequestID),
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		reply.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, reply)
}

func (d *Daemon) sendSessionShowWSResult(client *wsClient, msg *protocol.SessionShowMessage) {
	reply := protocol.SessionShowResultMessage{
		Event:     protocol.EventSessionShowResult,
		RequestID: protocol.Deref(msg.RequestID),
	}
	if entry := d.store.SessionLedgerEntry(strings.TrimSpace(msg.SessionID)); entry != nil {
		reply.Success = true
		reply.Entry = entry
	} else {
		reply.Error = protocol.Ptr(fmt.Sprintf("this daemon never ran session %s", strings.TrimSpace(msg.SessionID)))
	}
	d.sendToClient(client, reply)
}

// A closed session's ledger row, so an open Sessions surface updates the row in
// place. session_unregistered says the live session left; this says where it went.
func projectSessionClosed(d *Daemon, event bus.Event) {
	entry, ok := decodeFact[protocol.SessionLedgerEntry](d, event)
	if !ok {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:              protocol.EventSessionClosed,
		SessionLedgerEntry: &entry,
	})
}
