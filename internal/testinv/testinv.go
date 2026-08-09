// Package testinv catalogs states a package's tests must reach at least once
// per unfiltered run, and fails the run when one stops being reached — a
// condition (not a location) that quietly stops occurring is invisible to line
// coverage. Register marks in package-level vars in _test.go files with
// Sometimes, route TestMain through Run, call Reached where the state occurs.
// Scope is one package, one process. Marks live only in test files or test
// doubles; filtered runs (-run, -skip, -short, -list) are reported, not
// enforced. ATTN_TESTINV_VERBOSE=1 lists the whole inventory (pair with -v).
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

// Mark is one cataloged state: get it from Sometimes, call Reached where the
// state occurs.
type Mark struct {
	what string
	site string
	hit  atomic.Bool
}

// Reached records that the state happened. Safe from any goroutine; after the
// first hit it is an atomic load and a predicted branch — measured 3.0ns.
func (m *Mark) Reached() {
	if m == nil || m.hit.Load() {
		return
	}
	m.hit.Store(true)
}

// WasReached reports whether the state has happened yet.
func (m *Mark) WasReached() bool { return m != nil && m.hit.Load() }

// String is the mark's description.
func (m *Mark) String() string {
	if m == nil {
		return "<nil mark>"
	}
	return m.what
}

// catalog is one package's inventory; a type so the registry's rules can be
// tested without registering into the binary doing the testing.
type catalog struct {
	mu      sync.Mutex
	entries []*Mark
	byWhat  map[string]string // description -> registration site
}

func newCatalog() *catalog { return &catalog{byWhat: map[string]string{}} }

var defaultCatalog = newCatalog()

// Sometimes registers a state this package's tests must reach at least once per
// unfiltered run. Call it from a package-level var in a _test.go file —
// registration must precede TestMain. A duplicate description panics.
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

// Run substitutes for m.Run inside TestMain (it does not replace TestMain): it
// runs the tests, then turns a passing run into a failing one when a cataloged
// state was never reached.
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

// state is one inventory entry as review sees it.
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

// review renders the inventory report and says whether the run should fail.
// skipped is the reason enforcement does not apply, or "".
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

// filterReason names why this run cannot reach the whole inventory, or "".
// Testing's flags are parsed by m.Run, so only meaningful after it returns.
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

// callerSite is the package-relative file:line the mark was registered from.
func callerSite(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%s:%d", filepath.Join(filepath.Base(filepath.Dir(file)), filepath.Base(file)), line)
}
