// Package testinv keeps an inventory of interesting states a package's tests are
// supposed to reach, and fails the run when one of them stops being reached.
//
// The problem it exists for: a test suite can keep passing while it quietly
// stops exercising the thing it was written for. The assertion still holds, but
// the state it was meant to hold *about* is no longer produced — a refactor
// removed the delay a backoff test was pacing on, a faster consumer means the
// batch loop never sees a batch, a fixture drifted and the race the test provokes
// no longer happens. Line coverage cannot see any of that: the lines still run.
// It is a condition, not a location, that went missing.
//
// A cataloged state names such a condition and marks the spot where it occurs:
//
//	// package-level, in a _test.go file
//	var sawBacklogBatch = testinv.Sometimes("a drain is handed a batch of more than one event")
//
//	func TestMain(m *testing.M) { os.Exit(testinv.Run(m)) }
//
//	// wherever the condition can be observed
//	if len(batch) > 1 {
//		sawBacklogBatch.Reached()
//	}
//
// Reached says "this happened at least once" — not "this must happen here", and
// not "this must happen every time". Any test in the package may be the one that
// reaches it; no test owns it. That is the point: the claim is about the run, so
// it survives the individual test that happens to satisfy it being rewritten or
// deleted.
//
// # Scope
//
// One package, one process. `go test` runs each package in its own binary, so
// the inventory is per-package and Run checks it when that package's tests are
// done. Suite-wide aggregation would need a file drop and a summary step; it is
// not built here.
//
// # Where a mark may live
//
// Marks belong in test files or test-only helpers. In practice the best host is
// a test double that production code calls through — the package's in-memory
// Store, its fake clock, its recording handler — because the condition it
// observes is produced by the real code under test while the observation itself
// costs production nothing. A condition that is only visible from inside
// production code cannot be cataloged this way, and that is a deliberate limit,
// not an oversight.
//
// # When enforcement is off
//
// A filtered run (`-run`, `-skip`, `-short`, `-list`) cannot be expected to reach
// the whole inventory, so Run reports what it skipped and checks nothing. Be
// aware that go test suppresses a passing binary's output, so that notice only
// reaches the terminal under -v: a mark defends an unfiltered suite run, and
// never a developer's inner loop.
//
// # What it costs
//
// A package that never calls Sometimes registers nothing and Run is m.Run. A
// mark reads before it writes, so after the first hit it is an atomic load and a
// predicted branch — 3.0ns, and no contended store per hit. Placing one inside a
// test double that production code calls on a hot path is not a judgement call.
//
// # Reporting
//
// ATTN_TESTINV_VERBOSE=1 lists the whole inventory, reached and missing, instead
// of only reporting what went wrong. `go test` hides a passing binary's output,
// so pair it with -v.
package testinv

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Mark is one cataloged state. Get one from Sometimes; call Reached where the
// state occurs.
type Mark struct {
	what string
	site string
	hit  atomic.Bool
}

// Reached records that the state happened. Safe from any goroutine, and cheap
// enough to sit on a hot path: after the first hit it is a plain atomic load.
func (m *Mark) Reached() {
	if m == nil || m.hit.Load() {
		return
	}
	m.hit.Store(true)
}

// WasReached reports whether the state has happened yet. Tests that want to
// assert about the catalog itself use this; ordinary marks do not need it.
func (m *Mark) WasReached() bool { return m != nil && m.hit.Load() }

// String is the mark's description, so a mark drops into a %s without ceremony.
func (m *Mark) String() string {
	if m == nil {
		return "<nil mark>"
	}
	return m.what
}

// catalog is one package's inventory. There is exactly one per test binary; the
// type exists so the registry's own rules can be tested without registering into
// the binary that is doing the testing.
type catalog struct {
	mu      sync.Mutex
	entries []*Mark
	byWhat  map[string]string // description -> registration site
}

func newCatalog() *catalog { return &catalog{byWhat: map[string]string{}} }

var defaultCatalog = newCatalog()

// Sometimes registers a state that this package's tests must reach at least once
// per unfiltered run, and returns the mark to call Reached on.
//
// Call it from a package-level var in a _test.go file: registration has to happen
// before TestMain runs, and a var initializer is the only place that is
// guaranteed. Descriptions must be unique within the package — a duplicate makes
// the report ambiguous about which site went quiet, so it panics.
func Sometimes(what string) *Mark { return defaultCatalog.add(what, callerSite(2)) }

