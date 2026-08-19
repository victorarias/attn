import { createContext, memo, useContext, useMemo, type ReactNode } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkBreaks from 'remark-breaks';
import remarkGfm from 'remark-gfm';
import { MermaidDiagram } from './MermaidDiagram';
import { PENDING_DIAGRAM_LANGUAGE, prepareStreamingMarkdown, splitStreamingMarkdown } from './streaming';

// Read by the module-level CodeRenderer below. A context (rather than a
// per-render closure) keeps CodeRenderer's component identity stable across
// re-renders of Markdown, so a caller re-rendering with a fresh
// onDiagramLayoutChange reference (e.g. PresentTour after an items-version
// bump) never remounts an in-flight MermaidDiagram.
const DiagramLayoutChangeContext = createContext<(() => void) | undefined>(undefined);
const DiagramPresentationContext = createContext<'static' | 'reader'>('static');

/** Opts a full document reader into progressive controls for oversized diagrams. */
export function ReaderDiagramPresentation({ children }: { children: ReactNode }) {
  return (
    <DiagramPresentationContext.Provider value="reader">
      {children}
    </DiagramPresentationContext.Provider>
  );
}

// react-markdown v10's `code` component gets no `inline` flag; a fenced block
// carries a `language-*` className, inline code carries none.
// Exported so MarkdownReader reuses the exact mermaid path (and its stable
// component identity) instead of forking diagram rendering.
export const CodeRenderer: Components['code'] = ({ className, children, ...props }) => {
  const onDiagramLayoutChange = useContext(DiagramLayoutChangeContext);
  const presentation = useContext(DiagramPresentationContext);
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
  return (
    <code className={className} {...props}>
      {children}
    </code>
  );
};

const defaultComponents: Components = { code: CodeRenderer };

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
        <MarkdownDocument source={tail} remarkPlugins={remarkPlugins} components={merged} />
      </DiagramLayoutChangeContext.Provider>
    </div>
  );
}
