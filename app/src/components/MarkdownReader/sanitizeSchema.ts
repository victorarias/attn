/**
 * Sanitize schema for raw HTML inside markdown documents (rehype-raw output).
 *
 * Derived from rehype-sanitize's GitHub-style `defaultSchema`, which already
 * covers the PR3 allowlist core (`details`/`summary` with `open` via the `*`
 * list, `kbd`, `sub`, `sup`, `br`, task-list `input[type=checkbox][disabled]`,
 * `language-*` class on `code`, GFM table `align`, footnote plumbing, and
 * id-clobbering with the `user-content-` prefix). Extended with plannotator's
 * remaining inline/sectioning tags (`mark`, `small`, `abbr`, article/aside/
 * header/footer) — deliberately NO `style` attribute, no event handlers, and
 * `script`/`style` elements are stripped with their content.
 *
 * NO-NETWORK INVARIANT: the reader never fetches the network for document
 * media. Every fetchable URL attribute must either be gated by a component
 * renderer (img `src`, a `href` — routed through resolveMarkdownTarget +
 * convertFileSrc) or be absent from this schema. That is why:
 * - `srcSet`/`sizes` are NOT allowed on `img`: hast-util-sanitize's
 *   `protocols` map is keyed by property name, so `srcSet` would get no
 *   protocol check at all, and browsers prefer srcset over the gated src —
 *   a remote srcset would silently exfiltrate (tracking pixel) past the gate.
 * - `video` is not whitelisted and defaultSchema's `picture`/`source` are
 *   removed: none of them has a component renderer, so a remote `poster`,
 *   `src`, or `source srcset` would fetch with no gate. Re-adding any of them
 *   requires a renderer applying the same local-only resolve + convertFileSrc
 *   gate the img renderer uses.
 *
 * Ordering contract: this runs BEFORE rehypeSourceAnchors/rehypeAlerts, so the
 * reader's own `data-block-id`/`data-source-line`/`data-alert-kind` attributes
 * never need whitelisting here — author HTML can't forge them (any incoming
 * data-* is stripped) and the real ones are stamped after sanitization.
 */

import { defaultSchema, type Options } from 'rehype-sanitize';

// Ungated media containers (see the no-network invariant above).
const REMOVED_DEFAULT_TAGS = new Set(['picture', 'source']);

export const readerSanitizeSchema: Options = {
  ...defaultSchema,
  tagNames: [
    ...(defaultSchema.tagNames ?? []).filter((tag) => !REMOVED_DEFAULT_TAGS.has(tag)),
    'abbr',
    'small',
    'mark',
    'article',
    'aside',
    'header',
    'footer',
  ],
  protocols: {
    ...defaultSchema.protocols,
    // `file:` targets stay allowed at this layer; the img/a component
    // renderers still gate them through resolveMarkdownTarget + the
    // safe-extension checks (markdownLinks.ts), same as markdown-syntax links.
    href: [...(defaultSchema.protocols?.href ?? []), 'file'],
    src: [...(defaultSchema.protocols?.src ?? []), 'file'],
  },
  // Default strips <script> with its content; <style> text must not leak into
  // prose either (an unlisted tag is dropped but its children survive).
  strip: [...(defaultSchema.strip ?? []), 'style'],
};
