package jobs

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/testinv"
)

// The states this package's tests must reach at least once per run. Both are
// preconditions rather than outcomes: the tests assert where a job ended up,
// none asserts that the queue was ever in the situation the assertion is about.
// See internal/testinv.
//
// Marked from memStore in memstore_test.go — the test double the real Runner
// dispatches against — so production code carries nothing.
var (
	// Delay is the whole mechanism behind both backoff and the coalescing
	// debounce, and a delay is invisible to an assertion about the final state:
	// every test here advances a fake clock and then checks where the job landed.
	// A runner that scheduled every retry and every debounced trigger at now
	// would keep all of them green while nothing was ever actually deferred.
	sawJobWithheldByItsSchedule = testinv.Sometimes("dispatch withholds a job whose scheduled time has not arrived")

	// The requeue-during-run flag is the one path where a coalescing trigger
	// cannot overwrite the record it targets, so the run it collides with has to
	// hand the trigger forward instead of dropping it.
	sawTriggerLandOnARunningJob = testinv.Sometimes("a coalescing trigger lands on a job that is already running")
)

func TestMain(m *testing.M) { os.Exit(testinv.Run(m)) }
