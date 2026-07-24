package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/automation"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// automationRefusal tags an apply refusal with the machine-readable
// error_code the WS form uses to route the failure (banner vs. field).
// Unknown/transient errors are deliberately not tagged.
type automationRefusal struct {
	Code string
	Err  error
}

func (r *automationRefusal) Error() string { return r.Err.Error() }
func (r *automationRefusal) Unwrap() error { return r.Err }

const (
	automationErrCodeRevisionConflict = "revision_conflict"
	automationErrCodeIDCollision      = "id_collision"
	automationErrCodeDeletedElsewhere = "deleted_elsewhere"
	automationErrCodeIDMismatch       = "id_mismatch"
	automationErrCodeValidation       = "validation"
)

// defaultWSAutomationMutationTimeout is the fallback for
// Daemon.wsAutomationMutationTimeout (see wsAutomationMutationTimeoutDuration).
// 25s is deliberately strictly inside the frontend's 30s client timeout
// (useDaemonSocket.ts) so a WS set_enabled mutation either completes or
// aborts with a definitive error before the client gives up waiting — the
// client's timer can never observe a flip that happens after it already
// reported failure.
const defaultWSAutomationMutationTimeout = 25 * time.Second

// wsAutomationMutationTimeoutDuration resolves the effective deadline for a
// WS-originated automation mutation: the configured override if set, else
// defaultWSAutomationMutationTimeout.
func (d *Daemon) wsAutomationMutationTimeoutDuration() time.Duration {
	if d.wsAutomationMutationTimeout > 0 {
		return d.wsAutomationMutationTimeout
	}
	return defaultWSAutomationMutationTimeout
}

// validateAutomationSpec is the single seam every surface that judges
// automation definition YAML must call: automation_validate (validate-only,
// nothing persisted) and automationApply (validate-then-persist) both run
// through this, so a YAML document that validate accepts is guaranteed to be
// one apply also accepts — the two cannot drift apart by one of them
// forgetting a check. It runs ParseDefinitionYAML's schema validation plus
// every check automationApply used to run inline: the delegation agent must
// resolve, --model/--effort must be supported by it, the driver must be one
// of the automations subset that supports unattended automatic approval
// (codex|claude), and every repository_sources override must be a valid,
// reachable local clone.
func (d *Daemon) validateAutomationSpec(raw string) (automation.DefinitionSpec, []byte, error) {
	spec, canonical, err := automation.ParseDefinitionYAML([]byte(raw))
	if err != nil {
		return spec, nil, err
	}
	if _, err := d.resolveDelegationAgent("", protocol.Ptr(spec.Launch.Driver)); err != nil {
		return spec, nil, err
	}
	if err := d.validateDelegationModelEffort(spec.Launch.Driver, spec.Launch.Model, spec.Launch.Effort); err != nil {
		return spec, nil, err
	}
	if spec.Launch.Driver != "codex" && spec.Launch.Driver != "claude" {
		return spec, nil, fmt.Errorf("agent %q does not support automation automatic approval", spec.Launch.Driver)
	}
	for identity, source := range spec.Location.RepositorySources.Overrides {
		if _, err := attngit.ValidateLocalClone(source.Path, identity); err != nil {
			return spec, nil, fmt.Errorf("repository override %s: %w", identity, err)
		}
	}
	return spec, canonical, nil
}

// automationApply applies definition YAML unconditionally: parse, validate,
// upsert. This is the unguarded shape used by test fixtures and — via
// actionAutomationApply passing nil/nil for expectedID/expectedRevision — the
// unix-socket/CLI/agent surface (attn automation apply): "last writer wins."
// Thin wrapper over automationApplyWithGuards with both guards off; kept
// separate only because so many tests use it as fixture setup.
func (d *Daemon) automationApply(raw string) (*store.AutomationDefinition, error) {
	return d.automationApplyWithGuards(context.Background(), raw, nil, nil)
}

