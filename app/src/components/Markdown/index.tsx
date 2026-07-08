import { createContext, useContext } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkBreaks from 'remark-breaks';
import remarkGfm from 'remark-gfm';
import { MermaidDiagram } from './MermaidDiagram';

// Read by the module-level CodeRenderer below. A context (rather than a
// per-render closure) keeps CodeRenderer's component identity stable across
// re-renders of Markdown, so a caller re-rendering with a fresh
// onDiagramLayoutChange reference (e.g. PresentTour after an items-version
// bump) never remounts an in-flight MermaidDiagram.
const DiagramLayoutChangeContext = createContext<(() => void) | undefined>(undefined);

// react-markdown v10's `code` component gets no `inline` flag; a fenced block
// carries a `language-*` className, inline code carries none.
const CodeRenderer: Components['code'] = ({ className, children, ...props }) => {
  const onDiagramLayoutChange = useContext(DiagramLayoutChangeContext);
  if (className?.includes('language-mermaid')) {
    return <MermaidDiagram code={String(children).trimEnd()} onLayoutChange={onDiagramLayoutChange} />;
  }
  return (
    <code className={className} {...props}>
      {children}
    </code>
  );
};

const defaultComponents: Components = { code: CodeRenderer };

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
}

/** Shared markdown renderer: GFM + mermaid code fences rendered as diagrams. */
export function Markdown({ children, className, components, breaks, onDiagramLayoutChange }: MarkdownProps) {
  const remarkPlugins = breaks ? [remarkGfm, remarkBreaks] : [remarkGfm];
  return (
    <div className={className}>
      <DiagramLayoutChangeContext.Provider value={onDiagramLayoutChange}>
        <ReactMarkdown remarkPlugins={remarkPlugins} components={{ ...defaultComponents, ...components }}>
          {children}
        </ReactMarkdown>
      </DiagramLayoutChangeContext.Provider>
    </div>
  );
}
