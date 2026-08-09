/**
 * Sanitize schema for raw HTML inside markdown documents (rehype-raw output).
 * Extends rehype-sanitize's GitHub `defaultSchema`: no `style` attribute, no
 * event handlers, `script`/`style` stripped with their content.
 *
 * NO-NETWORK INVARIANT: every fetchable URL attribute is either gated by a
 * component renderer (img `src`, a `href` — resolveMarkdownTarget +
 * convertFileSrc) or absent from this schema. `srcSet`/`sizes` stay out because
 * hast-util-sanitize keys `protocols` by property name, so `srcSet` gets no
 * protocol check while browsers prefer it over the gated `src`; `video`,
 * `picture`, and `source` stay out for want of a renderer.
 *
 * Ordering contract: runs BEFORE rehypeSourceAnchors/rehypeAlerts, so the
 * reader's own `data-*` attributes are stamped after sanitization and are never
 * whitelisted here.
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
    // `file:` passes here; the img/a renderers still gate it (markdownLinks.ts).
    href: [...(defaultSchema.protocols?.href ?? []), 'file'],
    src: [...(defaultSchema.protocols?.src ?? []), 'file'],
  },
  // An unlisted tag is dropped but its children survive; <style> text must not.
  strip: [...(defaultSchema.strip ?? []), 'style'],
};
