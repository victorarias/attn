import { isValidElement, memo, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react';
import type { HTMLAttributes, ReactNode, Ref, RefObject } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import { convertFileSrc } from '@tauri-apps/api/core';
import rehypeRaw from 'rehype-raw';
import rehypeSanitize from 'rehype-sanitize';
import remarkFrontmatter from 'remark-frontmatter';
import remarkGfm from 'remark-gfm';
import type { PluggableList } from 'unified';
import '@fontsource-variable/inter';
import { CodeRenderer } from '../Markdown';
import { CodeBlock } from './CodeBlock';
import { extractFrontmatter, type FrontmatterEntry } from './frontmatter';
import { ImageLightbox } from './ImageLightbox';
import {
  isSafeLocalMarkdownImageTarget,
  isSafeLocalMarkdownTarget,
  openMarkdownTarget,
  resolveMarkdownTarget,
  sanitizeLinkUrl,
} from './markdownLinks';
import rehypeAlerts, { type AlertKind } from './rehypeAlerts';
import rehypeHeadingSlugs from './rehypeHeadingSlugs';
import rehypeProseTransforms from './proseTransforms';
import rehypeSourceAnchors from './rehypeSourceAnchors';
import { readerSanitizeSchema } from './sanitizeSchema';
import { scrollToAnchor } from './scrollToAnchor';
import { AnnotationLayer } from './annotations/AnnotationLayer';
import { useAnnotations } from './annotations/useAnnotations';
import { tilePathBasename } from '../../utils/tilePresentation';
import './MarkdownReader.css';

// The document is parsed WITH remark-frontmatter (the yaml node stays in the
// tree and is never rendered), so remark positions already refer to raw-file
// lines — the correct anchor lineOffset is 0. Only a caller that strips
// frontmatter before parsing would pass extractFrontmatter().lineCount.
// Module-level plugin arrays (never re-created per render): react-markdown
// re-parses when the plugin array identity changes, so these MUST stay stable
// for the memoization contract to hold.
const remarkPlugins = [remarkGfm, remarkFrontmatter];
const rehypePlugins: PluggableList = [
  // Raw HTML first (turns `raw` nodes into real elements), then sanitize.
  // Anchors run AFTER sanitize so the anchoring data-* attributes never need
  // whitelisting (and author HTML can't forge them); anchors before alerts so
  // alert blockquotes keep bN-blockquote ids and line ranges that include the
  // marker line; heading slugs BEFORE prose transforms so ids come from the
  // pre-transform text (emoji shortcodes delete letters — `## Deploy :rocket:`
  // must keep the id `deploy-rocket` that authors link against); prose
  // transforms last (text-only mutation).
  rehypeRaw,
  [rehypeSanitize, readerSanitizeSchema],
  [rehypeSourceAnchors, { lineOffset: 0 }],
  rehypeAlerts,
  rehypeHeadingSlugs,
  rehypeProseTransforms,
];

// GitHub alert chrome: octicon paths (16x16, fill=currentColor) + titles.
// Module-level so every render shares the same element trees.
const ALERT_TITLES: Record<AlertKind, string> = {
  note: 'Note',
  tip: 'Tip',
  warning: 'Warning',
  caution: 'Caution',
  important: 'Important',
};

// octicons: info-16, light-bulb-16, alert-16, stop-16, report-16.
const ALERT_ICON_PATHS: Record<AlertKind, string> = {
  note: 'M0 8a8 8 0 1 1 16 0A8 8 0 0 1 0 8Zm8-6.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13ZM6.5 7.75A.75.75 0 0 1 7.25 7h1a.75.75 0 0 1 .75.75v2.75h.25a.75.75 0 0 1 0 1.5h-2a.75.75 0 0 1 0-1.5h.25v-2h-.25a.75.75 0 0 1-.75-.75ZM8 6a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z',
  tip: 'M8 1.5c-2.363 0-4 1.69-4 3.75 0 .984.424 1.625.984 2.304l.214.253c.223.264.47.556.673.848.284.411.537.896.621 1.49a.75.75 0 0 1-1.484.211c-.04-.282-.163-.547-.37-.847a8.456 8.456 0 0 0-.542-.68c-.084-.1-.173-.205-.268-.32C3.201 7.75 2.5 6.766 2.5 5.25 2.5 2.31 4.863 0 8 0s5.5 2.31 5.5 5.25c0 1.516-.701 2.5-1.328 3.259-.095.115-.184.22-.268.319-.207.245-.383.453-.541.681-.208.3-.33.565-.37.847a.751.751 0 0 1-1.485-.212c.084-.593.337-1.078.621-1.489.203-.292.45-.584.673-.848.075-.088.147-.173.213-.253.561-.679.985-1.32.985-2.304 0-2.06-1.637-3.75-4-3.75ZM5.75 12h4.5a.75.75 0 0 1 0 1.5h-4.5a.75.75 0 0 1 0-1.5ZM6 15.25a.75.75 0 0 1 .75-.75h2.5a.75.75 0 0 1 0 1.5h-2.5a.75.75 0 0 1-.75-.75Z',
  warning: 'M6.457 1.047c.659-1.234 2.427-1.234 3.086 0l6.082 11.378A1.75 1.75 0 0 1 14.082 15H1.918a1.75 1.75 0 0 1-1.543-2.575Zm1.763.707a.25.25 0 0 0-.44 0L1.698 13.132a.25.25 0 0 0 .22.368h12.164a.25.25 0 0 0 .22-.368Zm.53 3.996v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 11a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z',
  caution: 'M4.47.22A.749.749 0 0 1 5 0h6c.199 0 .389.079.53.22l4.25 4.25c.141.14.22.331.22.53v6a.749.749 0 0 1-.22.53l-4.25 4.25A.749.749 0 0 1 11 16H5a.749.749 0 0 1-.53-.22L.22 11.53A.749.749 0 0 1 0 11V5c0-.199.079-.389.22-.53Zm.84 1.28L1.5 5.31v5.38l3.81 3.81h5.38l3.81-3.81V5.31L10.69 1.5ZM8 4a.75.75 0 0 1 .75.75v3.5a.75.75 0 0 1-1.5 0v-3.5A.75.75 0 0 1 8 4Zm0 8a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z',
  important: 'M0 1.75C0 .784.784 0 1.75 0h12.5C15.216 0 16 .784 16 1.75v9.5A1.75 1.75 0 0 1 14.25 13H8.06l-2.573 2.573A1.458 1.458 0 0 1 3 14.543V13H1.75A1.75 1.75 0 0 1 0 11.25Zm1.75-.25a.25.25 0 0 0-.25.25v9.5c0 .138.112.25.25.25h2a.75.75 0 0 1 .75.75v2.19l2.72-2.72a.749.749 0 0 1 .53-.22h6.5a.25.25 0 0 0 .25-.25v-9.5a.25.25 0 0 0-.25-.25Zm7 2.25v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 9a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z',
};

function isAlertKind(value: unknown): value is AlertKind {
  return typeof value === 'string' && value in ALERT_TITLES;
}

// The rehypeSourceAnchors attributes, as react-markdown passes them to
// component renderers. Pulled off a block's props when the visual wrapper
// (not the semantic element) must carry the anchor.
const ANCHOR_ATTRS = ['data-block-id', 'data-source-line', 'data-source-line-end'] as const;

function splitAnchorProps<T extends object>(props: T): {
  anchorProps: Record<string, unknown>;
  rest: T;
} {
  const anchorProps: Record<string, unknown> = {};
  const rest = { ...props } as Record<string, unknown>;
  for (const attr of ANCHOR_ATTRS) {
    if (attr in rest) {
      anchorProps[attr] = rest[attr];
      delete rest[attr];
    }
  }
  return { anchorProps, rest: rest as T };
}

function textOf(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') {
    return String(node);
  }
  if (Array.isArray(node)) {
    return node.map(textOf).join('');
  }
  if (isValidElement<{ children?: ReactNode }>(node)) {
    return textOf(node.props.children);
  }
  return '';
}