func (c *catalog) add(what, site string) *Mark {
	what = strings.TrimSpace(what)
	if what == "" {
		panic("testinv: a cataloged state needs a description")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if prev, dup := c.byWhat[what]; dup {
		panic(fmt.Sprintf("testinv: %q is already cataloged at %s", what, prev))
	}
	c.byWhat[what] = site
	m := &Mark{what: what, site: site}
	c.entries = append(c.entries, m)
	return m
}

// Run runs the package's tests and then checks the inventory. Use it as the whole
// body of TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(testinv.Run(m)) }
//
// A package that already has a TestMain wraps its own setup around this call the
// way it wrapped m.Run — Run substitutes for m.Run, it does not replace TestMain:
//
//	func TestMain(m *testing.M) {
//		dir, _ := os.MkdirTemp("", "attn-test")
//		config.ScopeTestEnvironment(dir)
//		code := testinv.Run(m)
//		os.RemoveAll(dir)
//		os.Exit(code)
//	}
//
// The returned code is the tests' own when they failed; a complete inventory
// leaves it alone, and a state that was never reached turns a passing run into a
// failing one.
func Run(m *testing.M) int {
	code := m.Run()

	verbose := os.Getenv("ATTN_TESTINV_VERBOSE") != ""
	report, incomplete := review(defaultCatalog.snapshot(), filterReason(), code == 0, verbose)
	if report != "" {
		fmt.Fprint(os.Stderr, report)
	}
	if incomplete && code == 0 {
		code = 1
	}
	return code
}

// state is one inventory entry as review sees it, so review can be tested
// without registering marks in the package under test.
type state struct {
	what    string
	site    string
	reached bool
}

func (c *catalog) snapshot() []state {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]state, 0, len(c.entries))
	for _, m := range c.entries {
		out = append(out, state{what: m.what, site: m.site, reached: m.hit.Load()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].what < out[j].what })
	return out
}

// review turns the inventory into the text the run prints and says whether the
// run should fail. skipped is the reason enforcement does not apply, or "";
// testsPassed decides how the advice is worded, because "the tests still pass"
// is only worth saying when they did.
func review(states []state, skipped string, testsPassed, verbose bool) (report string, incomplete bool) {
	if len(states) == 0 {
		return "", false
	}

	var b strings.Builder
	if skipped != "" {
		fmt.Fprintf(&b, "testinv: not checking %s (%s)\n", plural(len(states)), skipped)
		if verbose {
			writeAll(&b, states)
		}
		return b.String(), false
	}

	var missing []state
	for _, s := range states {
		if !s.reached {
			missing = append(missing, s)
		}
	}
	if len(missing) == 0 {
		if verbose {
			fmt.Fprintf(&b, "testinv: reached all %s\n", plural(len(states)))
			writeAll(&b, states)
		}
		return b.String(), false
	}

	fmt.Fprintf(&b, "\n--- FAIL: testinv (%s never reached this run)\n", plural(len(missing)))
	for _, s := range missing {
		fmt.Fprintf(&b, "    NEVER REACHED: %s\n", s.what)
		fmt.Fprintf(&b, "        cataloged at %s\n", s.site)
	}
	if testsPassed {
		b.WriteString("    The tests still pass, so nothing else will say this. A cataloged state\n")
		b.WriteString("    that stops happening means the suite no longer exercises the path it was\n")
		b.WriteString("    written for. Restore the state, or drop the entry and say why.\n")
	} else {
		b.WriteString("    Tests failed in this run too, so fix those first — a run that stops early\n")
		b.WriteString("    reaches less, and these may recover on their own.\n")
	}
	if verbose {
		writeAll(&b, states)
	}
	return b.String(), true
}

func writeAll(b *strings.Builder, states []state) {
	for _, s := range states {
		mark := "reached"
		if !s.reached {
			mark = "MISSING"
		}
		fmt.Fprintf(b, "    [%s] %s (%s)\n", mark, s.what, s.site)
	}
}

func plural(n int) string {
	if n == 1 {
		return "1 cataloged state"
	}
	return fmt.Sprintf("%d cataloged states", n)
}

// filterReason names why this run cannot be expected to reach the whole
// inventory, or "" when it can. Testing's flags are parsed by m.Run, so this is
// only meaningful after it returns.
func filterReason() string {
	if v := testFlag("test.run"); v != "" {
		return "this run is filtered by -run " + v
	}
	if v := testFlag("test.skip"); v != "" {
		return "this run is filtered by -skip " + v
	}
	if testFlag("test.short") == "true" {
		return "this run is -short"
	}
	if v := testFlag("test.list"); v != "" {
		return "this run is -list " + v
	}
	return ""
}

func testFlag(name string) string {
	f := flag.Lookup(name)
	if f == nil {
		return ""
	}
	return f.Value.String()
}

// callerSite is the file:line the mark was registered from, so the report can
// point at the var rather than making someone grep for the description.
func callerSite(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	// Package-relative is enough to find it and does not leak the build path.
	return fmt.Sprintf("%s:%d", filepath.Join(filepath.Base(filepath.Dir(file)), filepath.Base(file)), line)
}