// automationApplyWithGuards is the one apply path shared by both transports
// (see actionAutomationApply). expectedID and expectedRevision implement
// D4/D5 from the design: apply is an upsert keyed on the id *inside* the
// YAML, so an edited id would silently fork the definition and leave the
// original enabled and running (D4) — a non-nil expectedID that mismatches
// refuses. And a stale expectedRevision would silently clobber a concurrent
// apply from the CLI or another app window (D5) — the caller sees "changed
// elsewhere — reload" instead.
//
// Both guards are keyed on POINTER PRESENCE, not on the zero value: nil means
// "this caller does not want this guard enforced at all" — the socket/CLI
// path (attn automation apply) never sets either field, so it gets true
// last-writer-wins, unguarded, exactly as the old unconditional automationApply
// did. The WS editor's Save always sends both fields (zero-valued for a
// create — expectedID "" / expectedRevision 0, matching AutomationEditor's
// loadedId/revision state before a first save), so its guards apply exactly
// as before this unification: expectedRevision 0 means "creating" (refuse a
// collision with a live definition) and a non-zero expectedRevision must
// match the stored one. Using the dereferenced zero value here instead of
// pointer presence would force EVERY caller — including the CLI's unguarded
// edits of existing definitions — through the "0 means creating" branch,
// wrongly rejecting a CLI apply of an existing definition as "already
// exists".
//
// Both guards run inside automationApplyLocked's automationMu, atomically
// with the pre-upsert existing-row read they depend on, so there is no
// window between the check and the write where a concurrent apply could
// slip through.
func (d *Daemon) automationApplyWithGuards(ctx context.Context, raw string, expectedID *string, expectedRevision *int) (*store.AutomationDefinition, error) {
	spec, canonical, err := d.validateAutomationSpec(raw)
	if err != nil {
		return nil, &automationRefusal{Code: automationErrCodeValidation, Err: err}
	}
	if expectedID != nil && *expectedID != "" && spec.ID != *expectedID {
		return nil, &automationRefusal{Code: automationErrCodeIDMismatch, Err: fmt.Errorf("definition id %q in the YAML does not match the definition being edited (%q) — apply is keyed on the id inside the YAML, so an id change must be made as a separate create", spec.ID, *expectedID)}
	}
	guard := func(existing *store.AutomationDefinition) error {
		if expectedRevision == nil {
			// No revision guard requested at all (the unguarded socket/CLI path).
			return nil
		}
		if *expectedRevision == 0 {
			// Create. Revisions start at 1 (see UpsertAutomationDefinition), so
			// this is unambiguous — it is never an edit of a revision-0 row.
			//
			// Apply is keyed on the id inside the YAML, so a create whose id
			// happens to match a live definition would UPDATE that definition
			// in place: the user typing an id that already exists would replace
			// someone else's automation wholesale, from a form that said
			// "New automation" and never mentioned it. Refuse instead. The
			// socket/CLI path keeps its unconditional last-writer-wins
			// semantics — it passes no guard at all.
			//
			// A soft-deleted row is deliberately NOT a collision: re-applying a
			// deleted definition's id is how resurrect works, and the editor's
			// list only shows live definitions, so the user cannot be
			// overwriting anything they can see.
			if existing != nil && existing.DeletedAt == nil {
				return &automationRefusal{Code: automationErrCodeIDCollision, Err: fmt.Errorf("an automation with id %q already exists — edit it instead of creating a second one", spec.ID)}
			}
			return nil
		}
		if existing == nil || existing.Revision != *expectedRevision {
			return &automationRefusal{Code: automationErrCodeRevisionConflict, Err: errors.New("automation definition changed elsewhere — reload before saving")}
		}
		if existing.DeletedAt != nil {
			// DeleteAutomationDefinition sets deleted_at without touching
			// revision, so a stale editor's expectedRevision can still match a
			// row that was soft-deleted out from under it. Unlike the create
			// path above, an edit must never resurrect: automationDelete already
			// failed this definition's pending runs, fenced its provider
			// cursors, and purged its continuity bindings and review-request
			// edges on the assumption it is gone, so a Save silently bringing it
			// back live would restart an unattended cron the user deliberately
			// deleted, with "saved successfully" as the only feedback.
			return &automationRefusal{Code: automationErrCodeDeletedElsewhere, Err: fmt.Errorf("automation %q was deleted elsewhere while you were editing it — your changes were not saved; close this editor and use New if you want to bring it back", spec.ID)}
		}
		return nil
	}
	return d.automationApplyLocked(ctx, spec, canonical, guard)
}

