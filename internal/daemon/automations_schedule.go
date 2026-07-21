package daemon

import (
	"encoding/json"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/store"
)

// scheduleDueInstantCap bounds DueInstants' backlog walk. A cursor left far
// behind (e.g. the daemon was off for a long time on a minutely schedule)
// must never fire an unbounded burst of catch-up occurrences in one tick.
// A var, not a const, so tests can shrink it instead of constructing
// million-instant backlogs.
var scheduleDueInstantCap = 1_000_000

// scheduleSkipGrace is how long after an intended instant a "skip" catch-up
// policy will still fire it. Past this window the instant is considered
// missed and is never delivered.
const scheduleSkipGrace = 5 * time.Minute

// startAutomationScheduleLoop blocks running the scheduled-automation
// observation tick until done is closed. Intended to be launched in its own
// goroutine from Start, mirroring startNotebookCronEnqueuer.
func (d *Daemon) startAutomationScheduleLoop(done <-chan struct{}) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			d.observeDueSchedules(time.Now())
		}
	}
}

// observeDueSchedules is the per-tick fan-out over enabled scheduled
// automation definitions, deciding and claiming at most one due occurrence
// per definition. now is param-injected for testability.
func (d *Daemon) observeDueSchedules(now time.Time) {
	if d.isRecovering() {
		// Startup recovery has not yet decided existing pending runs; a fresh
		// observation racing ahead of it could double-claim or misjudge a
		// definition's cursor before recovery has settled prior state.
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
		// Definition validation should have prevented this at apply time.
		d.logf("automation schedule observation %s: scheduled trigger has no schedule", definition.ID)
		return
	}
	compiled, err := automation.CompileSchedule(*spec.Trigger.Schedule)
	if err != nil {
		// Definition validation should have prevented this at apply time.
		d.logf("automation schedule observation compile %s: %v", definition.ID, err)
		return
	}
	cursor, ok, err := d.store.GetAutomationScheduleCursor(definition.ID)
	if err != nil {
		d.logf("automation schedule observation cursor %s: %v", definition.ID, err)
		return
	}
	if !ok {
		// First observation anchors the cursor at now instead of firing
		// retroactively: only instants strictly after this point are due.
		if err := d.store.SetAutomationScheduleCursor(definition.ID, now); err != nil {
			d.logf("automation schedule observation anchor %s: %v", definition.ID, err)
		}
		return
	}
	instants, ok := compiled.DueInstants(cursor, now, scheduleDueInstantCap)
	if !ok {
		// Replay-storm guard: an enormous backlog must never fire an unbounded
		// catch-up burst. Jump the cursor to now and resume normal observation
		// on the next tick.
		d.logf("automation schedule observation %s: replay storm guard hit, cursor advanced to now", definition.ID)
		if err := d.store.SetAutomationScheduleCursor(definition.ID, now); err != nil {
			d.logf("automation schedule observation advance %s: %v", definition.ID, err)
		}
		return
	}
	if len(instants) == 0 {
		// Nothing due: leave the cursor untouched so anchor semantics survive
		// a run of empty ticks between scheduled slots.
		return
	}
	// Only the newest due instant is ever a fire candidate; older due instants
	// never fire, so at most one claim happens per definition per tick.
	intended := instants[len(instants)-1]
	fire := true
	if spec.Policy.CatchUp == "skip" {
		fire = now.Sub(intended) <= scheduleSkipGrace
	}
	if fire {
		if claimErr := d.claimAndDeliverScheduledRun(definition, spec, intended, now); claimErr != nil {
			// The claim was rejected (revision mismatch, a singleton's
			// undelivered-predecessor guard, or a transient store error): hold
			// the cursor behind intended so the missed instant stays eligible
			// and the next tick re-reads the definition and re-decides. The
			// retry fires the newest instant due at that time under the normal
			// newest-due-wins rule — the identical instant unless a newer one
			// has become due meanwhile (minutely-grade schedules) — so a claim
			// failure delays the definition's appointment; it never silently
			// drops it, and it never prefers a stale instant over a fresher
			// due one.
			return
		}
	}
	// Cursor advances after a successful claim decision (fire or skip), not
	// unconditionally: a crash between claim and this write is safe because
	// the next tick recomputes the same intended instant and the claim is
	// idempotent; a claim error must not advance (handled above), keeping the
	// missed instant eligible until a retry succeeds or a newer due instant
	// supersedes it.
	if err := d.store.SetAutomationScheduleCursor(definition.ID, now); err != nil {
		d.logf("automation schedule observation advance %s: %v", definition.ID, err)
	}
}

// claimAndDeliverScheduledRun claims the occurrence for intended (idempotent
// on definition + occurrence key) and, on a freshly pending run, delivers it
// exactly like the GitHub review-request observation path. It returns a
// non-nil error when no run row was claimed — claim rejection/failure or any
// pre-claim preparation failure; the caller uses that to withhold the cursor
// advance. Delivery failures after a successful claim always return nil: the
// run row already exists and is owned by delivery/recovery from here, not by
// re-claiming.
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
	if spec.Policy.Continuity == "singleton" {
		continuityKey = "singleton"
	}
	run, _, claimErr := d.store.ClaimScheduledAutomationRun(definition.ID, automation.ScheduledOccurrenceKey(intended), continuityKey, definition.Revision, string(payload), string(snapshotJSON), observedAt, newAutomationRunReservation())
	observationLock.Unlock()
	if claimErr != nil {
		d.logf("automation schedule observation claim %s: %v", definition.ID, claimErr)
		return claimErr
	}
	// A run now exists for this definition (freshly claimed, or the
	// idempotent dedup of an already-claimed one) whether or not delivery
	// below succeeds; broadcast so a WS client watching this definition's
	// runs sees it appear without waiting on the delivery outcome.
	d.broadcastAutomationsChanged(definition.ID)
	d.automationMu.Lock()
	current, loadErr := d.store.GetAutomationRun(run.ID)
	if loadErr == nil && current != nil && current.State == "pending" {
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
