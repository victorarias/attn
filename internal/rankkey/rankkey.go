// Package rankkey computes fractional rank keys: short strings that sort in
// byte order and can always be subdivided, so a key inserts strictly between
// any two others without rewriting them (workspace ordering; a reorder is a
// single-row write). A key is a base-36 fraction in (0,1) written without the
// leading "0." over alphabet "0".."9","a".."z", so byte order matches numeric
// order.
//
// Invariant: a generated key never ends in '0' — a trailing '0' is a numeric
// no-op (0.v == 0.v0) that would break "byte order == numeric order". The
// empty string "" is a sentinel, not a key: MIN as low bound, MAX as high.
package rankkey

import (
	"fmt"
	"strings"
)

// digits is the base-36 alphabet, minimum digit first.
const digits = "0123456789abcdefghijklmnopqrstuvwxyz"

const base = len(digits)

// digitVal returns the 0..base-1 value of a key digit.
func digitVal(b byte) int {
	return strings.IndexByte(digits, b)
}

// digitAt returns the digit value of s at position i, or def past its end.
func digitAt(s string, i, def int) int {
	if i < len(s) {
		return digitVal(s[i])
	}
	return def
}

// Between returns a key K with a < K < b under byte order. a == "" means MIN;
// b == "" means MAX. When the open interval (a, b) is non-empty it always
// succeeds, appending digits when no single digit of room exists; it errors
// only when both bounds are real keys and a >= b.
func Between(a, b string) (string, error) {
	if a != "" && b != "" && a >= b {
		return "", fmt.Errorf("rankkey: empty interval: a=%q must be < b=%q", a, b)
	}

	// bMax: the high bound is open (MAX, or already provably satisfied because
	// a lower digit than b's was taken at some position).
	bMax := b == ""

	var out strings.Builder
	for i := 0; ; i++ {
		da := digitAt(a, i, 0)
		// An open high side reads as base ("one past max"): the whole digit
		// range below it is available.
		db := base
		if !bMax {
			db = digitAt(b, i, base)
		}

		if da == db {
			out.WriteByte(digits[da])
			continue
		}

		// da < db (a < b guarantees it at the first differing digit).
		if db-da >= 2 {
			// The midpoint is >= da+1 >= 1, never the trailing minimum digit.
			out.WriteByte(digits[(da+db)/2])
			return out.String(), nil
		}

		// db == da+1: adjacent. Emitting da makes every later digit < b's
		// remainder, so b stops constraining; only "stay strictly above a" is left.
		out.WriteByte(digits[da])
		bMax = true

		// Descend a's tail to the first position with room to bump above its digit.
		for i++; ; i++ {
			da = digitAt(a, i, 0)
			if da+1 < base {
				// Midpoint of (da, base): central, and >= da+1 >= 1 so never the
				// minimum digit.
				out.WriteByte(digits[(da+base)/2])
				return out.String(), nil
			}
			// a's digit is max here; keep matching it and look deeper.
			out.WriteByte(digits[da])
		}
	}
}

// Seed returns n keys in strict byte order, evenly spaced (the opening-order
// seed and migration backfill; n <= 0 yields nil). Recursive midpoint
// subdivision uses the same Between math as runtime, so adjacent outputs are
// always Between-insertable by construction.
func Seed(n int) []string {
	if n <= 0 {
		return nil
	}
	keys := make([]string, n)
	seedRange(keys, 0, n, "", "")
	return keys
}

// seedRange fills keys[lo:hi) with strictly increasing keys inside the open
// interval (low, high), placing the median first and recursing on each side.
func seedRange(keys []string, lo, hi int, low, high string) {
	if lo >= hi {
		return
	}
	mid := (lo + hi) / 2
	k, err := Between(low, high)
	if err != nil {
		// Unreachable (low < high by construction); panic over a broken seed.
		panic(fmt.Sprintf("rankkey: Seed subdivision failed between %q and %q: %v", low, high, err))
	}
	keys[mid] = k
	seedRange(keys, lo, mid, low, k)
	seedRange(keys, mid+1, hi, k, high)
}

// After returns a key strictly greater than max under byte order; max == ""
// yields the first key. The new-workspace append.
func After(max string) string {
	if max == "" {
		// Central single digit leaves room on both sides.
		return string(digits[base/2])
	}
	k, _ := Between(max, "")
	return k
}
