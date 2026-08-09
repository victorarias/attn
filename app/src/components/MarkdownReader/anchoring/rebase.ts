/**
 * One fuzzy re-anchor per content change, then re-baseline. Runs only when the
 * content hash changed. Two tiers, the first with ≥1 candidate winning:
 * (a) every occurrence of `exact` across all blocks, re-attributed to the
 * deepest stamped owner and scored as one pool; (b) whitespace-normalized
 * search (`\s+ → ' '`), the only lossy tier.
 *
 * `blockId` is an ordinal reassigned on every edit, so it never accepts a
 * candidate on its own. Scoring is Levenshtein similarity over the 32-char
 * context windows plus source-line proximity; the winner must clear a
 * confidence floor AND lead the runner-up, so indistinguishable copies orphan
 * as ambiguous rather than paint whichever sorts first. It is then
 * RE-BASELINED against `newContent`, so fuzz never compounds across edits.
 */

import { buildAnchor, CONTEXT_CHARS } from './create';
import { extractBlockTexts, ownerBlockFor } from './extractBlocks';
import { fnv1a32 } from './hash';
import type { AnchorRecord, BlockText, RebaseResult, RebaseTier } from './types';

interface Candidate {
  /** Owning block (deepest stamped element containing the range). */
  block: BlockText;
  start: number;
  end: number;
}

function levenshtein(a: string, b: string): number {
  if (a === b) {
    return 0;
  }
  if (a.length === 0 || b.length === 0) {
    return Math.max(a.length, b.length);
  }
  let prev = Array.from({ length: b.length + 1 }, (_, i) => i);
  let curr = new Array<number>(b.length + 1);
  for (let i = 1; i <= a.length; i++) {
    curr[0] = i;
    for (let j = 1; j <= b.length; j++) {
      const cost = a.charCodeAt(i - 1) === b.charCodeAt(j - 1) ? 0 : 1;
      curr[j] = Math.min(curr[j - 1] + 1, prev[j] + 1, prev[j - 1] + cost);
    }
    [prev, curr] = [curr, prev];
  }
  return prev[b.length];
}

function similarity(a: string, b: string): number {
  const max = Math.max(a.length, b.length);
  if (max === 0) {
    return 1;
  }
  return 1 - levenshtein(a, b) / max;
}

function scoreCandidate(candidate: Candidate, anchor: AnchorRecord): number {
  const { block, start, end } = candidate;
  const prefix = block.text.slice(Math.max(0, start - CONTEXT_CHARS), start);
  const suffix = block.text.slice(end, end + CONTEXT_CHARS);
  const proximity = 1 / (1 + Math.abs(block.startLine - anchor.startLine) / 20);
  return (
    0.4 * similarity(prefix, anchor.prefix) +
    0.4 * similarity(suffix, anchor.suffix) +
    0.2 * proximity
  );
}

function occurrences(haystack: string, needle: string): number[] {
  const out: number[] = [];
  if (needle.length === 0) {
    return out;
  }
  let from = 0;
  for (;;) {
    const at = haystack.indexOf(needle, from);
    if (at === -1) {
      return out;
    }
    out.push(at);
    from = at + 1;
  }
}

/** Re-attribute to the deepest owner and dedupe by (ownerBlockId, ownerStart). */
function dedupeToOwners(blocks: BlockText[], raw: Candidate[]): Candidate[] {
  const seen = new Set<string>();
  const out: Candidate[] = [];
  for (const candidate of raw) {
    const owner = ownerBlockFor(blocks, candidate.block.blockId, candidate.start, candidate.end);
    const key = `${owner.block.blockId}\0${owner.start}`;
    if (!seen.has(key)) {
      seen.add(key);
      out.push({ block: owner.block, start: owner.start, end: owner.end });
    }
  }
  return out;
}

function documentCandidates(blocks: BlockText[], anchor: AnchorRecord): Candidate[] {
  const raw: Candidate[] = [];
  for (const block of blocks) {
    for (const start of occurrences(block.text, anchor.exact)) {
      raw.push({ block, start, end: start + anchor.exact.length });
    }
  }
  return dedupeToOwners(blocks, raw);
}

