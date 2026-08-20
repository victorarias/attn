// Garden search. Matching runs client-side over the pushed garden snapshot,
// which already carries every seed's body, so a query never costs a round trip
// and search has no loading state at all.
//
// One rule holds the design together: the query is the only filter state.
// Every affordance in the panel — the closed toggle, the scope and lens hints —
// writes into the query rather than keeping a flag beside it.
//
// See docs/plans/2026-08-20-garden-search.md.
import type { Seed } from '../hooks/useDaemonSocket';

/** Values `is:` accepts. Anything else is named back at the reader. */
export const IS_VALUES = ['open', 'closed', 'any', 'ready', 'blocked', 'dormant'] as const;
export type IsValue = (typeof IS_VALUES)[number];

export interface ParsedQuery {
  raw: string;
  /** Free-text terms, lowercased. Every term must hit, somewhere. */
  terms: string[];
  /** `is:` values, unioned. Empty means the default lens: open seeds only. */
  is: IsValue[];
  /** `tender:` values, unioned. */
  tenders: string[];
  /** Operator tokens with no such filter, verbatim — shown, never swallowed. */
  unknown: string[];
  /** The operator the reader is mid-way through typing, for the value hint. */
  partial: 'is' | 'tender' | null;
  /** Whether anything at all filters. */
  active: boolean;
  /**
   * Whether this is a search rather than a lens. Text or a tender searches the
   * scope's whole subtree; a bare `is:` value only changes which of the seeds
   * already on screen are shown. "Show me closed ones too" is not a search, and
   * making it flatten the tree would answer a question nobody asked.
   */
  searches: boolean;
}

const EMPTY: ParsedQuery = {
  raw: '',
  terms: [],
  is: [],
  tenders: [],
  unknown: [],
  partial: null,
  active: false,
  searches: false,
};

export function parseQuery(raw: string): ParsedQuery {
  const trimmed = raw.trim();
  if (!trimmed) return { ...EMPTY, raw };
  const tokens = trimmed.split(/\s+/);
  const q: ParsedQuery = { ...EMPTY, raw, terms: [], is: [], tenders: [], unknown: [] };
  tokens.forEach((token, i) => {
    const last = i === tokens.length - 1 && !/\s$/.test(raw);
    const op = /^(is|tender):(.*)$/i.exec(token);
    if (!op) {
      q.terms.push(token.toLowerCase());
      return;
    }
    const name = op[1].toLowerCase() as 'is' | 'tender';
    const value = op[2].toLowerCase();
    if (!value) {
      // `is:` with nothing after it filters nothing yet; it asks for the hint.
      if (last) q.partial = name;
      else q.unknown.push(token);
      return;
    }
    if (name === 'tender') {
      q.tenders.push(value);
      return;
    }
    if ((IS_VALUES as readonly string[]).includes(value)) {
      q.is.push(value as IsValue);
      return;
    }
    // A partially typed value is a hint, not an error, while it is being typed.
    if (last && IS_VALUES.some((v) => v.startsWith(value))) q.partial = 'is';
    else q.unknown.push(token);
  });
  q.searches = q.terms.length > 0 || q.tenders.length > 0 || q.unknown.length > 0;
  q.active = q.searches || q.is.length > 0;
  return q;
}

/** Toggle one operator token in a raw query, preserving the rest of it. */
export function toggleToken(raw: string, token: string): string {
  const tokens = raw.trim().split(/\s+/).filter(Boolean);
  const i = tokens.findIndex((t) => t.toLowerCase() === token.toLowerCase());
  if (i >= 0) tokens.splice(i, 1);
  else tokens.push(token);
  return tokens.join(' ');
}

export type Range = [number, number];

export interface SeedMatch {
  seed: Seed;
  /** Lower sorts first: id 0, title 10+position, tender 30, body 40. */
  rank: number;
  titleRanges: Range[];
  /** Only for a body-only hit — the line that says why this row is here. */
  snippet: { text: string; ranges: Range[] } | null;
  idHit: boolean;
}

/**
 * One seed, ready to be matched against.
 *
 * The lowercased text is built once per snapshot instead of once per
 * keystroke, which is what keeps the cost of a keystroke the cost of the scan.
 * Over a 1000-seed garden of delegation briefs the lowercasing is 0.7ms and
 * the scan that answers the query is 0.7ms, so folding the first into the
 * second would double every keystroke for nothing. Receipts, both engines, in
 * gardenSearch.bench.ts.
 */
export interface SearchEntry {
  seed: Seed;
  title: string;
  titleLower: string;
  bodyLower: string;
  idLower: string;
  tender: string;
  tenderLower: string;
  blockers: number;
  closed: boolean;
}

export interface IndexContext {
  /** Whoever holds the seed, already resolved to a display name. */
  tenderOf: (seed: Seed) => string;
  /** How many open seeds block it. */
  blockersOf: (seed: Seed) => number;
}

function isClosed(seed: Seed): boolean {
  return seed.status === 'harvested' || seed.status === 'withered';
}

export function buildIndex(seeds: Seed[], ctx: IndexContext): SearchEntry[] {
  return seeds.map((seed) => {
    const tender = ctx.tenderOf(seed);
    const title = seed.title ?? '';
    return {
      seed,
      title,
      titleLower: title.toLowerCase(),
      bodyLower: (seed.body ?? '').toLowerCase(),
      idLower: (seed.id ?? '').toLowerCase(),
      tender,
      tenderLower: tender.toLowerCase(),
      blockers: ctx.blockersOf(seed),
      closed: isClosed(seed),
    };
  });
}

