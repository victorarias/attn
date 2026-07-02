package tasks

import (
	"context"
	"testing"
	"time"
)

// recvWithin reads one value from ch or fails after 3s. Used to observe an
// executor entering (a channel sync point, never a sleep).
func recvWithin(t *testing.T, what string, ch <-chan string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return ""
	}
}

// assertNoRecv asserts nothing arrives on ch within a short window — the negative
// side of a concurrency-cap assertion (a still-blocked run must NOT have entered).
func assertNoRecv(t *testing.T, what string, ch <-chan string) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("%s: unexpected entry %q — the concurrency cap did not hold", what, v)
	case <-time.After(60 * time.Millisecond):
	}
}

// TestDifferentKindsRunConcurrently proves the whole point of the change: two
// DIFFERENT kinds run at the same time. Each executor parks on a shared release
// gate; both must enter before either is released. Under the old single-worker
// design the second kind could never enter while the first was parked, so this
// test would time out.
func TestDifferentKindsRunConcurrently(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)

	enteredA := make(chan string, 1)
	enteredB := make(chan string, 1)
	release := make(chan struct{})
	_ = r.Register("A", func(_ context.Context, task *Task) error {
		enteredA <- task.Subject
		<-release
		return nil
	})
	_ = r.Register("B", func(_ context.Context, task *Task) error {
		enteredB <- task.Subject
		<-release
		return nil
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	if _, err := r.Enqueue("A", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue("B", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}

	// BOTH must be running at once (neither released yet).
	recvWithin(t, "kind A to enter", enteredA)
	recvWithin(t, "kind B to enter (concurrently with A)", enteredB)

	close(release)
	waitFor(t, "both kinds to reach done", func() bool {
		a, _ := r.Get(TaskID("A", "s"))
		b, _ := r.Get(TaskID("B", "s"))
		return a != nil && a.State == StateDone && b != nil && b.State == StateDone
	})
}

// TestPerKindCapSerializesSameKind proves the default cap of 1 keeps a kind
// serialized with itself: two subjects of the same kind never run at once. The
// first parks; the second must not enter until the first finishes.
func TestPerKindCapSerializesSameKind(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)

	entered := make(chan string, 8)
	release := make(chan struct{})
	_ = r.Register("K", func(_ context.Context, task *Task) error {
		entered <- task.Subject
		<-release
		return nil
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	if _, err := r.Enqueue("K", "s1", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue("K", "s2", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}

	// Exactly one run enters; the other is cap-blocked while the first is parked.
	first := recvWithin(t, "the first same-kind run to enter", entered)
	assertNoRecv(t, "a second same-kind run started while the first was still running", entered)

	// Release the first; only now may the second run.
	close(release)
	second := recvWithin(t, "the second same-kind run to enter after the first finished", entered)
	if first == second {
		t.Fatalf("both entries were the same subject %q; expected the two distinct subjects", first)
	}
	waitFor(t, "both same-kind subjects to reach done", func() bool {
		a, _ := r.Get(TaskID("K", "s1"))
		b, _ := r.Get(TaskID("K", "s2"))
		return a != nil && a.State == StateDone && b != nil && b.State == StateDone
	})
}

// TestPerKindCapAllowsConfiguredConcurrency proves RegisterWith(MaxConcurrent: 2)
// lets exactly two runs of that kind proceed at once while a third waits for a
// freed slot. This is the shape PR3's reconcile kind uses.
func TestPerKindCapAllowsConfiguredConcurrency(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)

	entered := make(chan string, 8)
	release := make(chan struct{})
	if err := r.RegisterWith("K", func(_ context.Context, task *Task) error {
		entered <- task.Subject
		<-release
		return nil
	}, ExecutorConfig{MaxConcurrent: 2}); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	for _, s := range []string{"s1", "s2", "s3"} {
		if _, err := r.Enqueue("K", s, EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	// Two run concurrently (cap 2); the third is held back.
	recvWithin(t, "the first run to enter", entered)
	recvWithin(t, "the second run to enter (concurrently, cap 2)", entered)
	assertNoRecv(t, "a third run started while two of the same kind were already running", entered)

	// Free the two slots; the third now runs.
	close(release)
	recvWithin(t, "the third run to enter after a slot freed", entered)
	waitFor(t, "all three subjects to reach done", func() bool {
		for _, s := range []string{"s1", "s2", "s3"} {
			task, _ := r.Get(TaskID("K", s))
			if task == nil || task.State != StateDone {
				return false
			}
		}
		return true
	})
}

// TestStopDrainsConcurrentInFlightRuns proves Stop cancels and joins EVERY
// in-flight run, not just one. Two different kinds are parked on ctx.Done(); Stop
// must return only after both goroutines have exited.
func TestStopDrainsConcurrentInFlightRuns(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)

	enteredA := make(chan string, 1)
	enteredB := make(chan string, 1)
	exited := make(chan string, 2)
	_ = r.Register("A", func(ctx context.Context, task *Task) error {
		enteredA <- task.Subject
		<-ctx.Done() // Stop must cancel us
		exited <- task.Kind
		return ctx.Err()
	})
	_ = r.Register("B", func(ctx context.Context, task *Task) error {
		enteredB <- task.Subject
		<-ctx.Done()
		exited <- task.Kind
		return ctx.Err()
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue("A", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue("B", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	recvWithin(t, "kind A to enter", enteredA)
	recvWithin(t, "kind B to enter", enteredB)

	stopped := make(chan struct{})
	go func() {
		r.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return after cancelling both in-flight runs")
	}
	// Both run goroutines must have observed their cancellation and exited.
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case kind := <-exited:
			got[kind] = true
		case <-time.After(time.Second):
			t.Fatalf("only %d of 2 in-flight runs exited on Stop", len(got))
		}
	}
	if !got["A"] || !got["B"] {
		t.Fatalf("Stop did not drain both kinds; exited=%v", got)
	}
}
