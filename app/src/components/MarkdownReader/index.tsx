import { createElement, isValidElement, memo, useRef } from 'react';
import type { HTMLAttributes, ReactNode } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkFrontmatter from 'remark-frontmatter';
import remarkGfm from 'remark-gfm';
import '@fontsource-variable/inter';
import { CodeRenderer } from '../Markdown';
import { CodeBlock } from './CodeBlock';
import { extractFrontmatter, type FrontmatterEntry } from './frontmatter';
import {
  isSafeLocalMarkdownImageTarget,
  isSafeLocalMarkdownTarget,
  openMarkdownTarget,
  resolveMarkdownTarget,
  sanitizeLinkUrl,
} from './markdownLinks';
import rehypeSourceAnchors from './rehypeSourceAnchors';
import { scrollToAnchor } from './scrollToAnchor';
import { createSlugger } from './slugify';
import { tilePathBasename } from '../../utils/tilePresentation';
import './MarkdownReader.css';

// The document is parsed WITH remark-frontmatter (the yaml node stays in the
// tree and is never rendered), so remark positions already refer to raw-file
// lines — the correct anchor lineOffset is 0. Only a caller that strips
// frontmatter before parsing would pass extractFrontmatter().lineCount.
const remarkPlugins = [remarkGfm, remarkFrontmatter];
const rehypePlugins: [typeof rehypeSourceAnchors, { lineOffset: number }][] = [
  [rehypeSourceAnchors, { lineOffset: 0 }],
];

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
): Components {
  const slugger = createSlugger();
  const heading = (level: number): Components['h1'] => ({ node: _node, children, ...props }) =>
    createElement(`h${level}`, { ...props, id: slugger(textOf(children)) }, children);

  return {
    h1: heading(1),
    h2: heading(2),
    h3: heading(3),
    h4: heading(4),
    h5: heading(5),
    h6: heading(6),
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
    img({ src, alt }) {
      const imgSrc = typeof src === 'string' ? src : undefined;
      const target = imgSrc ? resolveMarkdownTarget(documentPath, imgSrc) : null;
      if (!target || target.kind !== 'local' || !allowLocalTargets || !isSafeLocalMarkdownImageTarget(target.value)) {
        return (
          <span className="md-reader-blocked-image" title={imgSrc}>
            [blocked image: {alt || imgSrc || 'unknown source'}]
          </span>
        );
      }
      return (
        <button
          type="button"
          className="md-reader-local-image"
          title={target.value}
          onClick={() => openMarkdownTarget(target)}
        >
          Open image: {alt || tilePathBasename(target.value)}
        </button>
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

export interface MarkdownReaderProps {
  /** Raw markdown file content (frontmatter included). */
  content: string;
  /** Absolute path of the document; relative link/image targets resolve against it. */
  path: string;
  /** False for remote workspaces: local file links/images render blocked. */
  allowLocalTargets?: boolean;
}

/**
 * Document reader for markdown tiles: plannotator typography on a centered
 * card, source-line anchoring (data-block-id/data-source-line), syntax
 * highlighting with a hover copy button, GitHub heading slugs, safe link
 * routing, and a frontmatter metadata card. Shared chat-style surfaces keep
 * using the plain `Markdown` component.
 *
 * Memoized: the body creates fresh component closures per render (the heading
 * slugger's dedup map must reset per document render), which React treats as
 * new element types and remounts the whole rendered tree — re-running async
 * shiki highlights and wiping copy-button state. memo blocks identical-prop
 * parent re-renders so that only happens when the document actually changes.
 */
export const MarkdownReader = memo(function MarkdownReader({
  content,
  path,
  allowLocalTargets = true,
}: MarkdownReaderProps) {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const frontmatter = extractFrontmatter(content);
  // Fresh components per render: the heading slugger's dedup map must reset
  // on every document render (same reason the dock tile rebuilt its map).
  const components = readerComponents(path, allowLocalTargets, rootRef);

  return (
    <div className="md-reader" ref={rootRef}>
      <div className="md-reader-wrap">
        <article className="md-reader-card">
          <FrontmatterCard entries={frontmatter.entries} />
          <ReactMarkdown remarkPlugins={remarkPlugins} rehypePlugins={rehypePlugins} components={components}>
            {content}
          </ReactMarkdown>
        </article>
      </div>
    </div>
  );
});