function codeMeta(children: ReactNode): { text: string; language?: string; isMermaid: boolean } {
  let className = '';
  if (isValidElement<{ className?: string; children?: ReactNode }>(children)) {
    className = children.props.className ?? '';
  }
  const language = className.match(/language-([\w+-]+)/)?.[1];
  return {
    text: textOf(children).replace(/\n$/, ''),
    language,
    isMermaid: language === 'mermaid',
  };
}

function readerComponents(
  documentPath: string,
  allowLocalTargets: boolean,
  rootRef: { current: HTMLDivElement | null },
  onImageClick: (src: string, alt: string) => void,
): Components {
  return {
    // Heading ids come from the rehypeHeadingSlugs pass (pre-prose-transform
    // text) and flow through the default heading renderers as plain props.
    code: CodeRenderer,
    pre({ node: _node, children, ref: _ref, ...preProps }) {
      const { text, language, isMermaid } = codeMeta(children);
      if (isMermaid) {
        // CodeRenderer renders the MermaidDiagram; skip the codeblock chrome
        // but keep the anchoring data-* attributes on the wrapper.
        return <div {...(preProps as HTMLAttributes<HTMLDivElement>)}>{children}</div>;
      }
      return <CodeBlock code={text} language={language} preProps={preProps} />;
    },
    blockquote({ node: _node, children, ...props }) {
      const { 'data-alert-kind': alertKind, ...rest } = props as Record<string, unknown> &
        HTMLAttributes<HTMLElement>;
      if (!isAlertKind(alertKind)) {
        return <blockquote {...(props as HTMLAttributes<HTMLElement>)}>{children}</blockquote>;
      }
      // Alert wrapper keeps the anchoring data-* attributes (still in `rest`)
      // plus data-alert-kind for downstream tooling/tests.
      return (
        <div
          {...(rest as HTMLAttributes<HTMLDivElement>)}
          data-alert-kind={alertKind}
          className={`md-alert md-alert-${alertKind}`}
        >
          {/* data-md-chrome: React-added text with no hast counterpart — the
              anchoring DOM walker skips these subtrees (see anchoring/domRange). */}
          <div className="md-alert-title" data-md-chrome="1">
            <svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor" aria-hidden="true">
              <path d={ALERT_ICON_PATHS[alertKind]} />
            </svg>
            <span>{ALERT_TITLES[alertKind]}</span>
          </div>
          {children}
        </div>
      );
    },
    table({ node: _node, children, ...props }) {
      // Horizontal scroll is contained to the wrapper, and the wrapper is the
      // top-level block element, so the anchoring attributes move onto it
      // (never duplicated — anchor consumers count blocks by data-block-id).
      const { anchorProps, rest } = splitAnchorProps(props as HTMLAttributes<HTMLTableElement>);
      return (
        <div className="md-table-wrap" {...anchorProps}>
          <table {...rest}>{children}</table>
        </div>
      );
    },
    a({ node: _node, href, children }) {
      const sanitized = href ? sanitizeLinkUrl(href) : null;
      const target = sanitized ? resolveMarkdownTarget(documentPath, sanitized) : null;
      if (!target) {
        return <span>{children}</span>;
      }
      if (target.kind === 'local' && (!allowLocalTargets || !isSafeLocalMarkdownTarget(target.value))) {
        return <span title={`Blocked local target: ${target.value}`}>{children}</span>;
      }
      if (target.kind === 'fragment') {
        return (
          <a
            href={target.value}
            onClick={(event) => {
              event.preventDefault();
              scrollToAnchor(rootRef.current, target.value);
            }}
          >
            {children}
          </a>
        );
      }
      return (
        <a
          href={href}
          title={target.kind === 'local' ? target.value : undefined}
          onClick={(event) => {
            event.preventDefault();
            openMarkdownTarget(target);
          }}
        >
          {children}
        </a>
      );
    },
    img({ node: _node, src, alt, ...props }) {
      // Defense in depth for the no-network invariant: srcSet/sizes are not
      // in the sanitize allowlist, but if they ever slipped through, spreading
      // them would let a remote srcset override the gated local src (browsers
      // prefer srcset). Never spread them.
      const { srcSet: _srcSet, sizes: _sizes, ...safeProps } = props as Record<string, unknown> &
        HTMLAttributes<HTMLImageElement>;
      const imgSrc = typeof src === 'string' ? src : undefined;
      // resolveMarkdownTarget joins relative srcs against the doc directory
      // and percent-decodes the URL path (`%20` etc.), yielding an absolute
      // filesystem path for local targets. Remote (http/https) and unsafe
      // targets keep the blocked-image fallback: the reader never fetches
      // the network for document images.
      const target = imgSrc ? resolveMarkdownTarget(documentPath, imgSrc) : null;
      if (!target || target.kind !== 'local' || !allowLocalTargets || !isSafeLocalMarkdownImageTarget(target.value)) {
        return (
          <span className="md-reader-blocked-image" title={imgSrc} data-md-chrome="1">
            [blocked image: {alt || imgSrc || 'unknown source'}]
          </span>
        );
      }
      // convertFileSrc serves the file over Tauri's asset protocol (enabled
      // with $HOME scope in tauri.conf.json).
      const resolvedSrc = convertFileSrc(target.value);
      const altText = alt ?? tilePathBasename(target.value);
      return (
        <img
          {...safeProps}
          className="md-reader-image"
          src={resolvedSrc}
          alt={altText}
          title={target.value}
          loading="lazy"
          onClick={(event) => {
            event.stopPropagation();
            onImageClick(resolvedSrc, alt ?? '');
          }}
        />
      );
    },
  };
}

