package daemon

import (
	"encoding/json"
	"errors"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) handleSessionList(conn net.Conn, msg *protocol.SessionListMessage) {
	scope := store.SessionLedgerLive
	switch {
	case protocol.Deref(msg.All):
		scope = store.SessionLedgerAll
	case protocol.Deref(msg.Closed):
		scope = store.SessionLedgerClosed
	}

	page, err := d.store.SessionLedger(store.SessionLedgerQuery{
		Scope:  scope,
		Limit:  protocol.Deref(msg.Limit),
		Before: strings.TrimSpace(protocol.Deref(msg.Before)),
	})
	if err != nil {
		var unknownCursor *store.ErrUnknownLedgerCursor
		var tooLarge *store.ErrLedgerLimitTooLarge
		if errors.As(err, &unknownCursor) || errors.As(err, &tooLarge) {
			d.sendError(conn, err.Error())
			return
		}
		d.logf("session ledger read failed: %v", err)
		d.sendError(conn, "session ledger unavailable")
		return
	}

	result := &protocol.SessionListResult{Entries: page.Entries, Omitted: page.Omitted}
	if result.Entries == nil {
		result.Entries = []protocol.SessionLedgerEntry{}
	}
	if page.NextBefore != "" {
		result.NextBefore = protocol.Ptr(page.NextBefore)
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
