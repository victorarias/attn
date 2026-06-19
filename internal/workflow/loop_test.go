package workflow

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLinearThreeAgents(t *testing.T) {
	eng := newTestEngine()
	script := `
		const a = await agent("one");
		const b = await agent("two");
		const c = await agent("three");
		return [a, b, c];
	`
	res := runScript(t, eng, script, nil)
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}
	if res.LiveCalls != 3 {
		t.Fatalf("LiveCalls = %d, want 3", res.LiveCalls)
	}
	entries := res.Journal.Entries()
	if len(entries) != 3 {
		t.Fatalf("journal has %d entries, want 3", len(entries))
	}
	// Each linear call gets a distinct structural ordinal (distinct call sites).
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.Ordinal] {
			t.Errorf("duplicate ordinal %q", e.Ordinal)
		}
		seen[e.Ordinal] = true
		if e.Status != "ok" {
			t.Errorf("entry %s status=%s", e.Ordinal, e.Status)
		}
	}
	vals, ok := res.Value.([]interface{})
	if !ok || len(vals) != 3 {
		t.Fatalf("value = %#v, want 3-element slice", res.Value)
	}
}

func TestWatchdogKillsInfiniteLoop(t *testing.T) {
	eng := newTestEngine(func(c *Config) { c.WatchdogTimeout = 150 * time.Millisecond })
	start := time.Now()
	res := runScript(t, eng, `while(true){}`, nil)
	elapsed := time.Since(start)
	if res.Status != StatusInterrupted {
		t.Fatalf("status=%s err=%v (should be interrupted, not a hang)", res.Status, res.Err)
	}
	if !strings.Contains(res.Err.Error(), "watchdog") {
		t.Errorf("interrupt error %q should mention the watchdog", res.Err.Error())
	}
	// Must be killed promptly — well under a generous ceiling, proving it is not a hang.
	if elapsed > 3*time.Second {
		t.Errorf("watchdog took %v, expected near the 150ms timeout", elapsed)
	}
}

func TestWatchdogKillsBusyLoopAfterAwait(t *testing.T) {
	// A busy loop reached only after an await still gets killed (the watchdog arms
	// around each resolve() re-entry, not just the initial run).
	eng := newTestEngine(func(c *Config) { c.WatchdogTimeout = 150 * time.Millisecond })
	script := `
		await agent("warmup");
		while(true){}
	`
	res := runScript(t, eng, script, nil)
	if res.Status != StatusInterrupted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}
}

func TestContextCancelInterrupts(t *testing.T) {
	eng := newTestEngine(func(c *Config) { c.WatchdogTimeout = 30 * time.Second })
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	res, _ := eng.Run(ctx, `while(true){}`, nil)
	if res.Status != StatusInterrupted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}
	if !strings.Contains(res.Err.Error(), "cancel") {
		t.Errorf("cancel error %q should mention cancellation", res.Err.Error())
	}
}

func TestAgentLifetimeCapTrips(t *testing.T) {
	// while(true){ await agent() } trips the lifetime cap and rejects.
	eng := newTestEngine(func(c *Config) {
		c.AgentLifetimeCap = 5
		c.WatchdogTimeout = 5 * time.Second
	})
	script := `
		let n = 0;
		while (true) {
			await agent("call " + n);
			n++;
		}
	`
	res := runScript(t, eng, script, nil)
	if res.Status != StatusErrored {
		t.Fatalf("status=%s err=%v, want errored", res.Status, res.Err)
	}
	if !strings.Contains(res.Err.Error(), "lifetime cap") {
		t.Errorf("error %q should mention the lifetime cap", res.Err.Error())
	}
	// The cap is on LIVE calls; exactly the cap many should have run live.
	if res.LiveCalls != 5 {
		t.Errorf("LiveCalls = %d, want 5 (the cap)", res.LiveCalls)
	}
}

func TestTooManyItemsRejected(t *testing.T) {
	cases := []struct {
		name   string
		script string
	}{
		{
			name: "parallel over cap",
			script: `
				const thunks = [];
				for (let i = 0; i < 5; i++) thunks.push(() => agent("x" + i));
				return await parallel(thunks);
			`,
		},
		{
			name: "pipeline over cap",
			script: `
				const items = [];
				for (let i = 0; i < 5; i++) items.push(i);
				return await pipeline(items, (v, item, i) => agent("x" + item));
			`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newTestEngine(func(c *Config) { c.MaxItemsPerCall = 4 })
			res := runScript(t, eng, tc.script, nil)
			if res.Status != StatusErrored {
				t.Fatalf("status=%s, want errored", res.Status)
			}
			if !strings.Contains(res.Err.Error(), "per-call cap") {
				t.Errorf("error %q should mention the per-call cap", res.Err.Error())
			}
		})
	}
}
