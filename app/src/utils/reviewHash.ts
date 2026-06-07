/**
 * Stable, fast content hash used by the diff review panel to detect when a
 * file's diff has changed since it was last viewed ("changed since viewed").
 *
 * This is intentionally a cheap non-cryptographic hash (djb2-style): it only
 * needs to be consistent and collision-resistant enough to notice that two
 * versions of the same file differ.
 */
export function hashContent(content: string): string {
  let hash = 0;
  for (let i = 0; i < content.length; i++) {
    const char = content.charCodeAt(i);
    hash = (hash << 5) - hash + char;
    hash = hash & hash; // Convert to 32bit integer
  }
  return hash.toString(16);
}

/**
 * Hash an (original, modified) diff pair into a single stable key.
 *
 * Frames the payload with the length of `original` so different ways of
 * splitting the same concatenation can't collide: `("ab", "c")` and
 * `("a", "bc")` both concatenate to `"abc"`, but the length prefix pins the
 * boundary so their inputs differ. Plain `original + modified` is ambiguous.
 */
export function hashDiffContent(original: string, modified: string): string {
  return hashContent(`${original.length}:${original}${modified}`);
}
