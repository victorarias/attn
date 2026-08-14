package daemon

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

func addCostSession(t *testing.T, d *Daemon, id string, agent protocol.SessionAgent) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Agent: agent, Label: id, Directory: t.TempDir(),
		State: protocol.StateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func TestSessionCostWireStates(t *testing.T) {
	d := newTurnDaemon(t)
	addCostSession(t, d, "cost", protocol.SessionAgentClaude)

	if got := d.sessionForBroadcast(d.store.Get("cost")); got.CostUsd != nil || got.CostUnknown != nil {
		t.Fatalf("unused session has cost fields: %+v", got)
	}
	usage := sessioncost.Usage{
		InputTokens: 4, OutputTokens: 3546, CacheReadInputTokens: 267672,
		CacheWrite5mInputTokens: 7391, CacheWrite1hInputTokens: 4111,
	}
	if _, err := d.store.ApplySessionCostObservations("cost", "cursor", []store.SessionCostObservation{{
		ObservationID: "captured", Model: "claude-opus-5", Usage: usage,
	}}); err != nil {
		t.Fatal(err)
	}
	got := d.sessionForBroadcast(d.store.Get("cost"))
	if got.CostUnknown != nil || got.CostUsd == nil || math.Abs(*got.CostUsd-0.30980975) > 1e-12 {
		t.Fatalf("priced wire session = %+v", got)
	}

	if _, err := d.store.ApplySessionCostObservations("cost", "cursor-2", []store.SessionCostObservation{{
		ObservationID: "unknown", Model: "future-model", Usage: sessioncost.Usage{InputTokens: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	got = d.sessionForBroadcast(d.store.Get("cost"))
	if got.CostUsd != nil || !protocol.Deref(got.CostUnknown) {
		t.Fatalf("unpriced wire session = %+v", got)
	}
}

func TestSessionCostWireMarksUnreadableDurableStateUnknown(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	persistent, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	d := newTurnDaemon(t)
	_ = d.store.Close()
	d.store = persistent
	t.Cleanup(func() { _ = persistent.Close() })
	addCostSession(t, d, "corrupt", protocol.SessionAgentClaude)

	direct, err := store.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := direct.Exec(
		"UPDATE sessions SET session_cost_json = ? WHERE id = ?",
		`{"ledger":`, "corrupt",
	); err != nil {
		_ = direct.Close()
		t.Fatal(err)
	}
	if err := direct.Close(); err != nil {
		t.Fatal(err)
	}

	got := d.store.Get("corrupt")
	d.decorateSessionWithCost(got)
	if got.CostUsd != nil || !protocol.Deref(got.CostUnknown) {
		t.Fatalf("session with unreadable cost state = %+v", got)
	}
}

func TestSessionCostForwardOnlyThenAccumulatesCapturedUsage(t *testing.T) {
	d := newTurnDaemon(t)
	addCostSession(t, d, "cost", protocol.SessionAgentClaude)

	fixture, err := os.ReadFile(filepath.Join("..", "transcript", "testdata", "usage", "claude-2.1.233.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	w := &transcriptWatcher{sessionID: "cost", agent: protocol.SessionAgentClaude}
	follower, err := d.newSessionCostFollower(w, path)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Records) != 0 {
		t.Fatalf("initial attach backfilled %d existing records", len(batch.Records))
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString(`{"type":"assistant","message":{"id":"future","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":2}}}` + "\n")
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append usage: write=%v close=%v", writeErr, closeErr)
	}
	batch, err = follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.applySessionCostBatch(w, follower, batch); err != nil {
		t.Fatal(err)
	}
	state, err := d.store.SessionCost("cost")
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Ledger["claude-opus-5"]; got.InputTokens != 1 || got.OutputTokens != 2 {
		t.Fatalf("forward usage = %+v", got)
	}
}

func TestSessionCostStartsAtZeroForNewSessionAndMarksUnsupportedHarnessUnknown(t *testing.T) {
	d := newTurnDaemon(t)
	addCostSession(t, d, "new", protocol.SessionAgentClaude)
	if err := d.store.InitializeSessionCostTracking("new"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "transcript", "testdata", "usage", "claude-2.1.233.jsonl")
	w := &transcriptWatcher{sessionID: "new", agent: protocol.SessionAgentClaude}
	follower, err := d.newSessionCostFollower(w, path)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.applySessionCostBatch(w, follower, batch); err != nil {
		t.Fatal(err)
	}
	got := d.sessionForBroadcast(d.store.Get("new"))
	if got.CostUsd == nil || *got.CostUsd <= 0 || got.CostUnknown != nil {
		t.Fatalf("captured Claude usage did not produce a priced cost: %+v", got)
	}

	addCostSession(t, d, "copilot", protocol.SessionAgentCopilot)
	if err := d.store.InitializeSessionCostTracking("copilot"); err != nil {
		t.Fatal(err)
	}
	copilotPath := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(copilotPath, []byte(`{"type":"assistant.message","data":{"content":"done"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	copilotWatcher := &transcriptWatcher{sessionID: "copilot", agent: protocol.SessionAgentCopilot}
	copilotFollower, err := transcript.NewFollower(copilotPath, "copilot", 0)
	if err != nil {
		t.Fatal(err)
	}
	copilotBatch, err := copilotFollower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.applySessionCostBatch(copilotWatcher, copilotFollower, copilotBatch); err != nil {
		t.Fatal(err)
	}
	got = d.sessionForBroadcast(d.store.Get("copilot"))
	if got.CostUsd != nil || !protocol.Deref(got.CostUnknown) {
		t.Fatalf("usage-less transcript harness did not show unknown: %+v", got)
	}
}

func TestSessionCostSeedsRewrittenCodexTranscriptAtHead(t *testing.T) {
	d := newTurnDaemon(t)
	addCostSession(t, d, "codex", protocol.SessionAgentCodex)
	if err := d.store.InitializeSessionCostTracking("codex"); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "transcript", "testdata", "usage", "codex-0.147.0.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	w := &transcriptWatcher{sessionID: "codex", agent: protocol.SessionAgentCodex}
	follower, err := d.newSessionCostFollower(w, path)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.applySessionCostBatch(w, follower, batch); err != nil {
		t.Fatal(err)
	}
	before, err := d.store.SessionCost("codex")
	if err != nil {
		t.Fatal(err)
	}
	beforeUsage := before.Ledger["gpt-5.5"]

	// Change the first record and every later byte offset while retaining the
	// already-accounted token records. Cursor recovery must not replay them.
	rewritten := append([]byte(`{"timestamp":"2026-06-13T18:12:39.000Z","type":"session_meta","payload":{"id":"replacement"}}`+"\n"), fixture...)
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	follower, err = d.newSessionCostFollower(w, path)
	if err != nil {
		t.Fatal(err)
	}
	batch, err = follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Records) != 0 {
		t.Fatalf("rewritten transcript replayed %d historical records", len(batch.Records))
	}
	afterRewrite, _ := d.store.SessionCost("codex")
	if got := afterRewrite.Ledger["gpt-5.5"]; got != beforeUsage {
		t.Fatalf("rewritten transcript changed usage from %+v to %+v", beforeUsage, got)
	}

	future := []byte(
		`{"timestamp":"2026-06-13T18:14:00.000Z","type":"turn_context","payload":{"model":"gpt-5.5"}}` + "\n" +
			`{"timestamp":"2026-06-13T18:14:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":2}}}}` + "\n",
	)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := bytes.NewReader(future).WriteTo(f)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append future usage: write=%v close=%v", writeErr, closeErr)
	}
	batch, err = follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.applySessionCostBatch(w, follower, batch); err != nil {
		t.Fatal(err)
	}
	afterFuture, _ := d.store.SessionCost("codex")
	got := afterFuture.Ledger["gpt-5.5"]
	if got.InputTokens != beforeUsage.InputTokens+6 ||
		got.CacheReadInputTokens != beforeUsage.CacheReadInputTokens+4 ||
		got.OutputTokens != beforeUsage.OutputTokens+2 {
		t.Fatalf("usage after rewritten transcript future record = %+v, before %+v", got, beforeUsage)
	}
}
