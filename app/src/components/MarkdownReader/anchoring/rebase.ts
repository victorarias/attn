/**
 * rebaseAnchor — one fuzzy re-anchor per content change, then re-baseline.
 *
 * Runs only when the content hash changed (resolve handles the exact path).
 * Two tiers; the first tier with ≥1 candidate wins:
 *
 *  (a) exact search — every occurrence of `exact` across every block,
 *      re-attributed to the deepest stamped owner (avoids ul/li duplicate
 *      candidates), scored together as one pool. `blockId` is an ordinal
 *      position reassigned on every edit, so it is used only to prefer a
 *      same-block match when scores are close — never to accept a lone
 *      same-block hit unchecked. An inserted sibling that happens to inherit
 *      the anchor's old ordinal `blockId` and also contains the exact quote
 *      must still lose to the real match on prefix/suffix/proximity.
 *  (b) whitespace-normalized search — needle and haystacks collapsed with
 *      `\s+ → ' '`; the only lossy tier (rewrapped paragraphs).
 *
 * Candidates are scored by prefix/suffix similarity (Levenshtein over the
 * 32-char context windows) plus source-line proximity — a genuine unedited
 * match keeps near-identical context and wins on that alone, no identity
 * shortcut needed. The winning match is RE-BASELINED: the returned record is
 * rebuilt from scratch against `newContent` (fresh blockId/offsets/lines/
 * context/hash) so fuzz never compounds across successive edits. The
 * reported tier is `'same-block'` when the winner's block still carries the
 * anchor's ordinal `blockId`, `'document'` otherwise.
 */

import { buildAnchor, CONTEXT_CHARS } from './create';
import { extractBlockTexts, ownerBlockFor } from './extractBlocks';
import { fnv1a32 } from './hash';
import type { AnchorRecord, BlockText, RebaseResult, RebaseTier } from './types';

interface Candidate {
  /** Owning block (deepest stamped element containing the range). */
  block: BlockText;
  /** Raw offsets into `block.text`. */
  start: number;
  end: number;
}

/** Classic O(a·b) Levenshtein — inputs are ≤32-char context windows. */
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

/** All indexOf occurrences of `needle` in `haystack` (non-overlapping start scan). */
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
 * Whitespace-collapse `text`, keeping a map from each normalized offset back
 * to the raw offset it came from (runs of whitespace map to the run's first
 * raw index). `map[normalized.length]` maps the end sentinel.
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
      // Raw end = start of the char after the match; trim trailing raw
      // whitespace the collapsed final space may have swallowed.
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

function pickWinner(candidates: Candidate[], anchor: AnchorRecord): Candidate | 'ambiguous' {
  if (candidates.length === 1) {
    // The text itself matched exactly, nowhere else in the document — accept
    // unconditionally (identity of the containing block is irrelevant here).
    return candidates[0];
  }
  const scored = candidates
    .map((candidate) => ({ candidate, score: scoreCandidate(candidate, anchor) }))
    .sort((a, b) => b.score - a.score);
  const best = scored[0];
  const second = scored[1];
  if (best.score - second.score >= 0.05 || best.score >= CONFIDENCE_THRESHOLD) {
    return best.candidate;
  }
  return 'ambiguous';
}

/**
 * Re-anchor `anchor` against `newContent`. Returns a fully re-baselined
 * record on success (caller persists it) or an orphan — never a silent
 * wrong-text match.
 *
 * `preExtracted` (optional) must be `extractBlockTexts(newContent)` — pass it
 * when the caller already ran the pipeline for this content.
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
      // Whitespace-only match after normalization edge cases — treat as miss.
      continue;
    }
    return { state: 'rebased', anchor: rebased, tier: deriveTier(winner) };
  }

  return { state: 'orphan', reason: 'text-not-found' };
}
