package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// watchTicketInbox runs unbabysat behind a harness Monitor, so its noise behavior
// matters: it must print new bundles, stay silent when nothing is new, and report a
// sustained daemon outage once rather than every poll (a Monitor treats each printed
// line as fresh activity and would nudge the chief every interval). This drives the
// loop deterministically with a manual tick channel and a scripted fetch so every
// poll is controlled — no daemon, signals, or wall-clock timer.
func TestWatchTicketInboxDedupesErrorsAndPrintsBundles(t *testing.T) {
	type step struct {
		bundles []protocol.TicketEventBundle
		err     error
	}
	down := errors.New("daemon down")
	steps := []step{
		{nil, nil}, // poll 0: silent
		{[]protocol.TicketEventBundle{{TicketID: "tkt-1"}}, nil}, // poll 1: prints the bundle
		{nil, down}, // poll 2: reports the outage
		{nil, down}, // poll 3: same error, suppressed
		{nil, nil},  // poll 4: recovered, silent, clears suppression
		{nil, down}, // poll 5: error again -> reported
	}

	// Buffered so the fetch closure never blocks: the loop pulls exactly one per poll.
	fetchCh := make(chan step, len(steps))
	for _, s := range steps {
		fetchCh <- s
	}
	fetch := func() ([]protocol.TicketEventBundle, error) {
		s := <-fetchCh
		return s.bundles, s.err
	}

	ctx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time) // unbuffered: a send blocks until the loop is back at its select
	var out, errOut bytes.Buffer
	done := make(chan struct{})
	go func() {
		watchTicketInbox(ctx, tick, fetch, &out, &errOut, false)
		close(done)
	}()

	// Poll 0 runs on entry. Each tick advances one poll; the unbuffered send only
	// returns once the loop has finished the previous poll and is waiting again.
	for k := 1; k < len(steps); k++ {
		tick <- time.Time{}
	}
	cancel() // unblocks the final select; the loop returns
	<-done

	if got := strings.Count(out.String(), "tkt-1"); got != 1 {
		t.Fatalf("bundle printed %d times, want exactly 1\nout:\n%s", got, out.String())
	}
	// Errors on polls 2, 3, 5; poll 3 is a consecutive duplicate (suppressed) and
	// poll 5 re-reports because poll 4's success cleared the suppression -> 2 lines.
	if got := strings.Count(errOut.String(), "ticket inbox --watch: daemon down"); got != 2 {
		t.Fatalf("outage reported %d times, want exactly 2 (poll 3 deduped)\nerr:\n%s", got, errOut.String())
	}
}
