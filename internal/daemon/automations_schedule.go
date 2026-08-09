package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

// scheduleDueInstantCap bounds DueInstants' backlog walk so a far-behind cursor
// never fires an unbounded catch-up burst. A var so tests can shrink it.
var scheduleDueInstantCap = 1_000_000

// scheduleSkipGrace is how long after an intended instant a "skip" catch-up
// policy will still fire it; past this the instant is never delivered.
const scheduleSkipGrace = 5 * time.Minute

// automationScheduleKind is the queue kind for the observation tick.
const automationScheduleKind = "automation_schedule"

// automationScheduleInterval matches the minutely-grade schedule floor.
const automationScheduleInterval = time.Minute

// automationScheduleTickTimeout is a tripwire: far past any healthy pass, so
// only a wedged one frees the slot instead of stalling the kind.
const automationScheduleTickTimeout = 2 * time.Minute

// automationScheduleHandler is the queue handler for the observation tick. It
// never errors: the decision is cursor-driven, so the next tick re-decides.
func (d *Daemon) automationScheduleHandler(_ context.Context, _ *jobs.Job) (any, error) {
	d.observeDueSchedules(time.Now())
	return nil, nil
}

// observeDueSchedules fans out over enabled scheduled definitions, claiming at
// most one due occurrence per definition per tick.
func (d *Daemon) observeDueSchedules(now time.Time) {
	if d.isRecovering() {
		// Racing ahead of startup recovery could double-claim or misjudge cursors.
		return
	}
	definitions, err := d.store.ListAutomationDefinitions()
	if err != nil {
		d.logf("automation schedule observation list definitions: %v", err)
		return
	}
	for i := range definitions {
		definition := definitions[i]
		if !definition.Enabled {
			continue
		}
		var spec automation.DefinitionSpec
		if err := json.Unmarshal([]byte(definition.SpecJSON), &spec); err != nil {
			d.logf("automation schedule observation parse %s: %v", definition.ID, err)
			continue
		}
		if spec.Trigger.Type != "scheduled" {
			continue
		}
		d.observeDueSchedule(definition, spec, now)
	}
}

// observeDueSchedule decides and, if due, claims and delivers the single
// occurrence owed to one scheduled definition this tick.
func (d *Daemon) observeDueSchedule(definition store.AutomationDefinition, spec automation.DefinitionSpec, now time.Time) {
	if spec.Trigger.Schedule == nil {
		d.logf("automation schedule observation %s: scheduled trigger has no schedule", definition.ID)
		return
	}
	compiled, err := automation.CompileSchedule(*spec.Trigger.Schedule)
	if err != nil {
		d.logf("automation schedule observation compile %s: %v", definition.ID, err)
		return
	}
	cursor, ok, err := d.store.GetAutomationScheduleCursor(definition.ID)
	if err != nil {
		d.logf("automation schedule observation cursor %s: %v", definition.ID, err)
		return
	}
	if !ok {
		// First observation anchors the cursor at now — never fires retroactively.
		if err := d.store.SetAutomationScheduleCursor(definition.ID, now); err != nil {
			d.logf("automation schedule observation anchor %s: %v", definition.ID, err)
		}
		return
	}
	instants, ok := compiled.DueInstants(cursor, now, scheduleDueInstantCap)
	if !ok {
		// Replay-storm guard: jump the cursor to now instead of bursting.
		d.logf("automation schedule observation %s: replay storm guard hit, cursor advanced to now", definition.ID)
		if err := d.store.SetAutomationScheduleCursor(definition.ID, now); err != nil {
			d.logf("automation schedule observation advance %s: %v", definition.ID, err)
		}
		return
	}
	if len(instants) == 0 {
		return
	}
	// Only the newest due instant ever fires: at most one claim per definition
	// per tick.
	intended := instants[len(instants)-1]
	fire := true
	if spec.Trigger.CatchUp == "skip" {
		fire = now.Sub(intended) <= scheduleSkipGrace
	}
	if fire {
		if claimErr := d.claimAndDeliverScheduledRun(definition, spec, intended, now); claimErr != nil {
			// Claim rejected or failed: hold the cursor behind intended so the
			// instant stays eligible and the next tick re-decides — delayed,
			// never silently dropped.
			return
		}
	}
	// Advance only after a successful claim decision. A crash before this write
	// is safe: the claim is idempotent and the next tick recomputes intended.
	if err := d.store.SetAutomationScheduleCursor(definition.ID, now); err != nil {
		d.logf("automation schedule observation advance %s: %v", definition.ID, err)
	}
}

// claimAndDeliverScheduledRun claims the occurrence for intended (idempotent on
// definition + occurrence key) and delivers a freshly pending run. Non-nil error
// means no run row was claimed (caller withholds the cursor advance); delivery
// failures after a successful claim return nil — the run belongs to
// delivery/recovery from there.
func (d *Daemon) claimAndDeliverScheduledRun(definition store.AutomationDefinition, spec automation.DefinitionSpec, intended, observedAt time.Time) error {
	observationLock := d.automationObservationLock(definition.ID, "schedule", 0)
	observationLock.Lock()
	payload, err := json.Marshal(automation.NewScheduledInput(intended, observedAt))
	if err != nil {
		observationLock.Unlock()
		d.logf("automation schedule observation payload %s: %v", definition.ID, err)
		return err
	}
	effective, err := automation.Effective(spec, definition.Revision)
	if err != nil {
		observationLock.Unlock()
		d.logf("automation schedule observation snapshot %s: %v", definition.ID, err)
		return err
	}
	snapshotJSON, err := json.Marshal(effective)
	if err != nil {
		observationLock.Unlock()
		d.logf("automation schedule observation snapshot marshal %s: %v", definition.ID, err)
		return err
	}
	continuityKey := ""
	continuity, _ := automation.ResolvedTriggerPolicy(spec)
	if continuity == "singleton" {
		continuityKey = "singleton"
	}
	run, _, claimErr := d.store.ClaimScheduledAutomationRun(definition.ID, automation.ScheduledOccurrenceKey(intended), continuityKey, definition.Revision, string(payload), string(snapshotJSON), observedAt, newAutomationRunReservation())
	observationLock.Unlock()
	if claimErr != nil {
		d.logf("automation schedule observation claim %s: %v", definition.ID, claimErr)
		return claimErr
	}
	// A run row now exists regardless of delivery; broadcast so watchers see it.
	d.broadcastAutomationsChanged(definition.ID)
	d.automationMu.Lock()
	current, loadErr := d.store.GetAutomationRun(run.ID)
	if loadErr == nil && current != nil && current.State == store.AutomationRunStatePending {
		if deliverErr := d.deliverObservedAutomationRun(current); deliverErr != nil {
			_, deliverErr = d.handleAutomationDeliveryError(current, deliverErr)
			loadErr = deliverErr
		}
	}
	d.automationMu.Unlock()
	if loadErr != nil {
		d.logf("automation schedule observation deliver %s: %v", definition.ID, loadErr)
	}
	return nil
}
