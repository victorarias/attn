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

// Parsed WITH remark-frontmatter, so remark positions already refer to raw-file
// lines and the anchor lineOffset is 0. These arrays MUST stay module-level:
// react-markdown re-parses whenever the plugin array identity changes.
const remarkPlugins = [remarkGfm, remarkFrontmatter];
const rehypePlugins: PluggableList = [
  // Order is load-bearing: raw HTML, then sanitize; anchors after sanitize so
  // data-* never needs whitelisting and author HTML cannot forge it; anchors
  // before alerts so blockquotes keep their ids and marker-inclusive ranges;
  // heading slugs before prose transforms so ids come from pre-transform text.
  rehypeRaw,
  [rehypeSanitize, readerSanitizeSchema],
  [rehypeSourceAnchors, { lineOffset: 0 }],
  rehypeAlerts,
  rehypeHeadingSlugs,
  rehypeProseTransforms,
];

// GitHub alert chrome: octicon paths (16x16, fill=currentColor) + titles.
const ALERT_TITLES: Record<AlertKind, string> = {
  note: 'Note',
  tip: 'Tip',
  warning: 'Warning',
  caution: 'Caution',
  important: 'Important',
};

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

// rehypeSourceAnchors attributes, pulled off a block's props when the visual
// wrapper rather than the semantic element must carry the anchor.
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
    code: CodeRenderer,
    pre({ node: _node, children, ref: _ref, ...preProps }) {
      const { text, language, isMermaid } = codeMeta(children);
      if (isMermaid) {
        // CodeRenderer draws the diagram; keep the anchor attrs on the wrapper.
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
      // The wrapper keeps the anchor attributes (in `rest`) plus the kind.
      return (
        <div
          {...(rest as HTMLAttributes<HTMLDivElement>)}
          data-alert-kind={alertKind}
          className={`md-alert md-alert-${alertKind}`}
        >
          {/* data-md-chrome: React text with no hast counterpart; the anchoring
              DOM walker skips these subtrees. */}
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
      // The scroll wrapper is the top-level block element, so it takes the
      // anchor attributes — never duplicated, consumers count data-block-id.
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
      // Never spread srcSet/sizes: a remote srcset would override the gated
      // local src (browsers prefer srcset) and break the no-network invariant.
      const { srcSet: _srcSet, sizes: _sizes, ...safeProps } = props as Record<string, unknown> &
        HTMLAttributes<HTMLImageElement>;
      const imgSrc = typeof src === 'string' ? src : undefined;
      // Local targets resolve to an absolute path; remote and unsafe ones keep
      // the blocked-image fallback — the reader never fetches the network.
      const target = imgSrc ? resolveMarkdownTarget(documentPath, imgSrc) : null;
      if (!target || target.kind !== 'local' || !allowLocalTargets || !isSafeLocalMarkdownImageTarget(target.value)) {
        return (
          <span className="md-reader-blocked-image" title={imgSrc} data-md-chrome="1">
            [blocked image: {alt || imgSrc || 'unknown source'}]
          </span>
        );
      }
      // Tauri's asset protocol, scoped to $HOME in tauri.conf.json.
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
 * Imperative bridge for the tile host's send flow: a handle, not props, so the
 * send button reads call-time state without re-rendering the tile per keystroke.
 */
export interface MarkdownAnnotationsSendHandle {
  /** Flush any armed debounced draft save; resolves when it settles. */
  flushPendingSave(): Promise<void>;
  /** False until the daemon draft loaded. Sends must be refused then: the
      daemon would format its STORED draft, not the local list. */
  isHydrated(): boolean;
  /** Empty local state after a delivered send; the floor seeds the counter. */
  applyDeliveredClear(generationFloor: number): void;
  /** Ids the client currently shows as orphaned (non-persisted, client-derived). */
  getOrphanedIds(): string[];
}

export interface MarkdownReaderProps {
  /** Raw markdown file content (frontmatter included). */
  content: string;
  /** Absolute path; relative link/image targets resolve against it. */
  path: string;
  /** False for remote workspaces: local file links/images render blocked. */
  allowLocalTargets?: boolean;
  /** Markdown TILES pass true; chat-surface readers never see the layer. */
  annotationsEnabled?: boolean;
  /** Routes draft persistence to the owning daemon; required when annotating. */
  workspaceId?: string;
  /** Reports the current annotation count (drives the tile header's Send N). */
  onAnnotationsCountChange?: (count: number) => void;
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
 * GATE CONTRACT: re-renders only when document identity changes — `memo`'s
 * shallow compare on the content STRING is the content hash, and the
 * live-reload poller re-reads the file every 750ms, so an unchanged file must
 * produce zero re-renders. `rootRef`/`onImageClick` stay referentially stable
 * so they never defeat the compare. A re-render remounts the whole tree (fresh
 * component closures are new element types), snapping open `<details>` shut,
 * re-running shiki, and wiping copy-button state; that cost is accepted only
 * when the content really changed.
 */
const MarkdownReaderBody = memo(function MarkdownReaderBody({
  content,
  path,
  allowLocalTargets,
  rootRef,
  onImageClick,
}: MarkdownReaderBodyProps) {
  const frontmatter = extractFrontmatter(content);
  // Fresh components per render is fine: the memo gate means the tree remounts
  // anyway, and per-parse state lives in the rehype passes.
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
 * Document reader for markdown tiles; chat-style surfaces keep the plain
 * `Markdown` component. State (the lightbox) lives OUTSIDE the memoized body,
 * so opening it never re-renders the document subtree.
 */
export const MarkdownReader = memo(function MarkdownReader({
  content,
  path,
  allowLocalTargets = true,
  annotationsEnabled = false,
  workspaceId = '',
  onAnnotationsCountChange,
  annotationsSendRef,
}: MarkdownReaderProps) {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const [lightbox, setLightbox] = useState<{ src: string; alt: string } | null>(null);
  // Props of the memoized body: identities must never change (gate contract).
  const handleImageClick = useCallback((src: string, alt: string) => {
    setLightbox({ src, alt });
  }, []);
  const handleLightboxClose = useCallback(() => {
    setLightbox(null);
  }, []);
  // Outside the memoized body so its content-keyed effect fires exactly when
  // the body remounted. Fully inert (no listeners, paints, or traffic) when off.
  const annotationsApi = useAnnotations({
    rootRef,
    content,
    path,
    workspaceId,
    enabled: annotationsEnabled,
  });

  // The send handle must read call-time state, not a captured api identity.
  const annotationsApiRef = useRef(annotationsApi);
  annotationsApiRef.current = annotationsApi;

  // Reports 0 on unmount so a vanished reader never leaves a stale Send N.
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
      // Focusable so a selection can claim keyboard focus: WebKit leaves focus
      // on the terminal's hidden input otherwise, leaking keys to the shell.
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
