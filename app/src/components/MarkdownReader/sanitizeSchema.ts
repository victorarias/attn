/**
 * Sanitize schema for raw HTML inside markdown documents (rehype-raw output).
 *
 * Derived from rehype-sanitize's GitHub-style `defaultSchema`, which already
 * covers the PR3 allowlist core (`details`/`summary` with `open` via the `*`
 * list, `kbd`, `sub`, `sup`, `br`, task-list `input[type=checkbox][disabled]`,
 * `language-*` class on `code`, GFM table `align`, footnote plumbing, and
 * id-clobbering with the `user-content-` prefix). Extended with plannotator's
 * remaining tags (`mark`, `small`, `abbr`, `video`/`source`/`picture`,
 * sectioning elements) and their media attributes — deliberately NO
 * `autoplay`, no `style` attribute, no event handlers, and `script`/`style`
 * elements are stripped with their content.
 *
 * Ordering contract: this runs BEFORE rehypeSourceAnchors/rehypeAlerts, so the
 * reader's own `data-block-id`/`data-source-line`/`data-alert-kind` attributes
 * never need whitelisting here — author HTML can't forge them (any incoming
 * data-* is stripped) and the real ones are stamped after sanitization.
 */

import { defaultSchema, type Options } from 'rehype-sanitize';

export const readerSanitizeSchema: Options = {
  ...defaultSchema,
  tagNames: [
    ...(defaultSchema.tagNames ?? []),
    'abbr',
    'small',
    'mark',
    'article',
    'aside',
    'header',
    'footer',
    'video',
  ],
  attributes: {
    ...defaultSchema.attributes,
    video: ['src', 'controls', 'poster', 'muted', 'loop', 'playsInline'],
    source: [...(defaultSchema.attributes?.source ?? []), 'src', 'srcSet', 'type', 'sizes'],
    img: [...(defaultSchema.attributes?.img ?? []), 'srcSet', 'sizes'],
  },
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