function FrontmatterCard({ entries }: { entries: FrontmatterEntry[] }) {
  if (entries.length === 0) {
    return null;
  }
  return (
    <div className="md-frontmatter">
      <div className="md-frontmatter-grid">
        {entries.map((entry) => (
          <div className="md-frontmatter-row" key={entry.key}>
            <span className="md-frontmatter-key">{entry.key}:</span>
            {Array.isArray(entry.value) ? (
              <span className="md-frontmatter-tags">
                {entry.value.map((item, index) => (
                  <span className="md-frontmatter-tag" key={`${item}-${index}`}>
                    {item}
                  </span>
                ))}
              </span>
            ) : (
              <span className="md-frontmatter-val">{entry.value}</span>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

/**
 * Imperative bridge the tile host (WorkspaceDockTile) uses for the PR6 send
 * flow. Kept imperative (a handle, not props) so the send button can flush
 * the debounced save and read the orphan set at click time without threading
 * annotation state up through the tile chrome on every keystroke.
 */
export interface MarkdownAnnotationsSendHandle {
  /** Flush any armed debounced draft save; resolves when it settles. */
  flushPendingSave(): Promise<void>;
  /** False while the daemon draft has not been loaded (initial hydrate in
      flight or failed-and-retrying). Sends must be refused then: the daemon
      would format its STORED draft, not the un-persisted local list. */
  isHydrated(): boolean;
  /** Empty local annotation state after a delivered send (daemon already
      tombstone-cleared); `generationFloor` seeds the local counter. */
  applyDeliveredClear(generationFloor: number): void;
  /** Ids the client currently shows as orphaned (non-persisted, client-derived). */
  getOrphanedIds(): string[];
}

export interface MarkdownReaderProps {
  /** Raw markdown file content (frontmatter included). */
  content: string;
  /** Absolute path of the document; relative link/image targets resolve against it. */
  path: string;
  /** False for remote workspaces: local file links/images render blocked. */
  allowLocalTargets?: boolean;
  /**
   * Enables the annotation layer (selection → comment/redline, daemon draft
   * persistence). Markdown TILES pass true; chat-surface readers never see it.
   */
  annotationsEnabled?: boolean;
  /** Reports the current annotation count (drives the tile header's Send N). */
  onAnnotationsCountChange?: (count: number) => void;
  /** PR6 send-flow handle (see MarkdownAnnotationsSendHandle). */
  annotationsSendRef?: Ref<MarkdownAnnotationsSendHandle | null>;
}

interface MarkdownReaderBodyProps {
  content: string;
  path: string;
  allowLocalTargets: boolean;
  rootRef: RefObject<HTMLDivElement | null>;
  onImageClick: (src: string, alt: string) => void;
}

/**
 * The rendered document subtree, behind the content re-render gate.
 *
 * GATE CONTRACT: this component re-renders only when the document identity
 * (`content`/`path`/`allowLocalTargets`) changes — `memo`'s shallow compare on
 * the content STRING is the "content hash": string equality is the degenerate
 * perfect hash, and the live-reload poller re-reads the file every 750ms, so
 * an unchanged file must produce zero re-renders here. `rootRef` and
 * `onImageClick` are referentially stable for the life of the reader (ref
 * object + useCallback([])), so they never defeat the compare.
 *
 * Why zero re-renders matters: the body creates fresh component closures per
 * render (the heading slugger's dedup map must reset per document render),
 * which React treats as new element types and remounts the whole rendered
 * tree — snapping user-toggled `<details>` shut, re-running async shiki
 * highlights, and wiping copy-button state. DOM-owned state survives no-op
 * reloads precisely because this subtree never re-renders for them; when the
 * content DID change, one full re-render (and a details reset) is accepted.
 * Parent state changes (e.g. the lightbox opening) re-render only the outer
 * shell, never this subtree.
 */
const MarkdownReaderBody = memo(function MarkdownReaderBody({
  content,
  path,
  allowLocalTargets,
  rootRef,
  onImageClick,
}: MarkdownReaderBodyProps) {
  const frontmatter = extractFrontmatter(content);
  // Fresh components per render is fine: this body only renders when the
  // document changed (memo gate), so the whole tree remounts anyway. Per-run
  // state (the heading slug dedup map) lives in the rehype passes, which
  // react-markdown re-runs per parse.
  const components = readerComponents(path, allowLocalTargets, rootRef, onImageClick);

  return (
    <article className="md-reader-card">
      <FrontmatterCard entries={frontmatter.entries} />
      <ReactMarkdown remarkPlugins={remarkPlugins} rehypePlugins={rehypePlugins} components={components}>
        {content}
      </ReactMarkdown>
    </article>
  );
});

/**
 * Document reader for markdown tiles: plannotator typography on a centered
 * card, source-line anchoring (data-block-id/data-source-line), syntax
 * highlighting with a hover copy button, GitHub heading slugs, safe link
 * routing, sanitized raw HTML, inline local images with a lightbox, and a
 * frontmatter metadata card. Shared chat-style surfaces keep using the plain
 * `Markdown` component.
 *
 * State (the lightbox) lives here, OUTSIDE the memoized body, so opening or
 * closing it never re-renders the document subtree (see MarkdownReaderBody's
 * gate contract).
 */
export const MarkdownReader = memo(function MarkdownReader({
  content,
  path,
  allowLocalTargets = true,
  annotationsEnabled = false,
  onAnnotationsCountChange,
  annotationsSendRef,
}: MarkdownReaderProps) {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const [lightbox, setLightbox] = useState<{ src: string; alt: string } | null>(null);
  // Stable identities: these are props of the memoized body and must never
  // change across renders (see the gate contract above).
  const handleImageClick = useCallback((src: string, alt: string) => {
    setLightbox({ src, alt });
  }, []);
  const handleLightboxClose = useCallback(() => {
    setLightbox(null);
  }, []);
  // Annotation engine: lives OUTSIDE the memoized body so its content-keyed
  // effect fires exactly when the body remounted — the same contract as the
  // re-render gate. Disabled (no listeners, no paints, no daemon traffic) for
  // chat-surface readers. The annotation UI (AnnotationLayer: toolbar/
  // popover/picker/sidebar) consumes this API.
  const annotationsApi = useAnnotations({ rootRef, content, path, enabled: annotationsEnabled });

  // Latest-api ref: the send handle below must read call-time state (the
  // api's memo identity changes whenever annotations change).
  const annotationsApiRef = useRef(annotationsApi);
  annotationsApiRef.current = annotationsApi;

  // Tile-header count bridge. Reports 0 on unmount so a tile whose reader
  // disappears (file emptied / errored) never shows a stale Send N.
  useEffect(() => {
    onAnnotationsCountChange?.(annotationsApi.annotations.length);
  }, [annotationsApi.annotations, onAnnotationsCountChange]);
  useEffect(() => {
    return () => {
      onAnnotationsCountChange?.(0);
    };
  }, [onAnnotationsCountChange]);

  useImperativeHandle(annotationsSendRef, () => ({
    flushPendingSave: () => annotationsApiRef.current.flushPendingSave(),
    isHydrated: () => annotationsApiRef.current.isHydrated(),
    applyDeliveredClear: (generationFloor: number) =>
      annotationsApiRef.current.applyDeliveredClear(generationFloor),
    getOrphanedIds: () => Array.from(annotationsApiRef.current.orphans.keys()),
  }), []);

  return (
    <div
      className={`md-reader ${annotationsEnabled ? 'md-reader--annotating' : ''}`.trim()}
      ref={rootRef}
      // Focusable so a selection gesture can claim keyboard focus for
      // type-to-comment; WebKit does not move focus when clicking
      // non-focusable content, which would leave the terminal's hidden
      // input as document.activeElement (keys leak to the shell and the
      // toolbar's editable-element guard blocks).
      tabIndex={annotationsEnabled ? -1 : undefined}
    >
      <div className="md-reader-doc">
        <div className="md-reader-wrap">
          <MarkdownReaderBody
            content={content}
            path={path}
            allowLocalTargets={allowLocalTargets}
            rootRef={rootRef}
            onImageClick={handleImageClick}
          />
        </div>
      </div>
      {annotationsEnabled && <AnnotationLayer api={annotationsApi} rootRef={rootRef} path={path} />}
      {lightbox && (
        <ImageLightbox src={lightbox.src} alt={lightbox.alt} onClose={handleLightboxClose} />
      )}
    </div>
  );
});
