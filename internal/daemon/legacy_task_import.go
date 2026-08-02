package daemon

import (
	"encoding/json"
	"strings"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

// One-time handover from the retired durable task runner (internal/tasks) to the
// job queue. The task runner stored its records in the `tasks` table with a
// `kind:subject` id and a flat map[string]string of inputs; the queue stores a
// uuid id, a coalescing key, and a typed JSON payload. This carries anything the
// old runner still owed onto the new table and empties the old one, so the whole
// path is a no-op on every boot after the first.
//
// Legacy meta keys are written out literally here rather than shared with the
// enqueue sites. They describe a format nothing writes anymore, so pinning them
// to this file is what lets the live payload types change without silently
// breaking the import.
const (
	legacyMetaTranscript      = "transcript"
	legacyMetaWorkspace       = "workspace"
	legacyMetaDailyPass       = "daily_pass"
	legacyMetaReconcileInputs = "reconcile_inputs"
)

// importLegacyTasks hands the task table's remaining rows to the jobs table. It
// runs before the queue is constructed, so nothing races it.
//
// The move is atomic in the store (see MigrateLegacyTasks): a failure anywhere
// leaves every old row where it was, and the next start tries again. That
// matters more here than anywhere else in the queue — this path runs exactly
// once per installation, and the work it carries has no other copy.
func (d *Daemon) importLegacyTasks() {
	if d.store == nil {
		return
	}
	imported, err := d.store.MigrateLegacyTasks(d.legacyTaskToJob)
	if err != nil {
		d.logf("jobs: hand over the retired task runner's records: %v "+
			"— nothing was moved and the old rows are intact; the next daemon start retries it", err)
		return
	}
	if imported > 0 {
		d.logf("jobs: imported %d task record(s) from the retired task runner", imported)
	}
}

// legacyTaskToJob is the job one legacy row becomes. It runs inside the
// handover's transaction and always returns a record, because a row it cannot
// fully read is still owed work.
//
// The legacy id is PRESERVED as the job id. A dead task's failure notification
// records that id as its SourceID, and the panel's Retry deep-links through it —
// minting a fresh uuid would leave every pre-upgrade failure notification
// pointing at nothing.
//
// A row whose meta cannot be translated becomes a job without a payload rather
// than being dropped: the record stays visible in the panel and its handler
// reports the missing inputs, which is a diagnosable outcome. Silently
// discarding owed work is not.
func (d *Daemon) legacyTaskToJob(rec store.LegacyTaskRecord) store.JobRecord {
	payload, err := legacyTaskPayload(rec)
	if err != nil {
		d.logf("jobs: import legacy task %s (%s): %v", rec.ID, rec.Kind, err)
	}
	job := store.JobRecord{
		ID:          rec.ID,
		Kind:        rec.Kind,
		UniqueKey:   rec.Subject,
		Payload:     payload,
		State:       rec.State,
		Attempts:    rec.Attempts,
		ScheduledAt: rec.NextAttemptAt,
		LastError:   rec.LastError,
		Requeued:    rec.Requeued,
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
	}
	if job.State == "" {
		job.State = string(jobs.StateQueued)
	}
	return job
}

// legacyTaskPayload translates one legacy meta blob into the payload its kind's
// handler now decodes. An empty string means "carries nothing", which every
// handler already tolerates. Kinds with no meta (compact_context) and rows that
// stored none return that empty payload with no error.
func legacyTaskPayload(rec store.LegacyTaskRecord) (string, error) {
	raw := strings.TrimSpace(rec.MetaJSON)
	if raw == "" || raw == "null" {
		return "", nil
	}
	var meta map[string]string
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return "", err
	}
	var payload any
	switch rec.Kind {
	case notebookSummarizeSessionKind:
		p := summarizeSessionPayload{Transcript: meta[legacyMetaTranscript]}
		// The pointer distinguishes "carried, and it is empty" (a solo session,
		// bound for the _solo bucket) from "carried nothing" (fall back to the live
		// session row), so it is set only when the key was actually present.
		if ws, ok := meta[legacyMetaWorkspace]; ok {
			p.WorkspaceID = &ws
		}
		payload = p
	case notebookNarrateWorkspaceKind:
		if meta[legacyMetaDailyPass] != "1" {
			return "", nil
		}
		payload = narrateWorkspacePayload{DailyPass: true}
	case reconcileKind:
		// The reconcile inputs were already a JSON object nested inside the meta
		// map, and the payload is that same object — carry it across verbatim
		// rather than decoding and re-encoding it.
		return meta[legacyMetaReconcileInputs], nil
	default:
		return "", nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
