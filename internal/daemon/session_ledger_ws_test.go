package daemon

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"strings"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func drainClientPayloads(t *testing.T, client *wsClient) [][]byte {
	t.Helper()
	var payloads [][]byte
	for {
		select {
		case msg, open := <-client.send:
			if !open {
				return payloads
			}
			payloads = append(payloads, msg.payload)
		default:
			return payloads
		}
	}
}

func onlySessionListResult(t *testing.T, client *wsClient) protocol.SessionListResultMessage {
	t.Helper()
	payloads := drainClientPayloads(t, client)
	if len(payloads) != 1 {
		t.Fatalf("client received %d messages, want one session_list_result", len(payloads))
	}
	var reply protocol.SessionListResultMessage
	if err := json.Unmarshal(payloads[0], &reply); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if reply.Event != protocol.EventSessionListResult {
		t.Fatalf("event = %q, want %q", reply.Event, protocol.EventSessionListResult)
	}
	return reply
}

func TestTheWebSocketAnswersSessionListWithAPageAndItsFacets(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	client := newWorkspaceProtocolTestClient()
	addLedgerTestSession(t, d, "live-one", t.TempDir())
	addLedgerTestSession(t, d, "closed-one", t.TempDir())
	d.closeSession("closed-one", store.SessionClose{By: store.SessionClosedByUser})

	d.sendSessionListWSResult(client, &protocol.SessionListMessage{
		Cmd: protocol.CmdSessionList, RequestID: protocol.Ptr("req-1"), All: protocol.Ptr(true),
	})

	reply := onlySessionListResult(t, client)
	if !reply.Success || reply.RequestID != "req-1" {
		t.Fatalf("reply = %+v, want a success correlated to req-1", reply)
	}
	if reply.Result == nil {
		t.Fatal("result = nil, want the page")
	}
	ids := make([]string, 0, len(reply.Result.Entries))
	for _, entry := range reply.Result.Entries {
		ids = append(ids, entry.ID)
	}
	if len(ids) != 2 || !slices.Contains(ids, "live-one") || !slices.Contains(ids, "closed-one") {
		t.Fatalf("entries = %v, want live and closed together", ids)
	}
	// The app draws its filter choices from these; the socket door must carry them.
	if reply.Result.Facets == nil || len(reply.Result.Facets.Workspaces) != 2 {
		t.Errorf("facets = %+v, want a workspace choice per session", reply.Result.Facets)
	}
}

func TestAPagedSessionListLeavesTheFacetsBehind(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	client := newWorkspaceProtocolTestClient()
	for _, id := range []string{"one", "two"} {
		addLedgerTestSession(t, d, id, t.TempDir())
	}

	d.sendSessionListWSResult(client, &protocol.SessionListMessage{
		Cmd: protocol.CmdSessionList, RequestID: protocol.Ptr("req-1"),
		All: protocol.Ptr(true), Limit: protocol.Ptr(1),
	})
	first := onlySessionListResult(t, client)
	if first.Result == nil || first.Result.NextBefore == nil {
		t.Fatalf("first page = %+v, want a cursor onto the older rows", first.Result)
	}

	d.sendSessionListWSResult(client, &protocol.SessionListMessage{
		Cmd: protocol.CmdSessionList, RequestID: protocol.Ptr("req-2"),
		All: protocol.Ptr(true), Limit: protocol.Ptr(1), Before: first.Result.NextBefore,
	})
	second := onlySessionListResult(t, client)
	if second.Result == nil || len(second.Result.Entries) != 1 {
		t.Fatalf("second page = %+v, want the next row", second.Result)
	}
	// The choices belong to the query, not the page: re-sending them would only
	// make the app redraw filters the user is already looking at.
	if second.Result.Facets != nil {
		t.Errorf("facets on a paged read = %+v, want none", second.Result.Facets)
	}
}

