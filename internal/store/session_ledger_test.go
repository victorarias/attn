package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func closeAt(t *testing.T, s *Store, id string, closed SessionClose, at time.Time) {
	t.Helper()
	recorded, err := s.CloseSession(id, closed, at)
	if err != nil {
		t.Fatalf("close %s: %v", id, err)
	}
	if !recorded {
		t.Fatalf("close %s recorded nothing, want the row marked closed", id)
	}
}

func TestCloseKeepsTheRowAndEverySessionOwnedTable(t *testing.T) {
	for _, table := range sessionOwnedTables {
		seed := sessionOwnedTableSeeds[table]
		if seed == nil {
			t.Fatalf("%s is session-owned but this test cannot seed it; add a seeder so closing "+
				"stays covered alongside removal", table)
		}
		t.Run(table, func(t *testing.T) {
			s := newSessionOwnedTableStore(t)
			addSessionInDirectory(t, s, "s1", "/tmp/one")
			seed(t, s, "s1")

			closeAt(t, s, "s1", SessionClose{By: SessionClosedByUser}, time.Now())

			if rows := countRowsFor(t, s, table, "s1"); rows != 1 {
				t.Errorf("%s rows for s1 after closing = %d, want the close to keep them", table, rows)
			}
			if entry := s.SessionLedgerEntry("s1"); entry == nil {
				t.Fatal("SessionLedgerEntry(s1) = nil, want the closed row")
			}
		})
	}
}

func TestAClosedSessionIsInvisibleToEveryLiveReader(t *testing.T) {
	s := newSessionOwnedTableStore(t)
	addSessionInDirectory(t, s, "live", "/tmp/live")
	addSessionInDirectory(t, s, "gone", "/tmp/gone")

	closeAt(t, s, "gone", SessionClose{By: SessionClosedByUser}, time.Now())

	if got := s.Get("gone"); got != nil {
		t.Errorf("Get(gone) = %+v, want nil: Get answers about live sessions", got)
	}
	if got := s.Get("live"); got == nil {
		t.Error("Get(live) = nil, want the untouched session")
	}
	for _, filter := range []string{"", string(protocol.SessionStateIdle)} {
		for _, session := range s.List(filter) {
			if session.ID == "gone" {
				t.Errorf("List(%q) returned the closed session", filter)
			}
		}
	}
	if s.HasSessionInDirectory("/tmp/gone") {
		t.Error("HasSessionInDirectory(/tmp/gone) = true, want the closed session to free its directory")
	}
	if ids := s.SessionsInWorkspace(s.SessionLedgerEntry("gone").WorkspaceID); slices.Contains(ids, "gone") {
		t.Errorf("SessionsInWorkspace = %v, want a closed session to leave its workspace", ids)
	}
}

func TestClosingRefusesTwiceAndReopenBringsTheRowBack(t *testing.T) {
	s := newSessionOwnedTableStore(t)
	addSessionInDirectory(t, s, "s1", "/tmp/one")
	closeAt(t, s, "s1", SessionClose{By: "sess-closer", Reason: "work finished"}, time.Now())

	again, err := s.CloseSession("s1", SessionClose{By: SessionClosedByUser}, time.Now())
	if err != nil {
		t.Fatalf("second close: %v", err)
	}
	if again {
		t.Error("closing an already closed session recorded a second close")
	}

	entry := s.SessionLedgerEntry("s1")
	if entry == nil {
		t.Fatal("SessionLedgerEntry(s1) = nil")
	}
	if by := protocol.Deref(entry.ClosedBy); by != "sess-closer" {
		t.Errorf("closed_by = %q, want the first closer kept", by)
	}
	if reason := protocol.Deref(entry.CloseReason); reason != "work finished" {
		t.Errorf("close_reason = %q, want %q", reason, "work finished")
	}

	if err := s.AddChecked(&protocol.Session{
		ID: "s1", Label: "s1", Directory: "/tmp/one", State: protocol.SessionStateIdle,
		StateSince: protocol.TimestampNow().String(), LastSeen: protocol.TimestampNow().String(),
	}); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("AddChecked over a closed row = %v, want ErrSessionClosed", err)
	}

	reopened, err := s.ReopenSession("s1")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !reopened {
		t.Fatal("ReopenSession(s1) = false, want the close cleared")
	}
	if got := s.Get("s1"); got == nil {
		t.Fatal("Get(s1) = nil after reopening")
	}
	if entry := s.SessionLedgerEntry("s1"); protocol.Deref(entry.ClosedAt) != "" {
		t.Errorf("closed_at = %q after reopening, want it cleared", protocol.Deref(entry.ClosedAt))
	}
}

