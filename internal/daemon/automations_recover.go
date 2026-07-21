package daemon

import (
	"context"
	"errors"

	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) recoverAutomations() {
	runs, err := d.store.ListPendingAutomationRuns()
	if err != nil {
		d.logf("automation recovery list: %v", err)
		return
	}
	for i := range runs {
		occurrence, occurrenceErr := d.store.GetAutomationOccurrence(runs[i].OccurrenceID)
		if occurrenceErr != nil {
			d.logf("automation recovery occurrence %s: %v", runs[i].OccurrenceID, occurrenceErr)
			continue
		}
		if occurrence != nil && occurrence.Provider == "github" {
			// Review-request demand must be refreshed before recovery decides whether
			// to deliver or cancel. The next successful provider observation retries
			// an accepted pending run or settles an inactive edge; generic startup
			// recovery must not race that snapshot using yesterday's active edge.
			continue
		}
		// Scheduled runs (occurrence.Provider == "schedule") fall through to
		// generic recovery: their payload is self-contained (the intended
		// instant, immutably snapshotted at claim time), so a pending run can
		// be delivered directly without refreshing any external demand first.
		d.automationMu.Lock()
		run, err := d.store.GetAutomationRun(runs[i].ID)
		if err == nil && run.State == "pending" {
			err = d.deliverAutomationRun(context.Background(), run)
			if err != nil {
				err = d.handleAutomationRecoveryError(run, err)
			}
		}
		d.automationMu.Unlock()
		if err != nil {
			d.logf("automation recovery run %s: %v", runs[i].ID, err)
		}
	}
}
func (d *Daemon) handleAutomationRecoveryError(run *store.AutomationRun, deliveryErr error) error {
	if errors.Is(deliveryErr, errAutomationReviewWithdrawn) {
		return d.cancelWithdrawnAutomationRun(run)
	}
	_, err := d.handleAutomationDeliveryError(run, deliveryErr)
	return err
}
func recoverAutomationsAfterGitHubReady(ready <-chan struct{}, recover func()) {
	<-ready
	recover()
}
