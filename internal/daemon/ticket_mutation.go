package daemon

import (
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/ticketnotify"
)

func requireExpectedTicketEventSeq(expected *int) error {
	if expected == nil {
		return fmt.Errorf("expected_event_seq is required; refresh the ticket before editing it")
	}
	return nil
}

func (d *Daemon) ticketMutationOptions(sessionID string) store.TicketMutationOptions {
	observers := d.ticketObserversForSession(sessionID)
	options := store.TicketMutationOptions{
		Observers:    make([]store.TicketMutationObserver, 0, len(observers)),
		AttentionKey: d.ticketAttentionKey(sessionID),
	}
	for _, observer := range observers {
		options.Observers = append(options.Observers, store.TicketMutationObserver{
			CursorIdentity: observer.ID,
			AuthorIdentity: observer.AuthorID,
		})
	}
	return options
}

func expectedTicketMutationOptions(expected *int) store.TicketMutationOptions {
	if expected == nil {
		return store.TicketMutationOptions{}
	}
	value := int64(*expected)
	return store.TicketMutationOptions{ExpectedEventSeq: &value}
}

func ticketMutationCatchUp(ticketID string, events []store.TicketEvent) *protocol.TicketEventBundle {
	if len(events) == 0 {
		return nil
	}
	bundles := ticketEventBundlesToProtocol([]ticketnotify.Bundle{{TicketID: ticketID, Events: events}})
	if len(bundles) == 0 {
		return nil
	}
	return &bundles[0]
}

// afterTicketMutationCatchUpLocked repairs live delivery state after the store's
// atomic read-before-write transaction advances cursors and attention. Caller holds
// deliveryMu so no stale deadline reconstruction can cross the new attention clock.
func (d *Daemon) afterTicketMutationCatchUpLocked(sessionID string, events []store.TicketEvent) {
	if len(events) == 0 {
		return
	}
	if d.debugLogging {
		d.logf("ticket delivery: observer=%s session=%s channel=mutation outcome=catch-up pending=%d", d.ticketAttentionKey(sessionID), sessionID, len(events))
	}
	// Catch-up advances the interruption clock. Any timer derived from its old
	// value is now stale and may be too early, so rebuild from the remaining
	// durable unread events instead of retaining the earliest-deadline timer.
	d.cancelNudgeCountdown(sessionID, "mutation catch-up")
	d.refreshTicketUnread(sessionID)
	d.notifyUnreadTicketSessionLocked(sessionID, time.Now())
}

func (d *Daemon) targetTicketUnreadCount(sessionID, ticketID string) int {
	seen := make(map[int64]struct{})
	for _, observer := range d.ticketObserversForSession(sessionID) {
		events, err := d.store.UnreadTicketEventsFor(observer.ID, observer.AuthorID)
		if err != nil {
			d.logf("ticket unread count %s/%s: %v", sessionID, ticketID, err)
			return 0
		}
		for _, event := range events {
			if event.TicketID == ticketID {
				seen[event.Seq] = struct{}{}
			}
		}
	}
	return len(seen)
}