// automationApplyLocked is the locked validate-then-persist step shared by
// automationApply and automationApplyWithGuards. ctx bounds how long the
// caller is willing to wait to acquire automationMu (mirroring
// automationSetEnabled/automationDelete's contract): once locked, ctx.Err()
// is checked before any store mutation, so a caller whose deadline already
// passed aborts without writing anything. guard (nil for the unconditional
// socket/CLI path) runs after the pre-upsert existing-row read but still
// inside automationMu, so a WS caller's expected_id/expected_revision check
// is atomic with the write it gates.
func (d *Daemon) automationApplyLocked(ctx context.Context, spec automation.DefinitionSpec, canonical []byte, guard func(*store.AutomationDefinition) error) (*store.AutomationDefinition, error) {
	d.automationMu.Lock()
	defer d.automationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("deadline exceeded waiting for an in-flight automation delivery: %w", err)
	}
	existing, err := d.store.GetAutomationDefinitionIncludingDeleted(spec.ID)
	if err != nil {
		return nil, err
	}
	if guard != nil {
		if err := guard(existing); err != nil {
			return nil, err
		}
	}
	definition, err := d.store.UpsertAutomationDefinition(spec.ID, spec.Name, string(canonical), time.Now())
	if err != nil {
		return definition, err
	}
	if err := d.rotateContinuityBindingsIfContractChanged(existing, spec, definition); err != nil {
		return definition, err
	}
	d.broadcastAutomationsChanged(spec.ID)
	if definition.Enabled {
		return definition, nil
	}
	return definition, d.cancelPendingAutomationRuns(spec.ID, store.AutomationCancelReasonDefinitionDisabled)
}

// rotateContinuityBindingsIfContractChanged drops definitionID's continuity
// bindings (see A1 in docs/plans/2026-07-18-attn-automations-implementation.md)
// when this apply changed what a resumed session would be asked to do:
// resurrecting a soft-deleted definition always rotates (its old bindings
// may point at threads built under an arbitrarily stale contract), and
// otherwise rotates only when the new effective spec's
// automation.ContinuationContract (Prompt/Launch/Location) differs from the
// previous one — a cron/catch_up-only edit leaves bindings alone so an
// in-flight thread survives. existing is nil for a brand-new definition,
// which never has bindings to rotate.
func (d *Daemon) rotateContinuityBindingsIfContractChanged(existing *store.AutomationDefinition, spec automation.DefinitionSpec, updated *store.AutomationDefinition) error {
	if existing == nil {
		return nil
	}
	rotate := existing.DeletedAt != nil
	if !rotate && existing.Revision != updated.Revision {
		var oldSpec automation.DefinitionSpec
		if err := json.Unmarshal([]byte(existing.SpecJSON), &oldSpec); err != nil {
			// Can't prove the old contract was unchanged; rotate rather than risk
			// reusing a session under an instruction set we can no longer compare.
			rotate = true
		} else if old, oldErr := automation.Effective(oldSpec, existing.Revision); oldErr != nil {
			rotate = true
		} else if newSnapshot, newErr := automation.Effective(spec, updated.Revision); newErr != nil {
			rotate = true
		} else {
			rotate = !old.ContinuationContract().Equal(newSnapshot.ContinuationContract())
		}
	}
	if !rotate {
		return nil
	}
	return d.store.ReleaseAutomationContinuityBindings(spec.ID, store.AutomationBindingReleasedContractRotated, time.Now())
}

// cancelPendingAutomationRuns cancels every pending run for definitionID with
// reason, e.g. when a definition is disabled or deleted before its pending
// occurrences were delivered. These runs never got a chance to run — that's
// cancelled, not failed, in the v2 state model (see
// docs/plans/2026-07-21-automations-v2-simplification.md). One run's
// cancellation does not stop the rest; errors are joined. Shared by
// automationApply's disable path, automationSetEnabled's disable path, and
// automationDelete, each with its own reason.
func (d *Daemon) cancelPendingAutomationRuns(definitionID, reason string) error {
	pending, err := d.store.ListPendingAutomationRuns()
	if err != nil {
		return err
	}
	message := "automation definition disabled before delivery"
	if reason == store.AutomationCancelReasonDefinitionDeleted {
		message = "automation definition deleted before delivery"
	}
	for i := range pending {
		run := pending[i]
		if run.DefinitionID != definitionID {
			continue
		}
		if _, cancelErr := d.cancelAutomationRun(&run, reason, message); cancelErr != nil {
			err = errors.Join(err, cancelErr)
		}
	}
	return err
}

