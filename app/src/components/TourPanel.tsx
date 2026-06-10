import { useEffect, useId, useMemo, useState, type ReactNode } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type {
  DaemonTour,
  DaemonTourDraft,
  DaemonTourQuestionContext,
} from '../hooks/useDaemonSocket';
import type { ReviewComment } from '../types/generated';
import type { ResolvedTheme } from '../hooks/useTheme';
import { DiffView } from './DiffView';
import './TourPanel.css';

type TourFile = DaemonTour['files'][number];

interface TourPanelProps {
  tour: DaemonTour;
  resolvedTheme: ResolvedTheme;
  onClose: () => void;
  refreshTour: (tourId: string) => Promise<DaemonTour>;
  saveTourDraft: (tourId: string, draft: DaemonTourDraft) => Promise<DaemonTour>;
  askTour: (tourId: string, body: string, context: DaemonTourQuestionContext) => Promise<DaemonTour>;
  submitTour: (tourId: string, body: string, finish: boolean) => Promise<DaemonTour>;
}

function MermaidBlock({ source, resolvedTheme }: { source: string; resolvedTheme: ResolvedTheme }) {
  const reactID = useId();
  const [svg, setSVG] = useState('');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const render = async () => {
      try {
        const { default: mermaid } = await import('mermaid');
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: 'strict',
          theme: resolvedTheme === 'dark' ? 'dark' : 'default',
        });
        const id = `tour-mermaid-${reactID.replace(/[^a-zA-Z0-9]/g, '')}`;
        const result = await mermaid.render(id, source);
        if (!cancelled) {
          setSVG(result.svg);
          setError(null);
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
          setSVG('');
        }
      }
    };
    void render();
    return () => {
      cancelled = true;
    };
  }, [reactID, resolvedTheme, source]);

  if (error) {
    return <pre className="tour-panel__mermaid-error"><code>{source}</code></pre>;
  }
  return <div className="tour-panel__mermaid" dangerouslySetInnerHTML={{ __html: svg }} />;
}

function TourMarkdown({
  children,
  resolvedTheme,
}: {
  children: string;
  resolvedTheme: ResolvedTheme;
}) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        code({ className, children: codeChildren, ...props }) {
          const source = String(codeChildren).replace(/\n$/, '');
          if (className === 'language-mermaid') {
            return <MermaidBlock source={source} resolvedTheme={resolvedTheme} />;
          }
          return <code className={className} {...props}>{codeChildren as ReactNode}</code>;
        },
      }}
    >
      {children}
    </ReactMarkdown>
  );
}

function emptyDraft(path: string): DaemonTourDraft {
  return {
    path,
    reviewed: false,
    note: '',
    annotation_replies: [],
    line_comments: [],
  };
}

function feedbackMarkdown(tour: DaemonTour): string {
  const sections: string[] = [];
  for (const draft of tour.drafts) {
    const details: string[] = [];
    if (draft.reviewed) details.push('- Reviewed');
    if (draft.note.trim()) details.push(draft.note.trim());
    for (const comment of draft.line_comments) {
      details.push(`- Line ${comment.line}: ${comment.body.trim()}`);
    }
    for (const reply of draft.annotation_replies) {
      details.push(`- Annotation ${reply.id}: ${reply.body.trim()}`);
    }
    if (details.length > 0) {
      sections.push(`### \`${draft.path}\`\n\n${details.join('\n\n')}`);
    }
  }
  return sections.length > 0
    ? `## Tour feedback\n\n${sections.join('\n\n')}`
    : '## Tour feedback\n\nNo additional notes.';
}

