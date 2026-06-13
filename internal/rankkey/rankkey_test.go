package rankkey

import (
	"strings"
	"testing"
)

// less reports the strict byte (lexicographic) ordering used by rank keys.
func less(a, b string) bool { return a < b }

// noTrailingMinDigit asserts the package invariant: a real key never ends in the
// minimum digit. Generated keys that violated this could compare equal-by-value
// to a shorter key, breaking byte-order == numeric-order.
func noTrailingMinDigit(t *testing.T, k string) {
	t.Helper()
	if k == "" {
		t.Fatalf("generated key is empty")
	}
	if k[len(k)-1] == digits[0] {
		t.Fatalf("key %q ends in the minimum digit %q (loses subdivision room)", k, string(digits[0]))
	}
}

func TestBetweenStrictOrdering(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
	}{
		{"min to max", "", ""},
		{"min to key", "", "n"},
		{"key to max", "n", ""},
		{"adjacent single digits", "1", "2"},
		{"far single digits", "1", "z"},
		{"no single-digit room, multi-char a", "11", "12"},
		{"b is prefix-adjacent to a", "a", "ab"},
		{"long keys close together", "aaaa", "aaab"},
		{"low bound empty, tight high", "", "1"},
		{"high bound empty, high low", "z", ""},
		{"min digit high bound", "", "01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, err := Between(tt.a, tt.b)
			if err != nil {
				t.Fatalf("Between(%q,%q) errored: %v", tt.a, tt.b, err)
			}
			if tt.a != "" && !less(tt.a, k) {
				t.Fatalf("Between(%q,%q)=%q: not a < k", tt.a, tt.b, k)
			}
			if tt.b != "" && !less(k, tt.b) {
				t.Fatalf("Between(%q,%q)=%q: not k < b", tt.a, tt.b, k)
			}
			noTrailingMinDigit(t, k)
		})
	}
}

func TestBetweenErrorsOnEmptyInterval(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
	}{
		{"equal", "abc", "abc"},
		{"inverted", "b", "a"},
		{"inverted long", "aab", "aaa"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Between(tt.a, tt.b); err == nil {
				t.Fatalf("Between(%q,%q): expected error, got nil", tt.a, tt.b)
			}
		})
	}
}

// TestBetweenRepeatedInsertSamePairLeft inserts 100 times always taking the new
// key as the next upper bound (descending), the worst case for the left edge.
func TestBetweenRepeatedInsertSamePairLeft(t *testing.T) {
	lo, hi := "a", "b"
	prev := hi
	for i := 0; i < 100; i++ {
		k, err := Between(lo, prev)
		if err != nil {
			t.Fatalf("iter %d Between(%q,%q): %v", i, lo, prev, err)
		}
		if !less(lo, k) || !less(k, prev) {
			t.Fatalf("iter %d: not %q < %q < %q", i, lo, k, prev)
		}
		noTrailingMinDigit(t, k)
		prev = k
	}
}

// TestBetweenRepeatedInsertSamePairRight is the mirror: always take the new key
// as the next lower bound (ascending toward hi).
func TestBetweenRepeatedInsertSamePairRight(t *testing.T) {
	lo, hi := "a", "b"
	prev := lo
	for i := 0; i < 100; i++ {
		k, err := Between(prev, hi)
		if err != nil {
			t.Fatalf("iter %d Between(%q,%q): %v", i, prev, hi, err)
		}
		if !less(prev, k) || !less(k, hi) {
			t.Fatalf("iter %d: not %q < %q < %q", i, prev, k, hi)
		}
		noTrailingMinDigit(t, k)
		prev = k
	}
}

// TestBetweenRepeatedInsertMidpoint keeps splitting the interval in half by
// inserting between a fixed low and the most-recent key, then between that key
// and a fixed high, growing a sorted set and re-checking total order each time.
func TestBetweenRepeatedInsertMidpoint(t *testing.T) {
	// Maintain a sorted slice; repeatedly insert between a random-ish adjacent
	// pair (here: always the middle pair) and assert strict total order.
	keys := []string{"a", "z"}
	for i := 0; i < 100; i++ {
		mid := len(keys) / 2
		k, err := Between(keys[mid-1], keys[mid])
		if err != nil {
			t.Fatalf("iter %d Between(%q,%q): %v", i, keys[mid-1], keys[mid], err)
		}
		noTrailingMinDigit(t, k)
		// Splice k in at position mid.
		keys = append(keys, "")
		copy(keys[mid+1:], keys[mid:])
		keys[mid] = k
		assertStrictlySorted(t, keys)
	}
}

func assertStrictlySorted(t *testing.T, keys []string) {
	t.Helper()
	for i := 1; i < len(keys); i++ {
		if !less(keys[i-1], keys[i]) {
			t.Fatalf("not strictly sorted at %d: %q >= %q (full=%v)", i, keys[i-1], keys[i], keys)
		}
	}
}

func TestSeedMonotonicAndCanonical(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 5, 10, 35, 36, 37, 64, 100, 500} {
		t.Run("n="+itoa(n), func(t *testing.T) {
			keys := Seed(n)
			if len(keys) != max0(n) {
				t.Fatalf("Seed(%d) returned %d keys", n, len(keys))
			}
			assertStrictlySorted(t, keys)
			for _, k := range keys {
				noTrailingMinDigit(t, k)
			}
		})
	}
}