// automationSetEnabled flips definitionID's enabled flag, reusing the store's
// shared enable-transition side effects (review-edge clear + provider-cursor
// fence — see store.SetAutomationEnabled) and, on a disable transition,
// failing any pending runs through the same path automationApply uses. It is
// a no-op (success, no broadcast) when the definition already has the
// requested state, and errors for an unknown or soft-deleted definition.
//
// ctx bounds how long the caller is willing to wait to acquire automationMu:
// once locked, ctx.Err() is checked before any store mutation, so a caller
// whose deadline already passed aborts without flipping the flag (see
// wsAutomationMutationTimeoutDuration for the WS caller's deadline). The
// unix-socket caller passes context.Background() — the CLI/agent path waits
// synchronously with no deadline of its own, so behavior there is unchanged.
func (d *Daemon) automationSetEnabled(ctx context.Context, definitionID string, enabled bool) (*store.AutomationDefinition, error) {
	d.automationMu.Lock()
	defer d.automationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("deadline exceeded waiting for an in-flight automation delivery: %w", err)
	}
	definition, changed, err := d.store.SetAutomationEnabled(definitionID, enabled, time.Now())
	if err != nil {
		return nil, err
	}
	if definition == nil {
		return nil, fmt.Errorf("automation %q not found", definitionID)
	}
	if !changed {
		return definition, nil
	}
	if !enabled {
		err = d.cancelPendingAutomationRuns(definitionID, store.AutomationCancelReasonDefinitionDisabled)
	}
	d.broadcastAutomationsChanged(definitionID)
	return definition, err
}

// automationDelete soft-deletes definitionID (see A2): fails any pending
// runs (the same mechanism automationSetEnabled's disable path uses), clears
// review-request edges and continuity bindings, fences provider cursors so
// an in-flight stale observation can't act on it, then soft-deletes the row
// and broadcasts. Runs, occurrences, tickets, sessions, and on-disk
// worktrees/artifacts are untouched and remain fully listable/inspectable —
// A3/A4 govern their eventual cleanup. automationApply resurrects a
// soft-deleted definition by reapplying the same id.
//
// Mirrors automationSetEnabled's lock/deadline contract exactly: ctx bounds
// how long the caller is willing to wait for automationMu, checked
// immediately after acquiring it and before any store write, so a caller
// whose deadline already passed aborts without deleting anything (see
// wsAutomationMutationTimeoutDuration for the WS caller's deadline; the
// unix-socket caller passes context.Background()).
func (d *Daemon) automationDelete(ctx context.Context, definitionID string) error {
	d.automationMu.Lock()
	defer d.automationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("deadline exceeded waiting for an in-flight automation delivery: %w", err)
	}
	definition, err := d.store.GetAutomationDefinition(definitionID)
	if err != nil {
		return err
	}
	if definition == nil {
		return fmt.Errorf("automation %q not found", definitionID)
	}
	if err := d.cancelPendingAutomationRuns(definitionID, store.AutomationCancelReasonDefinitionDeleted); err != nil {
		return err
	}
	if err := d.store.DeleteAutomationReviewRequestEdges(definitionID); err != nil {
		return err
	}
	if err := d.store.ReleaseAutomationContinuityBindings(definitionID, store.AutomationBindingReleasedDefinitionDeleted, time.Now()); err != nil {
		return err
	}
	if err := d.store.FenceAutomationProviderCursors(definitionID, time.Now()); err != nil {
		return err
	}
	if err := d.store.DeleteAutomationDefinition(definitionID, time.Now()); err != nil {
		return err
	}
	d.broadcastAutomationsChanged(definitionID)
	return nil
}