function commentsForFile(file: TourFile, draft: DaemonTourDraft): ReviewComment[] {
  const createdAt = new Date(0).toISOString();
  const comments: ReviewComment[] = [];
  for (const annotation of file.annotations) {
    for (const [index, comment] of annotation.comments.entries()) {
      comments.push({
        id: `annotation:${annotation.id}:${index}`,
        review_id: 'tour',
        filepath: file.path,
        line_start: annotation.line_start,
        line_end: annotation.line_end,
        content: comment.body,
        author: comment.author,
        resolved: false,
        created_at: createdAt,
      });
    }
    for (const reply of draft.annotation_replies.filter((entry) => entry.id === annotation.id)) {
      comments.push({
        id: `reply:${annotation.id}`,
        review_id: 'tour',
        filepath: file.path,
        line_start: annotation.line_start,
        line_end: annotation.line_end,
        content: reply.body,
        author: 'you',
        resolved: false,
        created_at: createdAt,
      });
    }
  }
  for (const [index, comment] of draft.line_comments.entries()) {
    comments.push({
      id: `line:${index}`,
      review_id: 'tour',
      filepath: file.path,
      line_start: comment.line,
      line_end: comment.line,
      content: comment.body,
      author: 'you',
      resolved: false,
      created_at: createdAt,
    });
  }
  return comments;
}

function codeContext(file: TourFile, start?: number, end?: number): string | undefined {
  if (!start) return undefined;
  const lines = file.modified.split('\n');
  return lines.slice(start - 1, Math.max(start, end || start)).join('\n');
}