/**
 * Does the status lens admit this seed.
 *
 * No `is:` value is itself a lens — the default one. The garden grows without
 * bound and most of what it holds is done, so closed seeds stay out until a
 * token asks for them.
 */
export function satisfiesLens(entry: SearchEntry, is: IsValue[]): boolean {
  if (is.length === 0) return !entry.closed;
  return is.some((value) => satisfies(value, entry));
}

function satisfies(value: IsValue, entry: SearchEntry): boolean {
  switch (value) {
    case 'any':
      return true;
    case 'open':
      return !entry.closed;
    case 'closed':
      return entry.closed;
    case 'ready':
      return Boolean(entry.seed.ready) && !entry.closed;
    case 'blocked':
      return entry.blockers > 0 && !entry.closed;
    case 'dormant':
      return entry.seed.status === 'dormant';
  }
}

/** Every occurrence of every term, merged so overlaps highlight once. */
function rangesOf(haystack: string, terms: string[]): Range[] {
  const lower = haystack.toLowerCase();
  const found: Range[] = [];
  for (const term of terms) {
    let from = lower.indexOf(term);
    while (from !== -1) {
      found.push([from, from + term.length]);
      from = lower.indexOf(term, from + term.length);
    }
  }
  if (found.length < 2) return found;
  found.sort((a, b) => a[0] - b[0]);
  const merged: Range[] = [found[0]];
  for (const range of found.slice(1)) {
    const tail = merged[merged.length - 1];
    if (range[0] <= tail[1]) tail[1] = Math.max(tail[1], range[1]);
    else merged.push(range);
  }
  return merged;
}

const SNIPPET_WIDTH = 132;

/**
 * A window of body text around the first hit, whitespace collapsed.
 *
 * The window is cut before the whitespace is collapsed: collapsing an 8KB
 * brief to show 132 characters of it is the kind of cost that only shows up
 * once a garden is big.
 */
function snippetOf(body: string, bodyLower: string, terms: string[]): { text: string; ranges: Range[] } | null {
  let at = -1;
  for (const term of terms) {
    const i = bodyLower.indexOf(term);
    if (i !== -1 && (at === -1 || i < at)) at = i;
  }
  if (at === -1) return null;
  const from = Math.max(0, at - Math.floor(SNIPPET_WIDTH / 3));
  const to = Math.min(body.length, from + SNIPPET_WIDTH + 40);
  const window = body.slice(from, to).replace(/\s+/g, ' ').trim();
  const text = (from > 0 ? '…' : '') + window + (to < body.length ? '…' : '');
  return { text, ranges: rangesOf(text, terms) };
}

/**
 * Does one seed answer the query, and how well.
 *
 * The status lens comes first because it is the cheapest test and the one that
 * removes the most rows: without an `is:` value, closed seeds are simply not in
 * the garden the reader is looking at.
 */
export function matchEntry(entry: SearchEntry, q: ParsedQuery): SeedMatch | null {
  if (q.unknown.length > 0) return null;
  if (!satisfiesLens(entry, q.is)) return null;

  if (q.tenders.length > 0) {
    const lower = entry.tenderLower;
    if (!lower || !q.tenders.some((name) => lower.includes(name))) return null;
  }

  if (q.terms.length === 0) {
    return { seed: entry.seed, rank: 20, titleRanges: [], snippet: null, idHit: false };
  }

  for (const term of q.terms) {
    const hit =
      entry.titleLower.includes(term) ||
      entry.idLower.includes(term) ||
      entry.tenderLower.includes(term) ||
      entry.bodyLower.includes(term);
    if (!hit) return null;
  }

  const idHit = q.terms.every((term) => entry.idLower.includes(term));
  const titleHit = q.terms.every((term) => entry.titleLower.includes(term));
  const tenderHit = q.terms.every((term) => entry.tenderLower.includes(term));

  let rank = 40;
  if (idHit) rank = 0;
  else if (titleHit) rank = 10 + Math.min(...q.terms.map((term) => entry.titleLower.indexOf(term)));
  else if (tenderHit) rank = 30;

  return {
    seed: entry.seed,
    rank,
    titleRanges: titleHit || rank === 0 ? rangesOf(entry.title, q.terms) : [],
    // The snippet earns its line only when the title does not already show why
    // the row matched.
    snippet: rank === 40 ? snippetOf(entry.seed.body ?? '', entry.bodyLower, q.terms) : null,
    idHit,
  };
}

/**
 * Every entry in `pool` that answers the query, best first.
 *
 * Ties keep the pool's own order, which is the snapshot's newest-first — so a
 * query never reshuffles rows that are equally good answers.
 */
export function searchGarden(pool: SearchEntry[], q: ParsedQuery): SeedMatch[] {
  const matches: Array<{ match: SeedMatch; i: number }> = [];
  pool.forEach((entry, i) => {
    const match = matchEntry(entry, q);
    if (match) matches.push({ match, i });
  });
  return matches
    .sort((a, b) => a.match.rank - b.match.rank || a.i - b.i)
    .map((entry) => entry.match);
}

/** Split a string into its highlighted and plain runs, in order. */
export function splitRanges(text: string, ranges: Range[]): Array<{ text: string; hit: boolean }> {
  if (ranges.length === 0) return [{ text, hit: false }];
  const parts: Array<{ text: string; hit: boolean }> = [];
  let at = 0;
  for (const [from, to] of ranges) {
    if (from > at) parts.push({ text: text.slice(at, from), hit: false });
    parts.push({ text: text.slice(from, to), hit: true });
    at = to;
  }
  if (at < text.length) parts.push({ text: text.slice(at), hit: false });
  return parts;
}
