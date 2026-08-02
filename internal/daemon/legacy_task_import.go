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

// importLegacyTasks drains the task table and writes each remaining row to the
// jobs table. It runs before the queue is constructed, so nothing races it.
//
// The legacy id is PRESERVED as the job id. A dead task's failure notification
// records that id as its SourceID, and the panel's Retry deep-links through it —
// minting a fresh uuid would leave every pre-upgrade failure notification
// pointing at nothing.
//
// A row whose meta cannot be translated is imported without a payload rather
// than dropped: the record stays visible in the panel and its handler reports
// the missing inputs, which is a diagnosable outcome. Silently discarding owed
// work is not.
func (d *Daemon) importLegacyTasks() {
	if d.store == nil {
		return
	}
	legacy, err := d.store.DrainLegacyTasks()
	if err != nil {
		d.logf("jobs: drain legacy tasks: %v", err)
		return
	}
	d.importDrainedTasks(legacy)
}

// importDrainedTasks writes the drained rows to the jobs table. It is split from
// the drain so the translation — which is the part that can silently lose a
// kind's inputs — is exercised without a fixture in the retired table.
func (d *Daemon) importDrainedTasks(legacy []store.LegacyTaskRecord) {
	if len(legacy) == 0 {
		return
	}
	imported := 0
	for _, rec := range legacy {
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
		if err := d.store.UpsertJob(job); err != nil {
			d.logf("jobs: import legacy task %s: %v", rec.ID, err)
			continue
		}
		imported++
	}
	d.logf("jobs: imported %d task record(s) from the retired task runner", imported)
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
