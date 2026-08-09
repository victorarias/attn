package testinv

import (
	"os"
	"strings"
	"testing"
)

// The package dogfoods itself: this state is reached by TestReachedIsIdempotent
// below, so a run of this package exercises registration, Reached, and Run's
// end-of-run check for real. It is the only mark this file puts in the default
// catalog — everything else registers into a throwaway one, so -count=N does not
// trip the duplicate rule on its own tests.
var selfCheck = Sometimes("testinv's own mark was reached at least once")

func TestMain(m *testing.M) { os.Exit(Run(m)) }

func TestReachedIsIdempotentAndVisible(t *testing.T) {
	selfCheck.Reached()
	selfCheck.Reached()
	if !selfCheck.WasReached() {
		t.Fatalf("Reached did not stick")
	}
	if got := selfCheck.String(); !strings.Contains(got, "testinv's own mark") {
		t.Errorf("String() = %q, want the description", got)
	}

	fresh := newCatalog().add("a state nothing has reached", "x_test.go:1")
	if fresh.WasReached() {
		t.Errorf("a freshly cataloged state reported itself reached")
	}
}

func TestANilMarkIsInert(t *testing.T) {
	var m *Mark
	m.Reached() // must not panic: a mark behind a build tag may be nil
	if m.WasReached() {
		t.Errorf("a nil mark reported itself reached")
	}
}

// Two marks with the same description make the report ambiguous about which site
// went quiet, which is the one thing the report has to be right about.
func TestCatalogingTheSameStateTwicePanics(t *testing.T) {
	const what = "a duplicated description"
	c := newCatalog()
	c.add(what, "first_test.go:1")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("cataloging %q twice did not panic", what)
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, what) || !strings.Contains(msg, "first_test.go:1") {
			t.Errorf("panic %v names neither the duplicate nor where it came from", r)
		}
	}()
	c.add(what, "second_test.go:2")
}

func TestAnEmptyDescriptionPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("an empty description did not panic")
		}
	}()
	newCatalog().add("   ", "x_test.go:1")
}

// The report points at the var so nobody has to grep for the description.
func TestSometimesRecordsWhereItWasCataloged(t *testing.T) {
	if !strings.HasPrefix(selfCheck.site, "testinv/testinv_test.go:") {
		t.Errorf("registration site = %q, want this file", selfCheck.site)
	}
}

// review is where the run's verdict is decided, so it is tested directly on
// synthetic inventories rather than by wrangling a second test binary.
func TestAnUnreachedStateFailsTheRunAndNamesItself(t *testing.T) {
	report, incomplete := review([]state{
		{what: "a batch of more than one event", site: "bus/testinv_test.go:12"},
		{what: "a redelivery", site: "bus/testinv_test.go:15", reached: true},
	}, "", true, false)

	if !incomplete {
		t.Fatalf("an unreached state did not fail the run")
	}
	for _, want := range []string{
		"1 cataloged state never reached",
		"NEVER REACHED: a batch of more than one event",
		"bus/testinv_test.go:12",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report is missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "NEVER REACHED: a redelivery") {
		t.Errorf("report named a state that was reached:\n%s", report)
	}
}

// A run whose tests failed reaches less by construction, so the advice must not
// tell someone chasing a real failure that their suite is silently rotting.
func TestAFailedRunIsToldToFixTheTestsFirst(t *testing.T) {
	report, incomplete := review([]state{
		{what: "a batch of more than one event", site: "bus/testinv_test.go:12"},
	}, "", false, false)

	if !incomplete {
		t.Errorf("an unreached state stopped being reported because tests failed")
	}
	if strings.Contains(report, "The tests still pass") {
		t.Errorf("a failing run was told its tests pass:\n%s", report)
	}
	if !strings.Contains(report, "fix those first") {
		t.Errorf("a failing run was not pointed at the real failures:\n%s", report)
	}
}

func TestACompleteInventoryIsSilent(t *testing.T) {
	report, incomplete := review([]state{
		{what: "a redelivery", site: "bus/testinv_test.go:15", reached: true},
	}, "", true, false)
	if incomplete {
		t.Errorf("a complete inventory failed the run")
	}
	if report != "" {
		t.Errorf("a complete inventory printed %q, want silence", report)
	}
}

func TestAnEmptyInventoryCostsNothing(t *testing.T) {
	report, incomplete := review(nil, "", true, false)
	if incomplete || report != "" {
		t.Errorf("an empty inventory produced (%q, %v), want silent and passing", report, incomplete)
	}
}

// A filtered run cannot reach the whole inventory, so it must not fail — but it
// must say that it checked nothing, or the silence reads as a pass.
func TestAFilteredRunSaysItCheckedNothing(t *testing.T) {
	report, incomplete := review([]state{
		{what: "a batch of more than one event", site: "bus/testinv_test.go:12"},
	}, "this run is filtered by -run TestFoo", true, false)

	if incomplete {
		t.Errorf("a filtered run failed on an unreached state")
	}
	if !strings.Contains(report, "not checking 1 cataloged state") ||
		!strings.Contains(report, "-run TestFoo") {
		t.Errorf("a filtered run did not report what it skipped:\n%s", report)
	}
}

// A mark may sit inside a test double that production code calls on a hot path,
// so the steady-state cost is the number that decides whether placing one is ever
// a judgement call. It is a plain atomic load once the state has been reached.
func BenchmarkReachedOnAnAlreadyReachedMark(b *testing.B) {
	m := newCatalog().add("a state already reached", "x_test.go:1")
	m.Reached()
	for b.Loop() {
		m.Reached()
	}
}

func TestVerboseListsTheWholeInventory(t *testing.T) {
	report, _ := review([]state{
		{what: "a batch of more than one event", site: "bus/testinv_test.go:12"},
		{what: "a redelivery", site: "bus/testinv_test.go:15", reached: true},
	}, "", true, true)

	if !strings.Contains(report, "[MISSING] a batch of more than one event") ||
		!strings.Contains(report, "[reached] a redelivery") {
		t.Errorf("verbose report does not list both states:\n%s", report)
	}
}