export function TourPanel({
  tour,
  resolvedTheme,
  onClose,
  refreshTour,
  saveTourDraft,
  askTour,
  submitTour,
}: TourPanelProps) {
  const firstTourFile = tour.files.find((file) => file.group === 'tour') ?? tour.files[0];
  const [selectedPath, setSelectedPath] = useState(tour.current_file || firstTourFile?.path || '');
  const [question, setQuestion] = useState('');
  const [questionStart, setQuestionStart] = useState('');
  const [questionEnd, setQuestionEnd] = useState('');
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!tour.files.some((file) => file.path === selectedPath)) {
      setSelectedPath(tour.current_file || firstTourFile?.path || '');
    }
  }, [firstTourFile?.path, selectedPath, tour.current_file, tour.files]);

  const selectedFile = tour.files.find((file) => file.path === selectedPath) ?? firstTourFile;
  const storedDraft = tour.drafts.find((draft) => draft.path === selectedFile?.path);
  const draft = storedDraft ?? emptyDraft(selectedFile?.path || '');
  const diffComments = useMemo(
    () => selectedFile ? commentsForFile(selectedFile, draft) : [],
    [draft, selectedFile],
  );

  const persistDraft = async (nextDraft: DaemonTourDraft) => {
    setBusy('save');
    setError(null);
    try {
      await saveTourDraft(tour.tour_id, nextDraft);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  const updateAnnotationReply = (annotationId: string, body: string) => {
    const replies = draft.annotation_replies.filter((reply) => reply.id !== annotationId);
    if (body.trim()) replies.push({ id: annotationId, body: body.trim() });
    void persistDraft({ ...draft, annotation_replies: replies });
  };

  const handleAsk = async () => {
    if (!selectedFile || !question.trim()) return;
    const start = Number.parseInt(questionStart, 10);
    const end = Number.parseInt(questionEnd, 10);
    const context: DaemonTourQuestionContext = {
      source: 'tour',
      path: selectedFile.path,
      ...(Number.isFinite(start) && start > 0 ? { line_start: start } : {}),
      ...(Number.isFinite(end) && end >= start ? { line_end: end } : {}),
      ...(codeContext(selectedFile, start, end) ? { code: codeContext(selectedFile, start, end) } : {}),
    };
    setBusy('ask');
    setError(null);
    try {
      await askTour(tour.tour_id, question.trim(), context);
      setQuestion('');
      setQuestionStart('');
      setQuestionEnd('');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  const handleSubmit = async (finish: boolean) => {
    setBusy(finish ? 'finish' : 'submit');
    setError(null);
    try {
      await submitTour(tour.tour_id, feedbackMarkdown(tour), finish);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  const groups = [
    { id: 'tour', label: 'Tour' },
    { id: 'other', label: 'Other' },
    { id: 'skip', label: 'Skipped' },
  ];

  return (
    <section className="tour-panel">
      <header className="tour-panel__header">
        <div>
          <div className="tour-panel__eyebrow">Interactive code tour</div>
          <h2>{tour.name}</h2>
          <div className="tour-panel__meta">
            <span>{tour.base_ref}</span>
            <span className={`tour-panel__connection tour-panel__connection--${tour.connection_state}`}>
              {tour.connection_state === 'connected' ? 'Agent listening' : 'Agent disconnected'}
            </span>
          </div>
        </div>
        <div className="tour-panel__header-actions">
          <button
            type="button"
            onClick={() => {
              setBusy('refresh');
              setError(null);
              void refreshTour(tour.tour_id)
                .catch((err) => setError(err instanceof Error ? err.message : String(err)))
                .finally(() => setBusy(null));
            }}
            disabled={busy !== null || tour.status === 'ended'}
          >
            {busy === 'refresh' ? 'Refreshing...' : 'Refresh'}
          </button>
          <button type="button" className="tour-panel__close" onClick={onClose} aria-label="Close tour panel">×</button>
        </div>
      </header>

      {tour.connection_state === 'disconnected' && tour.status === 'active' ? (
        <div className="tour-panel__notice">
          The tour is preserved. The agent can re-run <code>attn tour start</code> to reattach.
        </div>
      ) : null}
      {tour.warnings.length > 0 ? (
        <div className="tour-panel__warnings">{tour.warnings.join(' ')}</div>
      ) : null}
      {error ? <div className="tour-panel__error">{error}</div> : null}

      <div className="tour-panel__layout">
        <nav className="tour-panel__rail" aria-label="Tour files">
          <article className="tour-panel__summary">
            <TourMarkdown resolvedTheme={resolvedTheme}>{tour.summary}</TourMarkdown>
          </article>
          {groups.map((group) => {
            const files = tour.files.filter((file) => file.group === group.id);
            if (files.length === 0) return null;
            return (
              <div className="tour-panel__group" key={group.id}>
                <h3>{group.label}</h3>
                {files.map((file, index) => {
                  const fileDraft = tour.drafts.find((entry) => entry.path === file.path);
                  return (
                    <button
                      type="button"
                      key={file.path}
                      className={file.path === selectedFile?.path ? 'is-active' : ''}
                      onClick={() => {
                        setSelectedPath(file.path);
                        const nextDraft = tour.drafts.find((entry) => entry.path === file.path) ?? emptyDraft(file.path);
                        void persistDraft(nextDraft);
                      }}
                    >
                      <span>{group.id === 'tour' ? `${index + 1}.` : ''}</span>
                      <strong>{file.path.split('/').pop()}</strong>
                      <small>{file.path}</small>
                      {fileDraft?.reviewed ? <em>Reviewed</em> : null}
                    </button>
                  );
                })}
              </div>
            );
          })}
        </nav>

        <main className="tour-panel__main">
          {selectedFile ? (
            <>
              <div className="tour-panel__file-heading">
                <div>
                  <span>{selectedFile.status}</span>
                  <h3>{selectedFile.path}</h3>
                </div>
                <label>
                  <input
                    type="checkbox"
                    checked={draft.reviewed}
                    onChange={(event) => void persistDraft({ ...draft, reviewed: event.target.checked })}
                  />
                  Reviewed
                </label>
              </div>
              {selectedFile.note ? (
                <article className="tour-panel__file-note">
                  <TourMarkdown resolvedTheme={resolvedTheme}>{selectedFile.note}</TourMarkdown>
                </article>
              ) : null}

              <div className="tour-panel__code">
                {selectedFile.view === 'content' ? (
                  <pre><code>{selectedFile.modified}</code></pre>
                ) : (
                  <DiffView
                    original={selectedFile.original}
                    modified={selectedFile.modified}
                    filePath={selectedFile.path}
                    comments={diffComments}
                    editingCommentId={null}
                    resolvedTheme={resolvedTheme}
                    diffStyle="unified"
                    expandUnchanged={false}
                    onAddComment={(lineStart, _lineEnd, content) => persistDraft({
                      ...draft,
                      line_comments: [...draft.line_comments, { line: lineStart, body: content }],
                    })}
                    onEditComment={() => {}}
                    onStartEdit={() => {}}
                    onCancelEdit={() => {}}
                    onResolveComment={() => {}}
                    onDeleteComment={() => {}}
                  />
                )}
              </div>
            </>
          ) : <div className="tour-panel__empty">No changed files in this tour.</div>}
        </main>

        <aside className="tour-panel__conversation">
          <section>
            <h3>Notes</h3>
            <textarea
              defaultValue={draft.note}
              key={`${draft.path}:${draft.note}`}
              placeholder="Feedback on this file"
              onBlur={(event) => void persistDraft({ ...draft, note: event.target.value })}
            />
          </section>

          {selectedFile?.annotations.map((annotation) => {
            const reply = draft.annotation_replies.find((entry) => entry.id === annotation.id)?.body || '';
            return (
              <section className="tour-panel__annotation" key={annotation.id}>
                <h3>Lines {annotation.line_start}{annotation.line_end !== annotation.line_start ? `-${annotation.line_end}` : ''}</h3>
                {annotation.comments.map((comment, index) => (
                  <div className="tour-panel__annotation-comment" key={`${annotation.id}:${index}`}>
                    <strong>{comment.author}</strong>
                    <TourMarkdown resolvedTheme={resolvedTheme}>{comment.body}</TourMarkdown>
                  </div>
                ))}
                <textarea
                  defaultValue={reply}
                  key={`${annotation.id}:${reply}`}
                  placeholder="Reply to this annotation"
                  onBlur={(event) => updateAnnotationReply(annotation.id, event.target.value)}
                />
              </section>
            );
          })}

          <section className="tour-panel__question">
            <h3>Ask the agent</h3>
            <textarea
              value={question}
              onChange={(event) => setQuestion(event.target.value)}
              placeholder="Ask about the selected file"
            />
            <div className="tour-panel__line-inputs">
              <input value={questionStart} onChange={(event) => setQuestionStart(event.target.value)} placeholder="Start line" inputMode="numeric" />
              <input value={questionEnd} onChange={(event) => setQuestionEnd(event.target.value)} placeholder="End line" inputMode="numeric" />
            </div>
            <button type="button" onClick={() => void handleAsk()} disabled={!question.trim() || busy !== null || tour.status === 'ended'}>
              {busy === 'ask' ? 'Sending...' : 'Ask question'}
            </button>
          </section>

          <section className="tour-panel__transcript">
            <h3>Conversation</h3>
            {tour.transcript.length === 0 ? <p>No questions yet.</p> : tour.transcript.map((entry) => (
              <div className={`tour-panel__message tour-panel__message--${entry.role}`} key={entry.id}>
                <strong>{entry.role === 'agent' ? 'Agent' : 'You'}</strong>
                <TourMarkdown resolvedTheme={resolvedTheme}>{entry.body}</TourMarkdown>
              </div>
            ))}
          </section>

          <footer className="tour-panel__submit">
            <button type="button" onClick={() => void handleSubmit(false)} disabled={busy !== null || tour.status === 'ended'}>
              {busy === 'submit' ? 'Sending...' : 'Send feedback'}
            </button>
            <button type="button" className="tour-panel__end" onClick={() => void handleSubmit(true)} disabled={busy !== null || tour.status === 'ended'}>
              {tour.status === 'ended' ? 'Tour ended' : busy === 'finish' ? 'Ending...' : 'End tour'}
            </button>
            <small>Feedback stays local to attn. Nothing is submitted to GitHub.</small>
          </footer>
        </aside>
      </div>
    </section>
  );
}
