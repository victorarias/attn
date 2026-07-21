package daemon

import (
	"os"
	"strings"
	"time"
)

// Ticket TTL sweep: the periodic backstop that actually runs
// store.SweepExpiredTickets, hard-deleting terminal tickets once they age
// past the TTL (and, as a side effect load-bearing for automations, releasing
// any continuity binding the ticket documents — see SweepExpiredTickets and
// hasPriorAutomationContinuityRun). SweepExpiredTickets shipped with a
// production default baked into its own doc comment ("time.Now() and 30
// days") but no caller ever wired it up; this is that caller. Env overrides
// mirror automationRetentionSweepInterval's idiom (internal/daemon/
// automation_retention.go) so tests can shrink both without touching real
// time.Sleep or minting hundreds of tickets.
const (
	defaultTicketRetentionTTL           = 30 * 24 * time.Hour
	defaultTicketRetentionSweepInterval = time.Hour
)

func ticketRetentionTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_TICKET_RETENTION_TTL")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultTicketRetentionTTL
}

func ticketRetentionSweepInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_TICKET_RETENTION_SWEEP_INTERVAL")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultTicketRetentionSweepInterval
}

// runTicketRetentionSweep is the dedicated periodic driver for the ticket
// TTL. Mirrors runAutomationRetentionSweep's shape exactly (itself mirroring
// runTicketReconcileSweep, ticket_reconcile.go) — no initial pass at boot
// (the TTL is measured in weeks, not urgent enough to compete with startup
// churn), select on d.done to stop cleanly at shutdown.
func (d *Daemon) runTicketRetentionSweep() {
	ticker := time.NewTicker(ticketRetentionSweepInterval())
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.ticketRetentionSweepPass(time.Now())
		}
	}
}

func (d *Daemon) ticketRetentionSweepPass(now time.Time) {
	if d.store == nil {
		return
	}
	removed, err := d.store.SweepExpiredTickets(now, ticketRetentionTTL())
	if err != nil {
		d.logf("ticket retention sweep: %v", err)
		return
	}
	if removed > 0 {
		d.logf("ticket retention sweep: removed %d expired ticket(s)", removed)
	}
}
