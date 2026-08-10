package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newAgentMessageStore(t *testing.T) *Store {
	t.Helper()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func enqueue(t *testing.T, s *Store, id, sender, target, content string, createdAt time.Time) {
	t.Helper()
	if err := s.EnqueueAgentMessage(AgentMessage{
		ID:              id,
		SenderSessionID: sender,
		TargetSessionID: target,
		Content:         content,
		CreatedAt:       createdAt.UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("EnqueueAgentMessage(%s) error = %v", id, err)
	}
}

// A message is queued the moment it is accepted and stops being queued only
// when a delivery is stamped — that pair is what makes "never a silent drop"
// survive a restart.
func TestAgentMessagesQueueUntilDeliveryIsStamped(t *testing.T) {
	s := newAgentMessageStore(t)
	now := time.Now()
	enqueue(t, s, "second", "sender", "target", "later", now)
	enqueue(t, s, "first", "sender", "target", "earlier", now.Add(-time.Minute))
	enqueue(t, s, "other-target", "sender", "elsewhere", "not yours", now)

	queued, err := s.UndeliveredAgentMessages("target")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 2 || queued[0].ID != "first" || queued[1].ID != "second" {
		t.Fatalf("queue is not oldest-first: %+v", queued)
	}
	if queued[0].Content != "earlier" || queued[0].SenderSessionID != "sender" {
		t.Fatalf("row = %+v", queued[0])
	}

	targets, err := s.TargetsWithQueuedAgentMessages()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %v, want both queued targets", targets)
	}

	if err := s.MarkAgentMessageDelivered("first", now); err != nil {
		t.Fatal(err)
	}
	queued, err = s.UndeliveredAgentMessages("target")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].ID != "second" {
		t.Fatalf("after delivery, queue = %+v", queued)
	}
}

// Re-stamping must not move a delivery time: a racing drain that delivered
// nothing would otherwise rewrite the receipt of a message it never sent.
func TestMarkAgentMessageDeliveredIsWriteOnce(t *testing.T) {
	s := newAgentMessageStore(t)
	first := time.Now().Add(-time.Hour)
	enqueue(t, s, "once", "sender", "target", "hello", first)
	if err := s.MarkAgentMessageDelivered("once", first); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAgentMessageDelivered("once", time.Now()); err != nil {
		t.Fatal(err)
	}

	var deliveredAt string
	if err := s.db.QueryRow(`SELECT delivered_at FROM agent_messages WHERE id = 'once'`).Scan(&deliveredAt); err != nil {
		t.Fatal(err)
	}
	if deliveredAt != first.UTC().Format(time.RFC3339) {
		t.Fatalf("delivered_at = %q, want the first stamp", deliveredAt)
	}
}

// The guard's three counts, each scoped to exactly what its limit is about:
// dedupe to one sender's identical text, the rate to one sender's traffic, and
// the queue cap to the target's whole backlog from everyone.
func TestAgentMessageGuardCountsScopeEachLimit(t *testing.T) {
	s := newAgentMessageStore(t)
	now := time.Now()
	enqueue(t, s, "recent-dupe", "sender", "target", "same words", now.Add(-2*time.Second))
	enqueue(t, s, "old-dupe", "sender", "target", "stale words", now.Add(-time.Hour))
	enqueue(t, s, "other-sender", "someone-else", "target", "same words", now.Add(-time.Second))

	counts, err := s.AgentMessageGuardCounts("sender", "target", "same words", now.Add(-10*time.Second), now.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !counts.DuplicateFromSender {
		t.Fatal("identical recent text from the same sender was not seen as a duplicate")
	}
	if counts.FromSenderInWindow != 1 {
		t.Fatalf("rate count = %d; only the in-window message from this sender counts", counts.FromSenderInWindow)
	}
	if counts.UndeliveredForTarget != 3 {
		t.Fatalf("backlog = %d; the queue cap counts every sender", counts.UndeliveredForTarget)
	}

	// The same text outside the dedupe window is a new message, not a repeat.
	stale, err := s.AgentMessageGuardCounts("sender", "target", "stale words", now.Add(-10*time.Second), now.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if stale.DuplicateFromSender {
		t.Fatal("text older than the dedupe window still read as a duplicate")
	}
}

// The drop is safe only because nothing ever wrote that table. This plants the
// pre-101 world — the dispatch message table present, agent_messages absent —
// and shows the migration replaces one with the other on a real database.
func TestMigration101ReplacesTheDormantDispatchTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	if _, err := s.db.Exec(`
		DROP TABLE agent_messages;
		CREATE TABLE chief_of_staff_dispatch_messages (
			id TEXT PRIMARY KEY,
			dispatch_id TEXT NOT NULL,
			sender_session_id TEXT NOT NULL,
			target_session_id TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TEXT NOT NULL,
			read_at TEXT NOT NULL DEFAULT '',
			acknowledged_at TEXT NOT NULL DEFAULT '',
			acknowledgement TEXT NOT NULL DEFAULT ''
		);
		DELETE FROM schema_migrations WHERE version >= 101;
	`); err != nil {
		t.Fatalf("plant the pre-101 schema: %v", err)
	}
	// Sanity: without this the test would pass against a database that never
	// had the old table at all.
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM chief_of_staff_dispatch_messages`).Scan(new(int)); err != nil {
		t.Fatalf("the planted schema lacks the table this migration drops: %v", err)
	}

	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM chief_of_staff_dispatch_messages`).Scan(new(int)); err == nil {
		t.Fatal("the dormant dispatch message table survived migration 101")
	}
	enqueue(t, s, "after-migration", "sender", "target", "hello", time.Now())
	queued, err := s.UndeliveredAgentMessages("target")
	if err != nil || len(queued) != 1 {
		t.Fatalf("agent_messages after migration: %+v, %v", queued, err)
	}
}
