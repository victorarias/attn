/**
 * Content hash for anchor records.
 *
 * The reader's re-render gate is plain string equality on `content` (see
 * MarkdownReaderBody's memo contract) — there is no hash there to reuse. This
 * util exists solely for `AnchorRecord.contentHash`: cheap enough to run once
 * per actual content change (the gate already tells callers when that is).
 *
 * FNV-1a over UTF-16 code units (`charCodeAt`, high/low bytes folded in) — no
 * TextEncoder allocation, deterministic across platforms.
 */

const FNV_OFFSET_BASIS = 0x811c9dc5;
const FNV_PRIME = 0x01000193;

/** 32-bit FNV-1a of `s`, as 8 lowercase hex chars. */
export function fnv1a32(s: string): string {
  let hash = FNV_OFFSET_BASIS;
  for (let i = 0; i < s.length; i++) {
    const code = s.charCodeAt(i);
    hash ^= code & 0xff;
    hash = Math.imul(hash, FNV_PRIME);
    hash ^= code >>> 8;
    hash = Math.imul(hash, FNV_PRIME);
  }
  return (hash >>> 0).toString(16).padStart(8, '0');
}
