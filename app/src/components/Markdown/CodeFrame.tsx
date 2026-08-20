import { isValidElement, useEffect, useRef, useState, type ReactNode } from 'react';
import { PENDING_DIAGRAM_LANGUAGE } from './streaming';
import './Markdown.css';

/**
 * Chrome around a fenced code block: a header naming the language, a copy
 * button, and one border holding both to the code.
 *
 * It lives on `pre` rather than `code` because react-markdown renders the
 * `<pre>` itself — a `code` component cannot reach outside it. A diagram fence
 * is not code and never gets framed here; MermaidDiagram carries its own.
 */

function fenceLanguage(className: string | undefined): string | undefined {
  const found = /language-([\w-]+)/.exec(className ?? '');
  return found?.[1];
}

/** The text of the fence, for the clipboard. */
function fenceText(node: ReactNode): string {
  if (typeof node === 'string') return node;
  if (Array.isArray(node)) return node.map(fenceText).join('');
  if (isValidElement<{ children?: ReactNode }>(node)) return fenceText(node.props.children);
  return '';
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => () => {
    if (timer.current !== null) clearTimeout(timer.current);
  }, []);

  const copy = () => {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      if (timer.current !== null) clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), 2000);
    }).catch((error) => {
      console.error('[Markdown] Failed to copy code block:', error);
    });
  };

  return (
    <button
      type="button"
      className="markdown-code-frame-action"
      onClick={copy}
      aria-label={copied ? 'Copied' : 'Copy code'}
    >
      {copied ? 'Copied' : 'Copy'}
    </button>
  );
}

interface CodeFrameProps {
  /** The `<code>` element react-markdown built for this fence. */
  children: ReactNode;
  className?: string;
}

export function CodeFrame({ children, className }: CodeFrameProps) {
  const child = isValidElement<{ className?: string; children?: ReactNode }>(children)
    ? children
    : null;
  const language = fenceLanguage(child?.props.className);

  // A diagram — drawn or still arriving — is not a code block, and the element
  // CodeRenderer returned for it is already a complete box.
  if (language === 'mermaid' || language === PENDING_DIAGRAM_LANGUAGE) {
    return <>{children}</>;
  }

  return (
    <div className="markdown-code-frame" data-language={language ?? 'text'}>
      <div className="markdown-code-frame-header" data-md-chrome="1">
        <span className="markdown-code-frame-language">{language ?? 'text'}</span>
        <CopyButton text={fenceText(children)} />
      </div>
      <pre className={className}>{children}</pre>
    </div>
  );
}
