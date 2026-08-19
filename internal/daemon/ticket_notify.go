package daemon

import (
	"fmt"
	"strconv"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// ticketNudgePrompt is the fixed doorbell typed into a nudge-eligible agent: a
// bounded "go look" trigger, never event content. The agent then reads its own
// board with `attn ticket list`. This is the doorbell rule — the daemon signals,
// it never streams the message into the PTY.
const ticketNudgePrompt = "📋 Activity on a ticket that predates the garden — read the board with `attn ticket list`."

// defaultTicketBundleWindow is the quiet threshold after a ticket doorbell.
// Busy delegation tickets in the production event history had a median
// inter-event gap of 9m49s (440 gaps across 67 tickets with at least five events),
// so ten minutes sits just past the observed burst cadence.
const defaultTicketBundleWindow = 10 * time.Minute
const ticketWatchLeaseWindow = 5 * time.Second

func ticketWatchLeaseWindowFor(intervalMS *string) time.Duration {
	if intervalMS == nil {
		return ticketWatchLeaseWindow
	}
	milliseconds, err := strconv.ParseInt(*intervalMS, 10, 64)
	if err != nil || milliseconds <= 0 {
		return ticketWatchLeaseWindow
	}
	const maxDuration = time.Duration(1<<63 - 1)
	if milliseconds > int64(maxDuration/time.Millisecond) {
		return maxDuration
	}
	interval := time.Duration(milliseconds) * time.Millisecond
	grace := interval / 2
	if grace < time.Second {
		grace = time.Second
	}
	if interval > maxDuration-grace {
		return maxDuration
	}
	return interval + grace
}

func (d *Daemon) ticketBundleWindow() time.Duration {
	if d.ticketBundleWindowOverride > 0 {
		return d.ticketBundleWindowOverride
	}
	return defaultTicketBundleWindow
}

func (d *Daemon) ticketDeadline(sessionID string, newestPendingSeq int64, now time.Time) (time.Time, bool, error) {
	attention, found, err := d.store.TicketDeliveryAttention(d.ticketAttentionKey(sessionID))
	if err != nil {
		return time.Time{}, false, err
	}
	if found && newestPendingSeq <= attention.DeliveredThroughSeq {
		return time.Time{}, false, nil
	}
	deadline := now.Add(d.nudgeWindow())
	if !found || !attention.LastAttentionAt.Add(d.ticketBundleWindow()).After(now) {
		return deadline, true, nil
	}
	if bundled := attention.LastAttentionAt.Add(d.ticketBundleWindow()); bundled.After(deadline) {
		deadline = bundled
	}
	return deadline, false, nil
}

// ticketNudger adapts the daemon's doorbell primitive to ticketnotify.Nudger.
type ticketNudger struct{ d *Daemon }

func (n ticketNudger) Nudge(observerID string) error {
	// The immediate doorbell is gone: arm a visible, pausable countdown instead. The
	// countdown's timer fire is the only place a real doorbell happens, and only when
	// the user is not actively typing into the session (the anti-splice guard).
	n.d.armNudgeCountdown(observerID)
	return nil
}

// notifyTicketObservers runs the notification handler for every live session
// involved with a ticket after an event lands on it. A producer blanket-notifies
// without caring who caused the event: each observer sees only what it did not
// author (Notify -> Unread), so the author never notifies itself. All runtimes
// share one delivery policy: only an approval prompt blocks a countdown. An optional
// `ticket inbox --watch` may consume the unread activity before it rings.
func (d *Daemon) notifyTicketObservers(ticketID string) {
	if d.ptyBackend == nil || d.store == nil {
		return
	}
	participants, err := d.store.TicketParticipants(ticketID)
	if err != nil {
		d.logf("ticket notify: participants for %s: %v", ticketID, err)
		return
	}
	now := time.Now()
	targets := make(map[string]bool, len(participants))
	var sleepingMembers []string
	for _, identity := range participants {
		if _, member := store.ParseTicketMemberIdentity(identity); member {
			if id := d.ticketSessionForIdentity(identity); id != "" {
				targets[id] = true
			} else {
				sleepingMembers = append(sleepingMembers, identity)
			}
			continue
		}
		if id := d.ticketSessionForIdentity(identity); id != "" {
			targets[id] = true
		}
	}
	for id := range targets {
		d.notifyTicketSession(id, now)
	}
	for _, identity := range sleepingMembers {
		d.notifySleepingTicketMember(identity, ticketID)
	}
}

// notifySleepingTicketMember wakes a member only when this ticket still has
// unread activity for the durable member identity. The unread event is the
// durable delivery: neither a failed wake nor its warning advances the cursor.
func (d *Daemon) notifySleepingTicketMember(identity, ticketID string) {
	memberID, ok := store.ParseTicketMemberIdentity(identity)
	if !ok {
		return
	}
	events, err := d.store.UnreadTicketEventsFor(identity, identity)
	if err != nil {
		d.logf("ticket notify: unread for %s: %v", identity, err)
		return
	}
	unread := false
	for _, event := range events {
		if event.TicketID == ticketID {
			unread = true
			break
		}
	}
	if !unread {
		return
	}

	result, err := d.crewWakeWithDelivery(memberID, "", true, &crewWakeDelivery{
		AfterInitialPrompt: func(sessionID string) {
			d.notifyTicketSession(sessionID, time.Now())
		},
	})
	if err != nil {
		d.notifyTicketMemberWakeRefused(memberID, ticketID, err)
		return
	}
	if result.AlreadyAwake {
		d.notifyTicketSession(result.SessionID, time.Now())
		return
	}
	// The indicator can show immediately, but the actual nudge waits on the
	// prompt-submit receipt registered above, behind charter and handoff priming.
	d.refreshTicketUnread(result.SessionID)
}

// seedCrewTicketWakeDeliveries reconstructs the priming gate after a daemon
// restart. Its premise is durable: a live member binding plus unread member
// activity. Rebuilding from those facts prevents a restart between spawn and
// prompt submission from either splicing the nudge ahead of priming or losing
// it altogether.
func (d *Daemon) seedCrewTicketWakeDeliveries() error {
	members, _, err := d.readCrewMembers()
	if err != nil {
		return err
	}
	for _, member := range members {
		if !d.crewBindingLive(member) {
			continue
		}
		identity := store.TicketMemberIdentity(member.ID)
		events, err := d.store.UnreadTicketEventsFor(identity, identity)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			continue
		}
		sessionID := member.BindingSession
		session := d.store.Get(sessionID)
		if session == nil {
			continue
		}
		state := string(session.State)
		if state == protocol.StateLaunching || state == protocol.StateWorking {
			d.notePostInitialPrompt(sessionID, func() { d.notifyTicketSession(sessionID, time.Now()) })
			d.refreshTicketUnread(sessionID)
			continue
		}
		// A settled session has already crossed its first prompt. Restore its
		// ordinary unread delivery immediately instead of waiting for a hook it
		// may never emit while idle.
		d.notifyTicketSession(sessionID, time.Now())
	}
	return nil
}

