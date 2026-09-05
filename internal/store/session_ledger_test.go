package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
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

	_, reopened, err := s.ReopenSession("s1")
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
	backings := map[string]func(*testing.T) *Store{
		"sqlite": newSessionOwnedTableStore,
		"maps":   func(*testing.T) *Store { return newMapBackedStore() },
	}
	for name, newStore := range backings {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			addSessionInDirectory(t, s, "s1", "/tmp/one")
			if !s.UpdateState("s1", string(protocol.SessionStateWorking)) {
				t.Fatal("UpdateState before the close reported no row")
			}
			if !s.SnoozeTurn("s1", time.Now().Add(time.Hour), time.Now()) {
				t.Fatal("SnoozeTurn before the close reported no row")
			}
			if err := s.SetSessionCostCursor("s1", "cursor-before-the-close"); err != nil {
				t.Fatalf("SetSessionCostCursor before the close: %v", err)
			}
			if !s.BeginAgentDriverRun("s1", "plugin", "run-1") {
				t.Fatal("BeginAgentDriverRun before the close reported no row")
			}

			closeAt(t, s, "s1", SessionClose{By: SessionClosedByUser}, time.Now())
			snapshot := *s.SessionLedgerEntry("s1")
			stampsAtClose := s.TurnStamps("s1")
			costAtClose, err := s.SessionCost("s1")
			if err != nil {
				t.Fatalf("SessionCost at the close: %v", err)
			}

			if s.UpdateState("s1", string(protocol.SessionStateWaitingInput)) {
				t.Error("UpdateState after the close reported a row, want the closed row refused")
			}
			s.Touch("s1")
			s.UpdateSessionLabel("s1", "renamed after the close")
			s.AssignSessionWorkspace("s1", "workspace-elsewhere")
			if s.SettleTurn("s1", time.Now()) {
				t.Error("SettleTurn after the close reported a row, want the closed row refused")
			}
			if s.WakeTurn("s1") {
				t.Error("WakeTurn after the close reported a row, want the closed row refused")
			}
			if err := s.SetSessionCostCursor("s1", "cursor-after-the-close"); err != nil {
				t.Fatalf("SetSessionCostCursor after the close: %v", err)
			}
			if run := s.EndAgentDriverRun("s1"); run.RunID != "" {
				t.Errorf("EndAgentDriverRun after the close = %+v, want the closed row refused", run)
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
			if stamps := s.TurnStamps("s1"); stamps != stampsAtClose {
				t.Errorf("turn stamps = %+v after a late settle and wake, want %+v", stamps, stampsAtClose)
			}
			cost, err := s.SessionCost("s1")
			if err != nil {
				t.Fatalf("SessionCost after the late writes: %v", err)
			}
			if cost.Cursor != costAtClose.Cursor {
				t.Errorf("cost cursor = %q after a late observation, want %q", cost.Cursor, costAtClose.Cursor)
			}
		})
	}
}

func TestALiftedCloseGoesBackExactly(t *testing.T) {
	backings := map[string]func(*testing.T) *Store{
		"sqlite": newSessionOwnedTableStore,
		"maps":   func(*testing.T) *Store { return newMapBackedStore() },
	}
	for name, newStore := range backings {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			addSessionInDirectory(t, s, "s1", "/tmp/one")
			closeAt(t, s, "s1", SessionClose{By: "sess-dispatcher", Reason: "brief delivered"},
				time.Date(2026, 3, 4, 5, 6, 7, 89, time.UTC))
			closed := s.SessionLedgerEntry("s1")
			if closed == nil || protocol.Deref(closed.ClosedAt) == "" {
				t.Fatalf("ledger entry after the close = %+v, want it closed", closed)
			}

			lifted, reopened, err := s.ReopenSession("s1")
			if err != nil || !reopened {
				t.Fatalf("ReopenSession = %v, %v, want the close lifted", reopened, err)
			}
			if s.Get("s1") == nil {
				t.Fatal("Get(s1) = nil after reopening, want the session live again")
			}

			restored, err := s.RestoreSessionClose("s1", lifted)
			if err != nil || !restored {
				t.Fatalf("RestoreSessionClose = %v, %v, want the close back", restored, err)
			}
			if session := s.Get("s1"); session != nil {
				t.Errorf("Get(s1) = %+v after restoring the close, want it hidden again", session)
			}
			if got := s.SessionLedgerEntry("s1"); !reflect.DeepEqual(got, closed) {
				t.Errorf("ledger entry after reopen and restore:\n got=%+v\nwant=%+v", got, closed)
			}
		})
	}
}

