package bus

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/testinv"
)

// The states this package's tests must reach at least once per run. Both are
// preconditions rather than outcomes: every test here asserts what the bus did,
// none asserts that the situation it was written for actually arose. See
// internal/testinv for what that buys.
//
// Marked from the in-memory Store and the recording handler in bus_test.go —
// test doubles the real Bus calls through — so production code carries nothing.
var (
	// The batch loop in drain exists for a consumer that is behind: it pages the
	// log, advances the cursor per event, and clears the failure streak on each
	// success. A bus whose consumers always caught up in one event would run none
	// of that and every test here would still pass — including
	// TestBackoffDoesNotRatchetAcrossSuccessfulDeliveries, whose whole reason for
	// existing is that the loop never sees an empty batch.
	sawMultiEventBatch = testinv.Sometimes("the log is read forward into a batch holding more than one event")

	// At-least-once is the bus's promise to its handlers, and the reason
	// wireProjections must tolerate redelivery. It is only a tested promise while
	// some handler in this package is actually handed an event twice.
	sawRedelivery = testinv.Sometimes("a consumer handler is given an event it has already been given")
)

func TestMain(m *testing.M) { os.Exit(testinv.Run(m)) }