func TestSessionLedgerPagesNewestFirstAndCountsWhatItOmitted(t *testing.T) {
	s := newSessionOwnedTableStore(t)
	base := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		addSessionInDirectory(t, s, id, "/tmp/"+id)
	}
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		closeAt(t, s, id, SessionClose{By: SessionClosedByUser}, base.Add(time.Duration(i)*time.Minute))
	}

	first, err := s.SessionLedger(SessionLedgerQuery{Scope: SessionLedgerClosed, Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if got := ledgerIDs(first); len(got) != 2 || got[0] != "e" || got[1] != "d" {
		t.Fatalf("first page = %v, want [e d]", got)
	}
	if first.Omitted != 3 {
		t.Errorf("omitted = %d, want 3", first.Omitted)
	}
	if first.NextBefore != "d" {
		t.Errorf("next before = %q, want the last row of the page", first.NextBefore)
	}

	second, err := s.SessionLedger(SessionLedgerQuery{Scope: SessionLedgerClosed, Limit: 2, Before: first.NextBefore})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if got := ledgerIDs(second); len(got) != 2 || got[0] != "c" || got[1] != "b" {
		t.Fatalf("second page = %v, want [c b]", got)
	}
	if second.Omitted != 1 {
		t.Errorf("second page omitted = %d, want 1", second.Omitted)
	}

	last, err := s.SessionLedger(SessionLedgerQuery{Scope: SessionLedgerClosed, Limit: 2, Before: second.NextBefore})
	if err != nil {
		t.Fatalf("last page: %v", err)
	}
	if got := ledgerIDs(last); len(got) != 1 || got[0] != "a" {
		t.Fatalf("last page = %v, want [a]", got)
	}
	if last.Omitted != 0 || last.NextBefore != "" {
		t.Errorf("last page omitted %d with cursor %q, want nothing left", last.Omitted, last.NextBefore)
	}
}

func TestSessionLedgerScopesSeparateLiveFromClosed(t *testing.T) {
	s := newSessionOwnedTableStore(t)
	addSessionInDirectory(t, s, "live", "/tmp/live")
	addSessionInDirectory(t, s, "gone", "/tmp/gone")
	closeAt(t, s, "gone", SessionClose{By: SessionClosedByUser}, time.Now())

	cases := map[SessionLedgerScope][]string{
		SessionLedgerLive:   {"live"},
		SessionLedgerClosed: {"gone"},
		SessionLedgerAll:    {"gone", "live"},
	}
	for scope, want := range cases {
		page, err := s.SessionLedger(SessionLedgerQuery{Scope: scope})
		if err != nil {
			t.Fatalf("%s page: %v", scope, err)
		}
		got := ledgerIDs(page)
		if len(got) != len(want) {
			t.Fatalf("%s page = %v, want %v", scope, got, want)
		}
		for _, id := range want {
			if !slices.Contains(got, id) {
				t.Errorf("%s page = %v, want it to hold %s", scope, got, id)
			}
		}
	}
}

func TestSessionLedgerNamesTheLimitAndTheStaleCursor(t *testing.T) {
	s := newSessionOwnedTableStore(t)

	_, err := s.SessionLedger(SessionLedgerQuery{Limit: SessionLedgerMaxLimit + 1})
	var tooLarge *ErrLedgerLimitTooLarge
	if !errors.As(err, &tooLarge) {
		t.Fatalf("over-limit query error = %v, want ErrLedgerLimitTooLarge", err)
	}
	if tooLarge.Asked != SessionLedgerMaxLimit+1 || tooLarge.Max != SessionLedgerMaxLimit {
		t.Errorf("limit error = %+v, want it to name both the ask and the limit", tooLarge)
	}

	_, err = s.SessionLedger(SessionLedgerQuery{Before: "never-existed"})
	var unknown *ErrUnknownLedgerCursor
	if !errors.As(err, &unknown) {
		t.Fatalf("stale cursor error = %v, want ErrUnknownLedgerCursor", err)
	}
	if unknown.ID != "never-existed" {
		t.Errorf("cursor error names %q, want the id that was asked for", unknown.ID)
	}
}

func ledgerIDs(page SessionLedgerPage) []string {
	ids := make([]string, 0, len(page.Entries))
	for _, entry := range page.Entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func TestALateWriteCannotRewriteAClosedSession(t *testing.T) {
	s := newSessionOwnedTableStore(t)
	addSessionInDirectory(t, s, "s1", "/tmp/one")
	if !s.UpdateState("s1", string(protocol.SessionStateWorking)) {
		t.Fatal("UpdateState before the close reported no row")
	}

	closeAt(t, s, "s1", SessionClose{By: SessionClosedByUser}, time.Now())
	snapshot := *s.SessionLedgerEntry("s1")

	if s.UpdateState("s1", string(protocol.SessionStateWaitingInput)) {
		t.Error("UpdateState after the close reported a row, want the closed row refused")
	}
	s.Touch("s1")
	s.UpdateSessionLabel("s1", "renamed after the close")
	s.AssignSessionWorkspace("s1", "workspace-elsewhere")
	if s.SettleTurn("s1", time.Now()) {
		t.Error("SettleTurn after the close reported a row, want the closed row refused")
	}

	after := s.SessionLedgerEntry("s1")
	if after == nil {
		t.Fatal("SessionLedgerEntry(s1) = nil after the late writes")
	}
	if after.State != snapshot.State {
		t.Errorf("state = %q after a late write, want the closed snapshot %q", after.State, snapshot.State)
	}
	if after.LastSeen != snapshot.LastSeen {
		t.Errorf("last_seen = %q after a late touch, want %q", after.LastSeen, snapshot.LastSeen)
	}
	if after.Label != snapshot.Label {
		t.Errorf("label = %q after a late rename, want %q", after.Label, snapshot.Label)
	}
	if after.WorkspaceID != snapshot.WorkspaceID {
		t.Errorf("workspace = %q after a late assignment, want %q", after.WorkspaceID, snapshot.WorkspaceID)
	}
}

func TestEverySessionWriterCarriesTheClosedRowPredicate(t *testing.T) {
	exempt := map[string]string{
		"sqlite.go":         "migrations run over rows that predate the column",
		"session_ledger.go": "closing and reopening are the writers of closed_at",
	}
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list package sources: %v", err)
	}
	for _, name := range sources {
		if strings.HasSuffix(name, "_test.go") || exempt[name] != "" {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(body)
		for offset := 0; ; {
			start := strings.Index(text[offset:], "UPDATE sessions")
			if start < 0 {
				break
			}
			start += offset
			statement := text[start:min(start+400, len(text))]
			if !strings.Contains(statement, "closed_at = ''") {
				t.Errorf("%s:%d writes sessions without AND closed_at = '': a closed row is the ledger's "+
					"snapshot, so a late write must find no row", name, strings.Count(text[:start], "\n")+1)
			}
			offset = start + 1
		}
	}
}