/**
 * Whitespace-collapse `text`, mapping each normalized offset back to its raw
 * one (a whitespace run maps to its first index); `map[len]` is the sentinel.
 */
function normalizeWithMap(text: string): { normalized: string; map: number[] } {
  let normalized = '';
  const map: number[] = [];
  let inWhitespace = false;
  for (let i = 0; i < text.length; i++) {
    const char = text[i];
    if (/\s/.test(char)) {
      if (!inWhitespace) {
        map.push(i);
        normalized += ' ';
        inWhitespace = true;
      }
    } else {
      map.push(i);
      normalized += char;
      inWhitespace = false;
    }
  }
  map.push(text.length);
  return { normalized, map };
}

function normalizedCandidates(blocks: BlockText[], anchor: AnchorRecord): Candidate[] {
  const needle = anchor.exact.replace(/\s+/g, ' ');
  if (needle.trim() === '') {
    return [];
  }
  const raw: Candidate[] = [];
  for (const block of blocks) {
    const { normalized, map } = normalizeWithMap(block.text);
    for (const at of occurrences(normalized, needle)) {
      const start = map[at];
      // Trim trailing raw whitespace the collapsed final space swallowed.
      let end = map[at + needle.length];
      while (end > start && /\s/.test(block.text[end - 1]) && !/\s$/.test(needle)) {
        end--;
      }
      raw.push({ block, start, end });
    }
  }
  return dedupeToOwners(blocks, raw);
}

const CONFIDENCE_THRESHOLD = 0.5;
// A near-tie means indistinguishable copies, where sort order would be a
// silent wrong-paint. At proximity weight 0.2 this margin needs ~10+ lines.
const AMBIGUITY_MARGIN = 0.05;

function pickWinner(candidates: Candidate[], anchor: AnchorRecord): Candidate | 'ambiguous' {
  if (candidates.length === 1) {
    // Matched nowhere else in the document: accept regardless of block.
    return candidates[0];
  }
  const scored = candidates
    .map((candidate) => ({ candidate, score: scoreCandidate(candidate, anchor) }))
    .sort((a, b) => b.score - a.score);
  const best = scored[0];
  const second = scored[1];
  if (best.score >= CONFIDENCE_THRESHOLD && best.score - second.score >= AMBIGUITY_MARGIN) {
    return best.candidate;
  }
  return 'ambiguous';
}

/**
 * Re-anchor against `newContent`: a re-baselined record the caller persists,
 * or an orphan — never a silent wrong-text match. `preExtracted`, when given,
 * must be `extractBlockTexts(newContent)`.
 */
export function rebaseAnchor(
  anchor: AnchorRecord,
  newContent: string,
  preExtracted?: BlockText[],
): RebaseResult {
  const blocks = preExtracted ?? extractBlockTexts(newContent);
  const contentHash = fnv1a32(newContent);

  const passes: Array<{ candidates: Candidate[]; deriveTier: (winner: Candidate) => RebaseTier }> = [
    {
      candidates: documentCandidates(blocks, anchor),
      deriveTier: (winner) => (winner.block.blockId === anchor.blockId ? 'same-block' : 'document'),
    },
    { candidates: normalizedCandidates(blocks, anchor), deriveTier: () => 'normalized' },
  ];

  for (const { candidates, deriveTier } of passes) {
    if (candidates.length === 0) {
      continue;
    }
    const winner = pickWinner(candidates, anchor);
    if (winner === 'ambiguous') {
      return { state: 'orphan', reason: 'ambiguous' };
    }
    const rebased = buildAnchor(
      blocks,
      contentHash,
      winner.block.blockId,
      winner.start,
      winner.end,
    );
    if (!rebased) {
      continue;
    }
    return { state: 'rebased', anchor: rebased, tier: deriveTier(winner) };
  }

  return { state: 'orphan', reason: 'text-not-found' };
}
