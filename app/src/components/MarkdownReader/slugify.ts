/**
 * GitHub-style heading slugs, ported from plannotator's `slugifyHeading`
 * (packages/ui/utils/slugify.ts) — unicode-preserving, unlike a naive
 * `[^\w]` strip which destroys non-ASCII headings.
 */
export function slugifyHeading(text: string): string {
  return text
    .toLowerCase()
    .replace(/\[\[([^\]]+)\]\]/g, '$1') // [[wiki]] → wiki
    .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1') // [label](url) → label
    .replace(/[*_`~]/g, '') // strip emphasis/code markers
    .replace(/[^\p{L}\p{N}]+/gu, '-') // non letter/number runs → '-'
    .replace(/^-+|-+$/g, ''); // trim hyphens
}

/**
 * Per-document dedup: the first occurrence keeps the bare slug, later
 * duplicates get `-1`, `-2`, … An empty slug yields no id (undefined).
 * Create a fresh slugger per document render.
 */
export function createSlugger(): (text: string) => string | undefined {
  const counts = new Map<string, number>();
  return (text: string) => {
    const base = slugifyHeading(text);
    if (!base) {
      return undefined;
    }
    const count = counts.get(base) ?? 0;
    counts.set(base, count + 1);
    return count === 0 ? base : `${base}-${count}`;
  };
}
