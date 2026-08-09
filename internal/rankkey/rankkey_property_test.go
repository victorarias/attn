package rankkey

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// The example and brute-force tests in rankkey_test.go pin the shapes we thought
// of. This one explores the shapes we did not: rapid drives a random sequence of
// the four insertion moves a real reorder can make, and re-checks the package's
// whole contract after every single one. When it fails it shrinks the sequence
// to the shortest one that still fails, which is the point of the tool — a
// hand-rolled stress loop reports "iteration 1473 of a 1500-element list", which
// is a reproduction, not a diagnosis.
func TestBetweenPropertiesUnderRandomReorders(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// The list under test, always in the order the daemon would render it.
		keys := []string{After("")}

		// insert places k at index g and asserts the contract: strictly between its
		// neighbours, canonical, and the whole list still strictly ascending.
		insert := func(t *rapid.T, g int, lo, hi string) {
			k, err := Between(lo, hi)
			if err != nil {
				t.Fatalf("Between(%q, %q) on a non-empty interval: %v", lo, hi, err)
			}
			if lo != "" && !(lo < k) {
				t.Fatalf("Between(%q, %q) = %q is not above its low bound", lo, hi, k)
			}
			if hi != "" && !(k < hi) {
				t.Fatalf("Between(%q, %q) = %q is not below its high bound", lo, hi, k)
			}
			if strings.HasSuffix(k, string(digits[0])) {
				t.Fatalf("Between(%q, %q) = %q ends in the minimum digit; it has no room below it", lo, hi, k)
			}
			keys = append(keys, "")
			copy(keys[g+1:], keys[g:])
			keys[g] = k
		}

		t.Repeat(map[string]func(*rapid.T){
			// Drag something to the top of the list.
			"insert_front": func(t *rapid.T) {
				insert(t, 0, "", keys[0])
			},
			// Drag something to the bottom.
			"insert_back": func(t *rapid.T) {
				insert(t, len(keys), keys[len(keys)-1], "")
			},
			// Drop something between two existing rows — the move that forces the
			// key to grow, and the one an adversarial sequence can aim at.
			"insert_between": func(t *rapid.T) {
				if len(keys) < 2 {
					t.Skip("needs two keys to have a gap")
				}
				g := rapid.IntRange(1, len(keys)-1).Draw(t, "gap")
				insert(t, g, keys[g-1], keys[g])
			},
			// A new workspace appends past the current maximum.
			"append_new": func(t *rapid.T) {
				k := After(keys[len(keys)-1])
				if !(keys[len(keys)-1] < k) {
					t.Fatalf("After(%q) = %q is not above it", keys[len(keys)-1], k)
				}
				if strings.HasSuffix(k, string(digits[0])) {
					t.Fatalf("After(%q) = %q ends in the minimum digit", keys[len(keys)-1], k)
				}
				keys = append(keys, k)
			},
			// Runs before and after every action above: the list the daemon would
			// serve must be strictly ascending at all times, with every key canonical.
			"": func(t *rapid.T) {
				for i, k := range keys {
					if k == "" {
						t.Fatalf("key %d is the empty sentinel, which is not a key", i)
					}
					if strings.HasSuffix(k, string(digits[0])) {
						t.Fatalf("key %d (%q) ends in the minimum digit", i, k)
					}
					if i > 0 && !(keys[i-1] < k) {
						t.Fatalf("keys %d and %d are out of order: %q >= %q\nlist: %q", i-1, i, keys[i-1], k, keys)
					}
				}
			},
		})
	})
}

// Key length is the cost of fractional ranking: subdividing the same gap over
// and over must extend precision. This does not assert a bound — repeatedly
// halving one gap grows the key by design — but it does assert the shape of the
// growth: one insert adds at most one digit past the longer of its two bounds.
// A jump wider than that would mean the algorithm is spending precision it does
// not need, which is what makes keys unbounded in practice rather than in theory.
func TestBetweenGrowsKeysByAtMostOneDigit(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lo := rapid.StringOfN(rapid.SampledFrom([]rune(digits)), 0, 8, -1).Draw(t, "lo")
		hi := rapid.StringOfN(rapid.SampledFrom([]rune(digits)), 0, 8, -1).Draw(t, "hi")
		if lo != "" && hi != "" && lo >= hi {
			t.Skip("empty interval")
		}
		k, err := Between(lo, hi)
		if err != nil {
			t.Fatalf("Between(%q, %q): %v", lo, hi, err)
		}
		bound := max(len(lo), len(hi))
		if len(k) > bound+1 {
			t.Fatalf("Between(%q, %q) = %q is %d digits, more than one past its longest bound (%d)",
				lo, hi, k, len(k), bound)
		}
	})
}
