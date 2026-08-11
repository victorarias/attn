// Package prose is the deterministic layer of attn's prose density gate: it
// reads Markdown, finds the spans that are actually prose, and reports what a
// writer can act on.
//
// The output is always a [Finding] — a span, the rule, and the objection.
// Never a score. See
// docs/plans/2026-08-10-prose-density-gate.md for why.
//
// Every number the rules compare against lives in [Thresholds], each with the
// measurement that set it.
package prose