// TestBetweenWorksOnAdjacentSeedOutputs verifies the real use case: after a
// Seed, you can always insert between any adjacent pair (and between repeatedly).
func TestBetweenWorksOnAdjacentSeedOutputs(t *testing.T) {
	for _, n := range []int{2, 3, 5, 10, 36, 100} {
		keys := Seed(n)
		for i := 1; i < len(keys); i++ {
			lo, hi := keys[i-1], keys[i]
			k, err := Between(lo, hi)
			if err != nil {
				t.Fatalf("Seed(%d): Between adjacent %q,%q: %v", n, lo, hi, err)
			}
			if !less(lo, k) || !less(k, hi) {
				t.Fatalf("Seed(%d): not %q < %q < %q", n, lo, k, hi)
			}
			noTrailingMinDigit(t, k)
		}
		// Also insert below the first and above the last.
		first, err := Between("", keys[0])
		if err != nil || !less(first, keys[0]) {
			t.Fatalf("Seed(%d): Between(MIN, %q)=%q err=%v", n, keys[0], first, err)
		}
		last := After(keys[len(keys)-1])
		if !less(keys[len(keys)-1], last) {
			t.Fatalf("Seed(%d): After(%q)=%q not greater", n, keys[len(keys)-1], last)
		}
	}
}

func TestAfterMonotonic(t *testing.T) {
	prev := After("") // first key
	noTrailingMinDigit(t, prev)
	for i := 0; i < 100; i++ {
		k := After(prev)
		noTrailingMinDigit(t, k)
		if !less(prev, k) {
			t.Fatalf("iter %d: After(%q)=%q not greater", i, prev, k)
		}
		prev = k
	}
}

func TestAfterFirstKey(t *testing.T) {
	k := After("")
	if k == "" {
		t.Fatalf("After(\"\") returned empty")
	}
	// Must leave room both below (Between MIN) and above (After).
	if below, err := Between("", k); err != nil || !less(below, k) {
		t.Fatalf("no room below first key %q: %q err=%v", k, below, err)
	}
	if above := After(k); !less(k, above) {
		t.Fatalf("no room above first key %q: %q", k, above)
	}
}

// TestBruteForceBetweenAllShortPairs exhaustively checks Between over all pairs
// of short keys plus the MIN/MAX sentinels, the strongest correctness evidence.
func TestBruteForceBetweenAllShortPairs(t *testing.T) {
	// Build a corpus of short, canonical keys (no trailing min digit) of length
	// 1 and 2 over a reduced alphabet to keep the run fast but representative.
	alpha := []byte{digits[0], digits[1], digits[base/2], digits[base-1]}
	var corpus []string
	corpus = append(corpus, "") // sentinel
	for _, c0 := range alpha {
		if c0 == digits[0] {
			// length-1 key cannot be the min digit alone and remain canonical only
			// if it isn't trailing-min; "0" itself ends in min digit -> skip.
			continue
		}
		corpus = append(corpus, string(c0))
	}
	for _, c0 := range alpha {
		for _, c1 := range alpha {
			if c1 == digits[0] {
				continue // trailing min digit not allowed
			}
			corpus = append(corpus, string([]byte{c0, c1}))
		}
	}

	for _, a := range corpus {
		for _, b := range corpus {
			k, err := Between(a, b)
			emptyInterval := a != "" && b != "" && a >= b
			if emptyInterval {
				if err == nil {
					t.Fatalf("Between(%q,%q): expected error for empty interval", a, b)
				}
				continue
			}
			if err != nil {
				t.Fatalf("Between(%q,%q): unexpected error: %v", a, b, err)
			}
			if a != "" && !less(a, k) {
				t.Fatalf("Between(%q,%q)=%q: not a < k", a, b, k)
			}
			if b != "" && !less(k, b) {
				t.Fatalf("Between(%q,%q)=%q: not k < b", a, b, k)
			}
			noTrailingMinDigit(t, k)
		}
	}
}

// TestBetweenRandomInsertsStayStrictlyOrdered maintains a sorted set and, many
// times, inserts a key between a randomly chosen adjacent pair (including the
// MIN/MAX edges). After every insert the whole set must remain strictly sorted
// and canonical. This mirrors a long sequence of real reorders.
func TestBetweenRandomInsertsStayStrictlyOrdered(t *testing.T) {
	rng := newLCG(0x9e3779b9)
	keys := []string{After("")} // start with a single seeded key
	for i := 0; i < 2000; i++ {
		// Pick an insertion gap: index g in [0, len(keys)] where g==0 inserts at
		// the top (MIN..keys[0]) and g==len inserts at the bottom (keys[last]..MAX).
		g := int(rng() % uint32(len(keys)+1))
		lo, hi := "", ""
		if g > 0 {
			lo = keys[g-1]
		}
		if g < len(keys) {
			hi = keys[g]
		}
		k, err := Between(lo, hi)
		if err != nil {
			t.Fatalf("iter %d Between(%q,%q): %v", i, lo, hi, err)
		}
		noTrailingMinDigit(t, k)
		keys = append(keys, "")
		copy(keys[g+1:], keys[g:])
		keys[g] = k
		assertStrictlySorted(t, keys)
	}
	if len(keys) != 2001 {
		t.Fatalf("expected 2001 keys, got %d", len(keys))
	}
}

// newLCG returns a tiny deterministic pseudo-random source so the stress test is
// reproducible without importing math/rand.
func newLCG(seed uint32) func() uint32 {
	state := seed
	return func() uint32 {
		state = state*1664525 + 1013904223
		return state
	}
}

// --- tiny local helpers to avoid pulling strconv into the test for labels ---

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b strings.Builder
	var rev []byte
	for n > 0 {
		rev = append(rev, byte('0'+n%10))
		n /= 10
	}
	if neg {
		b.WriteByte('-')
	}
	for i := len(rev) - 1; i >= 0; i-- {
		b.WriteByte(rev[i])
	}
	return b.String()
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
