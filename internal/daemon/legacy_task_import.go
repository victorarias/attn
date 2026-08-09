package daemon

import (
	"encoding/json"
	"strings"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

// One-time handover from the retired task runner's `tasks` table to the job
// queue; a no-op on every boot after the first. The legacy meta keys are pinned
// here literally, not shared with the enqueue sites: they describe a format
// nothing writes anymore, so live payload types can change without breaking this.
const (
	legacyMetaTranscript      = "transcript"
	legacyMetaWorkspace       = "workspace"
	legacyMetaDailyPass       = "daily_pass"
	legacyMetaReconcileInputs = "reconcile_inputs"
)

// importLegacyTasks hands the task table's remaining rows to the jobs table.
// Runs before the queue is constructed, so nothing races it; the move is atomic
// in the store (MigrateLegacyTasks), so any failure leaves every old row intact
// for the next start to retry.
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

// legacyTaskToJob is the job one legacy row becomes; it always returns a record,
// because a row it cannot fully read is still owed work. The legacy id is
// PRESERVED as the job id — failure notifications record it as SourceID and the
// panel's Retry deep-links through it. Untranslatable meta yields a payload-less
// job, never a dropped row: its handler reports the missing inputs.
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
// handler now decodes; empty string means "carries nothing", which handlers tolerate.
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
		// Pointer set only when the key was present: "carried empty" (solo bucket)
		// vs "carried nothing" (fall back to the live session row).
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
		// Already the payload's JSON object; carry it across verbatim.
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