func TestSessionListRefusesAWindowThatCouldHoldNothing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	client := newWorkspaceProtocolTestClient()

	d.sendSessionListWSResult(client, &protocol.SessionListMessage{
		Cmd: protocol.CmdSessionList, RequestID: protocol.Ptr("req-1"),
		Since: protocol.Ptr("2026-09-05T00:00:00Z"), Until: protocol.Ptr("2026-09-01T00:00:00Z"),
	})

	reply := onlySessionListResult(t, client)
	if reply.Success || reply.Error == nil {
		t.Fatalf("reply = %+v, want a refusal", reply)
	}
	if got := protocol.Deref(reply.Error); got == "" || !strings.Contains(got, "half-open") {
		t.Errorf("error = %q, want it to explain the window", got)
	}

	d.sendSessionListWSResult(client, &protocol.SessionListMessage{
		Cmd: protocol.CmdSessionList, RequestID: protocol.Ptr("req-2"), Since: protocol.Ptr("yesterday"),
	})
	bad := onlySessionListResult(t, client)
	if bad.Success || !strings.Contains(protocol.Deref(bad.Error), "RFC3339") {
		t.Errorf("reply to a non-instant since = %+v, want it to name the format", bad)
	}
}

func TestSessionShowOverTheWebSocketNamesASessionItNeverRan(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	client := newWorkspaceProtocolTestClient()
	addLedgerTestSession(t, d, "known", t.TempDir())

	d.sendSessionShowWSResult(client, &protocol.SessionShowMessage{
		Cmd: protocol.CmdSessionShow, RequestID: protocol.Ptr("req-1"), SessionID: "known",
	})
	d.sendSessionShowWSResult(client, &protocol.SessionShowMessage{
		Cmd: protocol.CmdSessionShow, RequestID: protocol.Ptr("req-2"), SessionID: "elsewhere",
	})

	payloads := drainClientPayloads(t, client)
	if len(payloads) != 2 {
		t.Fatalf("client received %d messages, want two", len(payloads))
	}
	var found, missing protocol.SessionShowResultMessage
	if err := json.Unmarshal(payloads[0], &found); err != nil {
		t.Fatalf("unmarshal found: %v", err)
	}
	if err := json.Unmarshal(payloads[1], &missing); err != nil {
		t.Fatalf("unmarshal missing: %v", err)
	}
	if !found.Success || found.Entry == nil || found.Entry.ID != "known" {
		t.Errorf("show(known) = %+v, want the ledger row", found)
	}
	if missing.Success || !strings.Contains(protocol.Deref(missing.Error), "elsewhere") {
		t.Errorf("show(elsewhere) = %+v, want a refusal naming the session", missing)
	}
}

func TestTheSessionClosedFactReachesTheAppAsALedgerRow(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	var pushed []*protocol.WebSocketEvent
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) { pushed = append(pushed, event) }
	addLedgerTestSession(t, d, "closing", t.TempDir())
	entry := protocol.SessionLedgerEntry{
		ID: "closing", Label: "closing", Agent: string(protocol.SessionAgentClaude),
		Directory: "/tmp/closing", WorkspaceID: "ws-closing", State: protocol.SessionStateIdle,
		LastSeen: protocol.TimestampNow().String(),
		ClosedAt: protocol.Ptr(protocol.NewTimestamp(time.Now()).String()),
		ClosedBy: protocol.Ptr(store.SessionClosedByUser),
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal fact: %v", err)
	}

	projectSessionClosed(d, bus.Event{Name: FactSessionClosed, Subject: "closing", Payload: payload})

	if len(pushed) != 1 || pushed[0].Event != protocol.EventSessionClosed {
		t.Fatalf("broadcast %d events, want one session_closed", len(pushed))
	}
	if pushed[0].SessionLedgerEntry == nil || pushed[0].SessionLedgerEntry.ID != "closing" {
		t.Errorf("session_closed carried %+v, want the closed session's ledger row", pushed[0].SessionLedgerEntry)
	}
}
