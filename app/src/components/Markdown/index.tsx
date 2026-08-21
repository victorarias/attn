import { createContext, memo, useContext, useMemo, type ReactNode } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkBreaks from 'remark-breaks';
import remarkGfm from 'remark-gfm';
import { CodeFrame } from './CodeFrame';
import { MermaidDiagram } from './MermaidDiagram';
import { useShikiHighlight } from './shiki';
import { PENDING_DIAGRAM_LANGUAGE, prepareStreamingMarkdown, splitStreamingMarkdown } from './streaming';

// Read by the module-level CodeRenderer below. A context (rather than a
// per-render closure) keeps CodeRenderer's component identity stable across
// re-renders of Markdown, so a caller re-rendering with a fresh
// onDiagramLayoutChange reference (e.g. PresentTour after an items-version
// bump) never remounts an in-flight MermaidDiagram.
const DiagramLayoutChangeContext = createContext<(() => void) | undefined>(undefined);
// Chrome a long-form reading surface wants and a ticket description does not:
// framed code blocks, framed diagrams, and progressive controls for an
// oversized one. 'static' is the plain rendering every other markdown surface
// in attn already had.
const MarkdownPresentationContext = createContext<'static' | 'reader'>('static');
// True only inside the tail document of a message still being written. Its text
// can still change meaning, so anything whose cost is paid per render — today,
// syntax highlighting — waits until the settled half catches up.
const VolatileTextContext = createContext(false);

/** Opts a long-form reading surface into the framed-block chrome. */
export function ReaderPresentation({ children }: { children: ReactNode }) {
  return (
    <MarkdownPresentationContext.Provider value="reader">
      {children}
    </MarkdownPresentationContext.Provider>
  );
}

// react-markdown renders the <pre> for a fence, so the frame has to be here;
// CodeRenderer only ever gets the <code> inside it.
const PreRenderer: Components['pre'] = ({ children, className, ...props }) => {
  const presentation = useContext(MarkdownPresentationContext);
  if (presentation !== 'reader') {
    return <pre className={className} {...props}>{children}</pre>;
  }
  return <CodeFrame className={className}>{children}</CodeFrame>;
};

// Diagrams are drawn, not highlighted, and inline code carries no language.
function highlightableLanguage(className: string | undefined): string | undefined {
  const found = /language-([\w-]+)/.exec(className ?? '');
  const language = found?.[1];
  if (!language || language === 'mermaid' || language === PENDING_DIAGRAM_LANGUAGE) return undefined;
  return language;
}

// react-markdown v10's `code` component gets no `inline` flag; a fenced block
// carries a `language-*` className, inline code carries none.
// Exported so MarkdownReader reuses the exact mermaid path (and its stable
// component identity) instead of forking diagram rendering.
export const CodeRenderer: Components['code'] = ({ className, children, ...props }) => {
  const onDiagramLayoutChange = useContext(DiagramLayoutChangeContext);
  const presentation = useContext(MarkdownPresentationContext);
  const volatile = useContext(VolatileTextContext);
  // Hooks run before any of the early returns below can skip them. Inline code
  // has no language and is by far the most common `code` element in a
  // transcript, so it must not pay to join the children on every render.
  const language = highlightableLanguage(className);
  const code = language ? String(children) : '';
  const highlighted = useShikiHighlight(code, language, !volatile);
  // A diagram whose fence has not closed yet. prepareStreamingMarkdown renames
  // the language so half a graph never reaches mermaid, which would draw its
  // parse error where the picture goes.
  if (className?.includes(`language-${PENDING_DIAGRAM_LANGUAGE}`)) {
    return (
      <pre className="markdown-diagram-pending" data-testid="markdown-diagram-pending">
        <code>{children}</code>
      </pre>
    );
  }
  if (className?.includes('language-mermaid')) {
    return (
      <MermaidDiagram
        code={String(children).trimEnd()}
        onLayoutChange={onDiagramLayoutChange}
        presentation={presentation}
      />
    );
  }
  if (highlighted) {
    return (
      <code
        className={`${className ?? ''} markdown-shiki`.trim()}
        {...props}
        // eslint-disable-next-line react/no-danger -- shiki output is
        // library-generated spans over the code text, not document HTML.
        dangerouslySetInnerHTML={{ __html: highlighted.html }}
      />
    );
  }
  return (
    <code className={className} {...props}>
      {children}
    </code>
  );
};

const defaultComponents: Components = { code: CodeRenderer, pre: PreRenderer };

/**
 * One markdown document, re-rendered only when its source text changes.
 *
 * This is what makes the streaming split pay: the settled head is the same
 * string for tens of deltas in a row, so React skips the whole subtree —
 * parser included — until the next safe cut moves it. See
 * splitStreamingMarkdown for why the parse is the bill being avoided.
 */
const MarkdownDocument = memo(function MarkdownDocument({
  source,
  remarkPlugins,
  components,
}: {
  source: string;
  remarkPlugins: NonNullable<Parameters<typeof ReactMarkdown>[0]['remarkPlugins']>;
  components: Components;
}) {
  return (
    <ReactMarkdown remarkPlugins={remarkPlugins} components={components}>
      {source}
    </ReactMarkdown>
  );
});

interface MarkdownProps {
  children: string;
  className?: string;
  components?: Components;
  // Chat-style surfaces (ticket descriptions/comments, review comments) expect
  // Enter to produce a line break, like GitHub comments; standard markdown
  // collapses a single newline inside a paragraph. Off by default so Present
  // summaries/file notes keep standard markdown semantics.
  breaks?: boolean;
  // Forwarded to every mermaid diagram rendered inside this document — see
  // MermaidDiagram's onLayoutChange for why a CodeView host needs this.
  onDiagramLayoutChange?: () => void;
  // This text is still arriving. Markdown is not prefix-stable, so the tail is
  // completed before it is parsed — see streaming.ts for what that costs and
  // the measurement behind it. A settled message renders verbatim, which is
  // what makes the streamed result identical to rendering the final text once.
  streaming?: boolean;
}

/** Shared markdown renderer: GFM + mermaid code fences rendered as diagrams. */
export function Markdown({ children, className, components, breaks, onDiagramLayoutChange, streaming }: MarkdownProps) {
  const remarkPlugins = useMemo(() => (breaks ? [remarkGfm, remarkBreaks] : [remarkGfm]), [breaks]);
  const merged = useMemo(() => ({ ...defaultComponents, ...components }), [components]);
  // A settled message is rendered exactly as written — that is what makes the
  // streamed result and the one-shot result the same DOM.
  const { settled, tail } = useMemo(() => {
    if (!streaming) return { settled: '', tail: children };
    return splitStreamingMarkdown(prepareStreamingMarkdown(children));
  }, [children, streaming]);
  return (
    <div className={className}>
      <DiagramLayoutChangeContext.Provider value={onDiagramLayoutChange}>
        {settled !== '' && (
          <MarkdownDocument source={settled} remarkPlugins={remarkPlugins} components={merged} />
        )}
        <VolatileTextContext.Provider value={Boolean(streaming)}>
          <MarkdownDocument source={tail} remarkPlugins={remarkPlugins} components={merged} />
        </VolatileTextContext.Provider>
      </DiagramLayoutChangeContext.Provider>
    </div>
  );
}
