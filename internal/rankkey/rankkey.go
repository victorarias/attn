// Package rankkey computes fractional rank keys: short strings that sort by
// byte (lexicographic) order and can always be subdivided, so a new key can be
// inserted strictly between any two existing keys without rewriting the others.
//
// This backs workspace ordering. A reorder becomes a single-row write (only the
// moved workspace's key changes), which is safe across the daemon's websocket
// fan-out to multiple clients.
//
// # Model
//
// A key is a base-36 fraction written without the leading "0.": the string
// "v8" denotes the number 0.v8 in base 36. The alphabet is "0".."9","a".."z"
// (digit value 0..35), so byte order on the strings matches numeric order on
// the fractions. Every key denotes a value strictly between 0 and 1.
//
// Invariant: a generated key never ends in the minimum digit '0'. A trailing
// '0' is a numeric no-op (0.v == 0.v0) that would create distinct strings with
// the same value and break the "byte order == numeric order" guarantee.
// Forbidding it keeps every key canonical and guarantees there is always room
// to insert a smaller key just below any key by appending more digits.
//
// The empty string "" is a sentinel, not a key: as the low bound it means -inf
// (MIN), as the high bound it means +inf (MAX).
package rankkey

import (
	"fmt"
	"strings"
)

// digits is the base-36 alphabet. digits[0] is the minimum digit, digits[base-1]
// the maximum.
const digits = "0123456789abcdefghijklmnopqrstuvwxyz"

const base = len(digits)

// digitVal returns the 0..base-1 value of a key digit.
func digitVal(b byte) int {
	return strings.IndexByte(digits, b)
}

// digitAt returns the digit value of s at position i, or the supplied default
// when s has no digit there (i past its end).
func digitAt(s string, i, def int) int {
	if i < len(s) {
		return digitVal(s[i])
	}
	return def
}

// Between returns a key K with a < K < b under byte (lexicographic) order.
//
// a == "" means the low bound is MIN (-inf); b == "" means the high bound is
// MAX (+inf). When the open interval (a, b) is non-empty Between always
// succeeds, extending precision (appending digits) when there is no single
// digit of room between a and b.
//
// It returns an error only when both bounds are real keys and a >= b, i.e. the
// interval is empty or inverted.
//
// Algorithm: walk one base-36 fraction digit at a time. While a and b share a
// digit, copy it. At the first position where they differ (da < db), if there
// is a digit strictly between them emit the midpoint and stop. Otherwise the
// digits are adjacent (db == da+1): emit da, which makes every later digit
// strictly less than b regardless of value, so b stops constraining us — from
// there we only have to stay strictly greater than a, achieved by descending
// into a's tail and bumping the first position with room.
func Between(a, b string) (string, error) {
	if a != "" && b != "" && a >= b {
		return "", fmt.Errorf("rankkey: empty interval: a=%q must be < b=%q", a, b)
	}

	// bMax tracks whether the high bound is open (MAX, or already provably
	// satisfied because we took a lower digit than b at some position).
	bMax := b == ""

	var out strings.Builder
	for i := 0; ; i++ {
		da := digitAt(a, i, 0)
		// When the high side is open, treat its digit as base ("one past max"),
		// which leaves the whole digit range available below it.
		db := base
		if !bMax {
			db = digitAt(b, i, base)
		}

		if da == db {
			// Shared digit (only possible while both bounds are real and still
			// agree at this position). Copy it and continue into finer precision.
			out.WriteByte(digits[da])
			continue
		}

		// da < db here (a < b guarantees the first differing digit has da < db).
		if db-da >= 2 {
			// Room for a digit strictly between da and db. The midpoint is at
			// least da+1 >= 1, so it is never the trailing minimum digit.
			out.WriteByte(digits[(da+db)/2])
			return out.String(), nil
		}

		// db == da+1: adjacent, no room at this position. Emit da. Any digits we
		// append after this are < the remainder of b, so b no longer constrains
		// us; we now only need to stay strictly greater than a.
		out.WriteByte(digits[da])
		bMax = true

		// Descend through the rest of a, copying its digits until we find a
		// position where we can bump above a's digit (leaving room below for
		// future inserts). a's digits are all < base, so such a position always
		// exists within one more digit when a is exhausted.
		for i++; ; i++ {
			da = digitAt(a, i, 0)
			if da+1 < base {
				// Bump to a value strictly above a's digit and below max. Choosing
				// the midpoint of (da, base) keeps the key central and never lands
				// on the minimum digit (it is >= da+1 >= 1).
				out.WriteByte(digits[(da+base)/2])
				return out.String(), nil
			}
			// a's digit is already the max here; we must keep matching it (the new
			// key has to stay above a) and look one position deeper.
			out.WriteByte(digits[da])
		}
	}
}

// Seed returns n keys k0 < k1 < ... < k(n-1) in strict byte order, evenly
// spaced with room to insert between any adjacent pair. It is the opening-order
// seed and the migration backfill. n <= 0 yields an empty slice.
//
// Keys are produced by recursive midpoint subdivision of the open interval
// (MIN, MAX), the same Between math used at runtime, so adjacent Seed outputs
// are always Between-insertable by construction.
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
		// low < high always holds by construction, so this is unreachable; panic
		// rather than emit a silently-broken seed.
		panic(fmt.Sprintf("rankkey: Seed subdivision failed between %q and %q: %v", low, high, err))
	}
	keys[mid] = k
	seedRange(keys, lo, mid, low, k)
	seedRange(keys, mid+1, hi, k, high)
}

// After returns a key strictly greater than max under byte order. max == ""
// yields the first key. It is the new-workspace append.
func After(max string) string {
	if max == "" {
		// First key: a central single digit leaves room on both sides.
		return string(digits[base/2])
	}
	k, _ := Between(max, "")
	return k
}
