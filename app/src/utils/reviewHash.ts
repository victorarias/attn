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