func TestClosingDropsTheCostObservationsAndKeepsTheTotals(t *testing.T) {
	backings := map[string]func(*testing.T) *Store{
		"sqlite": newSessionOwnedTableStore,
		"maps":   func(*testing.T) *Store { return newMapBackedStore() },
	}
	for name, newStore := range backings {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			addSessionInDirectory(t, s, "s1", "/tmp/one")
			observations := []SessionCostObservation{
				{ObservationID: "msg-1", Model: "opus", Usage: sessioncost.Usage{InputTokens: 10, OutputTokens: 3}},
				{ObservationID: "msg-2", Model: "opus", Usage: sessioncost.Usage{InputTokens: 5, OutputTokens: 1}},
				{ObservationID: "msg-3", Model: "haiku", Usage: sessioncost.Usage{InputTokens: 7, OutputTokens: 2}},
			}
			if _, err := s.ApplySessionCostObservations("s1", "cursor-1", observations); err != nil {
				t.Fatalf("ApplySessionCostObservations: %v", err)
			}
			before, err := s.SessionCost("s1")
			if err != nil {
				t.Fatalf("SessionCost before the close: %v", err)
			}
			if len(before.Observations) != len(observations) {
				t.Fatalf("observations before the close = %d, want %d", len(before.Observations), len(observations))
			}

			closeAt(t, s, "s1", SessionClose{By: SessionClosedByUser}, time.Now())

			after, err := s.SessionCost("s1")
			if err != nil {
				t.Fatalf("SessionCost after the close: %v", err)
			}
			if len(after.Observations) != 0 {
				t.Errorf("observations after the close = %d, want the map dropped", len(after.Observations))
			}
			if len(after.Finalized) != len(observations) {
				t.Fatalf("finalized ids after the close = %v, want one per observation", after.Finalized)
			}
			for _, observation := range observations {
				if !slices.Contains(after.Finalized, observation.ObservationID) {
					t.Errorf("finalized ids = %v, want %s among them", after.Finalized, observation.ObservationID)
				}
			}
			if len(after.Ledger) != len(before.Ledger) {
				t.Fatalf("ledger after the close = %v, want the per-model totals kept %v", after.Ledger, before.Ledger)
			}
			for model, usage := range before.Ledger {
				if after.Ledger[model] != usage {
					t.Errorf("%s total = %+v after the close, want %+v", model, after.Ledger[model], usage)
				}
			}
			if after.Cursor != before.Cursor {
				t.Errorf("cost cursor = %q after the close, want %q", after.Cursor, before.Cursor)
			}
			if after.Initialized != before.Initialized {
				t.Errorf("initialized = %v after the close, want %v", after.Initialized, before.Initialized)
			}
		})
	}
}

func TestNothingAfterAReopenCanInflateAFinalizedTotal(t *testing.T) {
	backings := map[string]func(*testing.T) *Store{
		"sqlite": newSessionOwnedTableStore,
		"maps":   func(*testing.T) *Store { return newMapBackedStore() },
	}
	for name, newStore := range backings {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			addSessionInDirectory(t, s, "s1", "/tmp/one")
			counted := SessionCostObservation{
				ObservationID: "claude:msg-1", Model: "opus",
				Usage: sessioncost.Usage{InputTokens: 10, OutputTokens: 3},
			}
			if _, err := s.ApplySessionCostObservations("s1", "cursor-1", []SessionCostObservation{counted}); err != nil {
				t.Fatalf("first apply: %v", err)
			}
			closeAt(t, s, "s1", SessionClose{By: SessionClosedByUser}, time.Now())
			finalized, err := s.SessionCost("s1")
			if err != nil {
				t.Fatalf("SessionCost at the close: %v", err)
			}

			if _, reopened, err := s.ReopenSession("s1"); err != nil || !reopened {
				t.Fatalf("reopen = %v, %v", reopened, err)
			}

			replay := counted
			revision := counted
			revision.Usage.InputTokens = 12
			for _, late := range []struct {
				name  string
				batch SessionCostObservation
			}{
				{"an exact replay", replay},
				{"a later revision of the same message", revision},
			} {
				changed, err := s.ApplySessionCostObservations("s1", "cursor-2", []SessionCostObservation{late.batch})
				if err != nil {
					t.Fatalf("%s: %v", late.name, err)
				}
				if changed {
					t.Errorf("%s reported a change, want a finalized observation refused", late.name)
				}
				after, err := s.SessionCost("s1")
				if err != nil {
					t.Fatalf("SessionCost after %s: %v", late.name, err)
				}
				if after.Ledger["opus"] != finalized.Ledger["opus"] {
					t.Errorf("opus total = %+v after %s, want the finalized %+v",
						after.Ledger["opus"], late.name, finalized.Ledger["opus"])
				}
			}

			fresh := SessionCostObservation{
				ObservationID: "claude:msg-2", Model: "opus",
				Usage: sessioncost.Usage{InputTokens: 4, OutputTokens: 1},
			}
			if changed, err := s.ApplySessionCostObservations("s1", "cursor-3", []SessionCostObservation{fresh}); err != nil || !changed {
				t.Fatalf("work after reopening changed=%v err=%v, want it counted", changed, err)
			}
			after, err := s.SessionCost("s1")
			if err != nil {
				t.Fatalf("SessionCost after the resumed work: %v", err)
			}
			want := finalized.Ledger["opus"].Add(fresh.Usage)
			if after.Ledger["opus"] != want {
				t.Errorf("opus total = %+v after resuming, want %+v", after.Ledger["opus"], want)
			}
		})
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
