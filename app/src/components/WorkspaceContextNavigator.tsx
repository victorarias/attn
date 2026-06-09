import { useEffect, useMemo, useRef, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { DaemonWorkspaceContext } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import './WorkspaceContextNavigator.css';

export interface WorkspaceContextView {
  context: DaemonWorkspaceContext;
  title: string;
  directory: string;
  updatedByLabel?: string;
}

interface WorkspaceContextNavigatorProps {
  isOpen: boolean;
  contexts: WorkspaceContextView[];
  isLoading: boolean;
  error: string | null;
  onClose: () => void;
  onRetry: () => void;
}

export function WorkspaceContextNavigator({
  isOpen,
  contexts,
  isLoading,
  error,
  onClose,
  onRetry,
}: WorkspaceContextNavigatorProps) {
  const searchRef = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState('');
  const [selectedWorkspaceId, setSelectedWorkspaceId] = useState<string | null>(null);
  const filteredContexts = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return contexts;
    return contexts.filter(({ title, directory, context }) => (
      `${title} ${directory} ${context.content}`.toLowerCase().includes(normalized)
    ));
  }, [contexts, query]);
  const selected = filteredContexts.find(({ context }) => context.workspace_id === selectedWorkspaceId)
    || filteredContexts[0]
    || null;

  useEscapeStack(onClose, isOpen);

  useEffect(() => {
    if (!isOpen) return;
    setQuery('');
    requestAnimationFrame(() => searchRef.current?.focus());
  }, [isOpen]);

  useEffect(() => {
    if (!contexts.some(({ context }) => context.workspace_id === selectedWorkspaceId)) {
      setSelectedWorkspaceId(contexts[0]?.context.workspace_id || null);
    }
  }, [contexts, selectedWorkspaceId]);

  if (!isOpen) return null;

  const moveSelection = (offset: number) => {
    if (filteredContexts.length === 0) return;
    const currentIndex = Math.max(0, filteredContexts.findIndex(({ context }) => (
      context.workspace_id === selected?.context.workspace_id
    )));
    const nextIndex = Math.min(filteredContexts.length - 1, Math.max(0, currentIndex + offset));
    setSelectedWorkspaceId(filteredContexts[nextIndex].context.workspace_id);
  };

  return (
    <div className="workspace-context-shell">
      <FocusTrap focusTrapOptions={{ escapeDeactivates: false }}>
        <div
          className="workspace-context-navigator"
          role="dialog"
          aria-modal="true"
          aria-labelledby="workspace-context-title"
        >
          <header className="workspace-context-header">
            <div className="workspace-context-heading">
              <ContextIcon />
              <div>
                <span className="workspace-context-eyebrow">Local shared memory</span>
                <h1 id="workspace-context-title">Workspace contexts on this Mac</h1>
              </div>
            </div>
            <div className="workspace-context-search">
              <SearchIcon />
              <input
                ref={searchRef}
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'ArrowDown') {
                    event.preventDefault();
                    moveSelection(1);
                  } else if (event.key === 'ArrowUp') {
                    event.preventDefault();
                    moveSelection(-1);
                  }
                }}
                placeholder="Search contexts..."
                aria-label="Search workspace contexts"
              />
            </div>
            <button type="button" className="workspace-context-close" onClick={onClose}>
              <span>Close</span><kbd>esc</kbd>
            </button>
          </header>

          <div className="workspace-context-body">
            <aside className="workspace-context-list" aria-label="Workspace contexts">
              <div className="workspace-context-list-label">
                <span>Contexts</span>
                <strong>{filteredContexts.length}</strong>
              </div>
              {isLoading && <ContextListSkeleton />}
              {!isLoading && error && (
                <div className="workspace-context-list-state">
                  <span>Could not load contexts</span>
                  <button type="button" onClick={onRetry}>Try again</button>
                </div>
              )}
              {!isLoading && !error && filteredContexts.map((item) => (
                <button
                  type="button"
                  key={item.context.workspace_id}
                  className={`workspace-context-list-item${
                    item.context.workspace_id === selected?.context.workspace_id ? ' is-selected' : ''
                  }`}
                  onClick={() => setSelectedWorkspaceId(item.context.workspace_id)}
                >
                  <span className="workspace-context-list-marker" />
                  <span className="workspace-context-list-copy">
                    <strong>{item.title}</strong>
                    <span>{item.directory}</span>
                  </span>
                  <span className="workspace-context-revision">r{item.context.revision}</span>
                </button>
              ))}
              {!isLoading && !error && filteredContexts.length === 0 && (
                <div className="workspace-context-list-state">
                  <span>{query ? 'No matching contexts' : 'No workspace contexts yet'}</span>
                </div>
              )}
            </aside>

            <main className="workspace-context-document">
              {isLoading && <DocumentSkeleton />}
              {!isLoading && error && (
                <div className="workspace-context-document-state">
                  <ContextIcon />
                  <h2>Context unavailable</h2>
                  <p>{error}</p>
                </div>
              )}
              {!isLoading && !error && selected && (
                <>
                  <div className="workspace-context-document-meta">
                    <div>
                      <span>Workspace</span>
                      <h2>{selected.title}</h2>
                      <p>{selected.directory}</p>
                    </div>
                    <dl>
                      <div><dt>Revision</dt><dd>{selected.context.revision}</dd></div>
                      <div><dt>Updated</dt><dd>{formatUpdatedAt(selected.context.updated_at)}</dd></div>
                      {selected.updatedByLabel && (
                        <div><dt>By</dt><dd>{selected.updatedByLabel}</dd></div>
                      )}
                    </dl>
                  </div>
                  <article className="workspace-context-markdown">
                    <ReactMarkdown
                      remarkPlugins={[remarkGfm]}
                      components={{
                        a: ({ children, ...props }) => <a {...props} target="_blank" rel="noreferrer">{children}</a>,
                      }}
                    >
                      {selected.context.content || '_This workspace context is empty._'}
                    </ReactMarkdown>
                  </article>
                </>
              )}
              {!isLoading && !error && !selected && (
                <div className="workspace-context-document-state">
                  <ContextIcon />
                  <h2>{query ? 'No context selected' : 'No shared context yet'}</h2>
                  <p>{query ? 'Adjust the search to find a workspace.' : 'Contexts appear here after an agent publishes one.'}</p>
                </div>
              )}
            </main>
          </div>
        </div>
      </FocusTrap>
    </div>
  );
}

function formatUpdatedAt(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return 'Unknown';
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  }).format(date);
}

function ContextIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M6 3.5h9l3 3V20a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1Z" />
      <path d="M15 3.5V7h3M8 11h7M8 14.5h7M8 18h4" />
    </svg>
  );
}

function SearchIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <circle cx="8.5" cy="8.5" r="5.5" />
      <path d="m12.5 12.5 4 4" />
    </svg>
  );
}

function ContextListSkeleton() {
  return (
    <div className="workspace-context-skeleton-list" aria-label="Loading contexts">
      {[0, 1, 2].map((item) => <span key={item} />)}
    </div>
  );
}

function DocumentSkeleton() {
  return (
    <div className="workspace-context-skeleton-document" aria-label="Loading context">
      <span /><span /><span /><span />
    </div>
  );
}
