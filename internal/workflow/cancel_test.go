package workflow

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// TestCancelWhileAwaitingLiveAgentSettlesAndTearsDownAgent is the regression for
// the cancel-during-await deadlock.
//
// A workflow parked on `await agent("x")` is blocked in TWO places at once:
//   - the event loop is parked on a Go channel receive (`<-el.jobs`), NOT inside
//     goja, so the watchdog's vm.Interrupt cannot reach it; and
//   - the live agent dispatch is parked inside AgentStub.Run.
//
// Canceling the run context must therefore (a) wake the event loop's pump select
// so the run settles as interrupted, and (b) be threaded into AgentStub.Run so the
// in-flight subagent is actually torn down. Before the fix neither path honored
// ctx — `<-el.jobs` and the stub's gate both ignored it — so `attn workflow cancel`
// hung forever on any run awaiting a live agent(). The 5s deadline guards below
// turn that regression into a clear failure instead of a hung test.
// Converted to synctest. Every wait here was a deadline guard standing in for a
// state the test could not otherwise observe: "in flight", "settled", "torn
// down". synctest.Wait blocks until every goroutine in the bubble is durably
// blocked, so each of those is now read off a settled system — and the deadlock
// this test regresses shows up as a bubble that never settles rather than as a
// 5s timeout.
func TestCancelWhileAwaitingLiveAgentSettlesAndTearsDownAgent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// blockingStub parks every Run until released. We NEVER release it here, so the
		// only way Run can return is by honoring ctx.Done() — exactly the path under test.
		stub := newBlockingStub(echoPrompt)
		eng := New(Config{Stub: stub, WatchdogTimeout: 30 * time.Second})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		type outcome struct {
			res RunResult
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			r, err := eng.Run(ctx, `return await agent("x");`, nil)
			done <- outcome{r, err}
		}()

		// The agent is genuinely in flight (parked inside the stub): the loop is now
		// parked on <-el.jobs and the dispatch is parked inside Run. This is the
		// precise state where the watchdog's vm.Interrupt is a no-op.
		synctest.Wait()
		if got := stub.inFlight.Load(); got != 1 {
			stub.releaseAll() // let the run unwind before failing
			<-done
			t.Fatalf("agent never reached in-flight; inFlight=%d", got)
		}

		// Cancel mid-await. Settling now depends ENTIRELY on the pump's ctx.Done() select
		// and the stub honoring ctx — neither the watchdog nor a stub release is involved.
		cancel()

		synctest.Wait()
		select {
		case got := <-done:
			if got.res.Status != StatusInterrupted {
				t.Fatalf("status = %s (err=%v), want interrupted", got.res.Status, got.res.Err)
			}
			var ie *ErrInterrupted
			if !errors.As(got.err, &ie) {
				t.Fatalf("err = %v (%T), want *ErrInterrupted", got.err, got.err)
			}
		default:
			t.Fatal("run did not settle after cancel — event loop stayed parked on <-el.jobs (cancel deadlock regression)")
		}

		// The run context must have reached AgentStub.Run: the parked stub observed
		// ctx.Done() and unwound, dropping in-flight back to 0. If ctx were not threaded
		// into Run, this goroutine would still be parked on the stub's gate.
		if got := stub.inFlight.Load(); got != 0 {
			t.Fatalf("in-flight agent was not torn down after cancel (inFlight=%d); run ctx was not threaded into AgentStub.Run", got)
		}
	})
}