const notificationKindCrewTicketWakeRefused = "crew_ticket_wake_refused"

func (d *Daemon) notifyTicketMemberWakeRefused(memberID, ticketID string, wakeErr error) {
	name := crew.DisplayName(memberID)
	d.logf("ticket notify: could not wake %s for %s: %v; activity remains unread", name, ticketID, wakeErr)
	record, err := d.store.AddNotification(store.NotificationRecord{
		Kind:       notificationKindCrewTicketWakeRefused,
		Severity:   store.NotificationWarning,
		Title:      fmt.Sprintf("Could not wake %s for ticket activity", name),
		Body:       fmt.Sprintf("Ticket %s is still unread. Wake %s from the sidebar or run `attn crew wake %s`.", ticketID, name, memberID),
		Detail:     wakeErr.Error(),
		SourceKind: "ticket",
		SourceID:   ticketID,
	}, time.Now())
	if err != nil {
		d.logf("notifications: add crew ticket wake refusal for %s: %v", memberID, err)
		return
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
}

// notifyTicketSession runs Notify for one session's observer when it is a live
// session. A participant that is not a live session — the attn crash author, or a
// session already gone — is skipped: there is nothing to nudge.
func (d *Daemon) notifyTicketSession(sessionID string, now time.Time) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	// Reflect unread ticket activity on the session for the indicator, independent of
	// the delivery mechanism, so an active agent and an optional watcher both surface
	// the unread marker.
	d.refreshTicketUnread(sessionID)
	d.notifyUnreadTicketSession(sessionID, now)
}

// syncNudgeForState cancels a queued doorbell only while a session is waiting for
// approval. Leaving that state rechecks unread activity so a previously deferred
// nudge is armed as soon as it is safe.
func (d *Daemon) syncNudgeForState(sessionID, state string) {
	if !isNudgeDeliveryAllowed(state) {
		d.cancelNudgeCountdown(sessionID, "waiting for approval")
		return
	}
	go d.notifyUnreadTicketSession(sessionID, time.Now())
}

// notifyUnreadTicketSession rebuilds a deadline after an approval wait or daemon
// recovery, when there is no single triggering ticket. It derives the earliest
// eligible deadline from durable unread events instead of persisting a scheduler.
func (d *Daemon) notifyUnreadTicketSession(sessionID string, now time.Time) {
	d.deliveryMu.Lock()
	defer d.deliveryMu.Unlock()
	d.notifyUnreadTicketSessionLocked(sessionID, now)
}

// notifyUnreadTicketSessionLocked is the serialized form used by catch-up paths
// that already hold deliveryMu. Keeping the unread scan, attention read, deadline
// calculation, and timer arm in one critical section prevents an old calculation
// from re-arming an earlier countdown after a concurrent consume advances the
// observer's attention clock.
func (d *Daemon) notifyUnreadTicketSessionLocked(sessionID string, now time.Time) {
	if d.store == nil {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil || d.initialPromptPending(sessionID) || !isNudgeDeliveryAllowed(string(session.State)) {
		return
	}
	pending := make(map[int64]struct{})
	for _, observer := range d.ticketObserversForSession(sessionID) {
		events, err := d.store.UnreadTicketEventsFor(observer.ID, observer.AuthorID)
		if err != nil {
			d.logf("ticket notify rebuild: %s: %v", sessionID, err)
			return
		}
		for _, event := range events {
			pending[event.Seq] = struct{}{}
		}
	}
	if len(pending) > 0 {
		var newestPendingSeq int64
		for seq := range pending {
			if seq > newestPendingSeq {
				newestPendingSeq = seq
			}
		}
		deadline, immediate, err := d.ticketDeadline(sessionID, newestPendingSeq, now)
		if err != nil {
			d.logf("ticket notify deadline: %s: %v", sessionID, err)
			return
		}
		if deadline.IsZero() {
			return
		}
		if d.ticketRebuildBeforeArmHook != nil {
			d.ticketRebuildBeforeArmHook(sessionID, deadline)
		}
		if d.debugLogging {
			d.logf("ticket delivery: observer=%s session=%s class=%s pending=%d deadline=%s channel=countdown outcome=armed", d.ticketAttentionKey(sessionID), sessionID, map[bool]string{true: "immediate", false: "bundled"}[immediate], len(pending), deadline.Format(time.RFC3339))
		}
		d.armNudgeCountdownAt(sessionID, deadline)
	}
}
