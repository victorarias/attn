import {
  memo,
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type {
  DaemonTour,
  DaemonTourDraft,
  DaemonTourQuestionContext,
} from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { ReviewComment } from '../types/generated';
import type { ResolvedTheme } from '../hooks/useTheme';
import { DiffView } from './DiffView';
import './TourPanel.css';

type TourFile = DaemonTour['files'][number];

interface TourPanelProps {
  tour: DaemonTour;
  resolvedTheme: ResolvedTheme;
  uiScale: number;
  onClose: () => void;
  refreshTour: (tourId: string) => Promise<DaemonTour>;
  saveTourDraft: (tourId: string, draft: DaemonTourDraft) => Promise<DaemonTour>;
  askTour: (tourId: string, body: string, context: DaemonTourQuestionContext) => Promise<DaemonTour>;
  submitTour: (tourId: string, body: string, finish: boolean) => Promise<DaemonTour>;
}

interface TourSection {
  id: string;
  title: string;
  summary: string;
  group: string;
  files: TourFile[];
}

const BRIEFING_STORAGE_PREFIX = 'attn.tour.briefing.';
const MERMAID_ZOOM_STEP = 0.25;
const MERMAID_ZOOM_MIN = 0.75;
const MERMAID_ZOOM_MAX = 3;
let mermaidRenderQueue = Promise.resolve();
const noop = () => {};

function briefingWasSeen(tourId: string): boolean {
  try {
    return window.localStorage.getItem(`${BRIEFING_STORAGE_PREFIX}${tourId}`) === '1';
  } catch {
    return false;
  }
}

function markBriefingSeen(tourId: string): void {
  try {
    window.localStorage.setItem(`${BRIEFING_STORAGE_PREFIX}${tourId}`, '1');
  } catch (err) {
    console.warn('[TourPanel] Failed to persist briefing state:', err);
  }
}

const MermaidBlock = memo(function MermaidBlock({
  source,
  resolvedTheme,
  uiScale,
}: {
  source: string;
  resolvedTheme: ResolvedTheme;
  uiScale: number;
}) {
  const reactID = useId();
  const [svg, setSVG] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [zoom, setZoom] = useState(1);

  useEffect(() => {
    let cancelled = false;
    const render = () => {
      const job = mermaidRenderQueue
        .catch(() => {})
        .then(async () => {
          const { default: mermaid } = await import('mermaid');
          mermaid.initialize({
            startOnLoad: false,
            securityLevel: 'strict',
            theme: resolvedTheme === 'dark' ? 'dark' : 'default',
            themeVariables: {
              fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
              fontSize: `${Math.round(16 * uiScale)}px`,
            },
          });
          const id = `tour-mermaid-${reactID.replace(/[^a-zA-Z0-9]/g, '')}`;
          return mermaid.render(id, source);
        });
      mermaidRenderQueue = job.then(() => undefined, () => undefined);
      return job;
    };
    void render()
      .then((result) => {
        if (!cancelled) {
          setSVG(result.svg);
          setError(null);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [reactID, resolvedTheme, source, uiScale]);

  if (error && !svg) {
    return <pre className="tour-panel__mermaid-error"><code>{source}</code></pre>;
  }
  return (
    <figure className={`tour-panel__mermaid ${svg ? 'is-ready' : 'is-loading'}`}>
      <div className="tour-panel__mermaid-toolbar" aria-label="Diagram zoom controls">
        <span>Diagram</span>
        <div>
          <button
            type="button"
            aria-label="Zoom out diagram"
            onClick={() => setZoom((current) => Math.max(MERMAID_ZOOM_MIN, current - MERMAID_ZOOM_STEP))}
            disabled={zoom <= MERMAID_ZOOM_MIN}
          >
            -
          </button>
          <button
            type="button"
            className="tour-panel__mermaid-reset"
            aria-label="Reset diagram zoom"
            onClick={() => setZoom(1)}
          >
            {Math.round(zoom * 100)}%
          </button>
          <button
            type="button"
            aria-label="Zoom in diagram"
            onClick={() => setZoom((current) => Math.min(MERMAID_ZOOM_MAX, current + MERMAID_ZOOM_STEP))}
            disabled={zoom >= MERMAID_ZOOM_MAX}
          >
            +
          </button>
        </div>
      </div>
      <div className="tour-panel__mermaid-viewport">
        {svg ? (
          <div
            className="tour-panel__mermaid-canvas"
            style={{
              width: `${zoom * 100}%`,
              minWidth: `${Math.round(720 * uiScale * zoom)}px`,
            }}
            dangerouslySetInnerHTML={{ __html: svg }}
          />
        ) : (
          <div className="tour-panel__mermaid-placeholder" aria-label="Rendering diagram">
            <span />
            <span />
            <span />
          </div>
        )}
      </div>
    </figure>
  );
});

const TourMarkdown = memo(function TourMarkdown({
  children,
  resolvedTheme,
  uiScale,
  className = '',
}: {
  children: string;
  resolvedTheme: ResolvedTheme;
  uiScale: number;
  className?: string;
}) {
  const components = useMemo(() => ({
    code({ className: codeClassName, children: codeChildren, ...props }: {
      className?: string;
      children?: ReactNode;
    }) {
      const source = String(codeChildren).replace(/\n$/, '');
      if (codeClassName === 'language-mermaid') {
        return <MermaidBlock source={source} resolvedTheme={resolvedTheme} uiScale={uiScale} />;
      }
      return <code className={codeClassName} {...props}>{codeChildren}</code>;
    },
  }), [resolvedTheme, uiScale]);

  return (
    <div className={`tour-markdown ${className}`.trim()}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {children}
      </ReactMarkdown>
    </div>
  );
});

function fileName(path: string): string {
  return path.split('/').pop() || path;
}

function fileDirectory(path: string): string {
  const parts = path.split('/');
  return parts.length > 1 ? parts.slice(0, -1).join('/') : 'repository root';
}

function isMarkdownFile(path: string): boolean {
  const lowerPath = path.toLowerCase();
  return lowerPath.endsWith('.md') || lowerPath.endsWith('.markdown');
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

function tourWithDraft(tour: DaemonTour, nextDraft: DaemonTourDraft): DaemonTour {
  return {
    ...tour,
    drafts: [
      ...tour.drafts.filter((draft) => draft.path !== nextDraft.path),
      nextDraft,
    ],
  };
}

function draftsMatch(left: DaemonTourDraft, right: DaemonTourDraft): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
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

function buildSections(files: TourFile[]): TourSection[] {
  const sections: TourSection[] = [];
  const sectionByID = new Map<string, TourSection>();
  for (const file of files.filter((entry) => entry.group === 'tour')) {
    const id = file.chapter_id || 'reading-path';
    let section = sectionByID.get(id);
    if (!section) {
      section = {
        id,
        title: file.chapter_title || 'Reading path',
        summary: file.chapter_summary || '',
        group: 'tour',
        files: [],
      };
      sectionByID.set(id, section);
      sections.push(section);
    }
    section.files.push(file);
  }
  for (const bucket of [
    { id: 'other', title: 'Other changes', summary: 'Changed files outside the curated reading path.' },
    { id: 'skip', title: 'Skipped changes', summary: 'Generated, mechanical, or intentionally omitted files.' },
  ]) {
    const bucketFiles = files.filter((file) => file.group === bucket.id);
    if (bucketFiles.length > 0) {
      sections.push({ ...bucket, group: bucket.id, files: bucketFiles });
    }
  }
  return sections;
}

function sectionForFile(sections: TourSection[], path: string): TourSection | undefined {
  return sections.find((section) => section.files.some((file) => file.path === path));
}

function FileStats({ file }: { file: TourFile }) {
  if (file.additions === 0 && file.deletions === 0) return null;
  return (
    <span className="tour-panel__file-stats" aria-label={`${file.additions} additions, ${file.deletions} deletions`}>
      <span className="tour-panel__additions">+{file.additions}</span>
      <span className="tour-panel__deletions">-{file.deletions}</span>
    </span>
  );
}

export function TourPanel({
  tour,
  resolvedTheme,
  uiScale,
  onClose,
  refreshTour,
  saveTourDraft,
  askTour,
  submitTour,
}: TourPanelProps) {
  const firstTourFile = tour.files.find((file) => file.group === 'tour') ?? tour.files[0];
  const [selectedPath, setSelectedPath] = useState(tour.current_file || firstTourFile?.path || '');
  const [briefingOpen, setBriefingOpen] = useState(() => !briefingWasSeen(tour.tour_id));
  const [conversationOpen, setConversationOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [hotspotsOnly, setHotspotsOnly] = useState(false);
  const [unreviewedOnly, setUnreviewedOnly] = useState(false);
  const [expandedSections, setExpandedSections] = useState<Set<string>>(() => new Set());
  const [question, setQuestion] = useState('');
  const [questionStart, setQuestionStart] = useState('');
  const [questionEnd, setQuestionEnd] = useState('');
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reviewSent, setReviewSent] = useState(false);
  const [noteInputs, setNoteInputs] = useState<Record<string, string>>({});
  const [replyInputs, setReplyInputs] = useState<Record<string, string>>({});
  const [markdownRenderOverrides, setMarkdownRenderOverrides] = useState<Record<string, boolean>>({});

  const sections = useMemo(() => buildSections(tour.files), [tour.files]);
  const selectedFile = tour.files.find((file) => file.path === selectedPath) ?? firstTourFile;
  const selectedSection = selectedFile ? sectionForFile(sections, selectedFile.path) : undefined;
  const canRenderMarkdown = Boolean(
    selectedFile
      && isMarkdownFile(selectedFile.path)
      && selectedFile.modified.trim(),
  );
  const renderMarkdown = Boolean(
    selectedFile
      && canRenderMarkdown
      && (markdownRenderOverrides[selectedFile.path] ?? true),
  );
  const reviewedPaths = useMemo(
    () => new Set(tour.drafts.filter((draft) => draft.reviewed).map((draft) => draft.path)),
    [tour.drafts],
  );
  const storedDraft = tour.drafts.find((draft) => draft.path === selectedFile?.path);
  const draft = storedDraft ?? emptyDraft(selectedFile?.path || '');
  const draftRef = useRef(draft);
  draftRef.current = draft;
  const diffComments = useMemo(
    () => selectedFile ? commentsForFile(selectedFile, draft) : [],
    [draft, selectedFile],
  );

  const curatedFiles = useMemo(
    () => tour.files.filter((file) => file.group === 'tour'),
    [tour.files],
  );
  const otherFiles = useMemo(
    () => tour.files.filter((file) => file.group === 'other'),
    [tour.files],
  );
  const skippedFiles = useMemo(
    () => tour.files.filter((file) => file.group === 'skip'),
    [tour.files],
  );
  const reviewedCurated = curatedFiles.filter((file) => reviewedPaths.has(file.path)).length;
  const progressPercent = curatedFiles.length > 0
    ? Math.round((reviewedCurated / curatedFiles.length) * 100)
    : 0;

  const filteredSections = useMemo(() => {
    const query = search.trim().toLowerCase();
    return sections
      .map((section) => {
        const sectionMatches = query !== '' && `${section.title} ${section.summary}`.toLowerCase().includes(query);
        const files = section.files.filter((file) => {
          if (hotspotsOnly && !file.risk_note) return false;
          if (unreviewedOnly && (file.group === 'skip' || reviewedPaths.has(file.path))) return false;
          if (!query || sectionMatches) return true;
          return `${file.path} ${file.note} ${file.risk_note || ''}`.toLowerCase().includes(query);
        });
        return { ...section, files };
      })
      .filter((section) => section.files.length > 0);
  }, [hotspotsOnly, reviewedPaths, search, sections, unreviewedOnly]);

  useEffect(() => {
    if (briefingOpen) {
      markBriefingSeen(tour.tour_id);
    }
  }, [briefingOpen, tour.tour_id]);

  useEffect(() => {
    if (!tour.files.some((file) => file.path === selectedPath)) {
      setSelectedPath(tour.current_file || firstTourFile?.path || '');
    }
  }, [firstTourFile?.path, selectedPath, tour.current_file, tour.files]);

  useEffect(() => {
    if (!selectedSection) return;
    setExpandedSections((current) => {
      if (current.has(selectedSection.id)) return current;
      const next = new Set(current);
      next.add(selectedSection.id);
      return next;
    });
  }, [selectedSection]);

  useEscapeStack(onClose, true);
  useEscapeStack(() => setConversationOpen(false), conversationOpen);
  useEscapeStack(() => setBriefingOpen(false), briefingOpen);

  const persistDraft = useCallback(async (nextDraft: DaemonTourDraft) => {
    setBusy('save');
    setError(null);
    setReviewSent(false);
    try {
      await saveTourDraft(tour.tour_id, nextDraft);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  }, [saveTourDraft, tour.tour_id]);

  const selectFile = useCallback((file: TourFile) => {
    setSelectedPath(file.path);
    const nextDraft = tour.drafts.find((entry) => entry.path === file.path) ?? emptyDraft(file.path);
    void persistDraft(nextDraft);
  }, [persistDraft, tour.drafts]);

  const selectNextUnreviewed = useCallback(() => {
    const reviewable = tour.files.filter((file) => file.group !== 'skip');
    if (reviewable.length === 0) return;
    const currentIndex = reviewable.findIndex((file) => file.path === selectedFile?.path);
    const ordered = [
      ...reviewable.slice(currentIndex + 1),
      ...reviewable.slice(0, Math.max(0, currentIndex + 1)),
    ];
    const next = ordered.find((file) => !reviewedPaths.has(file.path));
    if (next) selectFile(next);
  }, [reviewedPaths, selectFile, selectedFile?.path, tour.files]);

  const updateAnnotationReply = (annotationId: string, body: string) => {
    const replies = draft.annotation_replies.filter((reply) => reply.id !== annotationId);
    if (body.trim()) replies.push({ id: annotationId, body: body.trim() });
    void persistDraft({ ...draft, annotation_replies: replies });
  };

  const addLineComment = useCallback((lineStart: number, _lineEnd: number, content: string) => {
    const currentDraft = draftRef.current;
    return persistDraft({
      ...currentDraft,
      line_comments: [...currentDraft.line_comments, { line: lineStart, body: content }],
    });
  }, [persistDraft]);

  const draftWithPendingInputs = useCallback((): DaemonTourDraft => {
    const currentDraft = draftRef.current;
    if (!selectedFile) return currentDraft;
    const replies = currentDraft.annotation_replies.filter(
      (reply) => !selectedFile.annotations.some((annotation) => annotation.id === reply.id),
    );
    for (const annotation of selectedFile.annotations) {
      const key = `${selectedFile.path}:${annotation.id}`;
      const body = replyInputs[key]
        ?? currentDraft.annotation_replies.find((reply) => reply.id === annotation.id)?.body
        ?? '';
      if (body.trim()) replies.push({ id: annotation.id, body: body.trim() });
    }
    return {
      ...currentDraft,
      note: noteInputs[selectedFile.path] ?? currentDraft.note,
      annotation_replies: replies,
    };
  }, [noteInputs, replyInputs, selectedFile]);

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
      const submissionDraft = draftWithPendingInputs();
      let feedbackTour = tour;
      if (submissionDraft.path && !draftsMatch(submissionDraft, draft)) {
        await saveTourDraft(tour.tour_id, submissionDraft);
        feedbackTour = tourWithDraft(tour, submissionDraft);
      }
      await submitTour(tour.tour_id, feedbackMarkdown(feedbackTour), finish);
      if (!finish) setReviewSent(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  return (
    <section className="tour-panel" role="dialog" aria-modal="true" aria-label={`Code Tour: ${tour.name}`}>
      <header className="tour-panel__header">
        <div className="tour-panel__identity">
          <span className="tour-panel__mark" aria-hidden="true">AT</span>
          <div>
            <div className="tour-panel__eyebrow">Interactive code tour</div>
            <h2>{tour.name}</h2>
            <div className="tour-panel__meta">
              <span>Base <code>{tour.base_ref}</code></span>
              <span>{tour.files.length} changed {tour.files.length === 1 ? 'file' : 'files'}</span>
            </div>
          </div>
        </div>

        <div className="tour-panel__progress" aria-label={`${progressPercent}% of curated files reviewed`}>
          <div>
            <strong>{reviewedCurated}/{curatedFiles.length}</strong>
            <span>curated reviewed</span>
          </div>
          <span className="tour-panel__progress-track"><span style={{ width: `${progressPercent}%` }} /></span>
        </div>

        <div className="tour-panel__header-actions">
          <span className={`tour-panel__connection tour-panel__connection--${tour.connection_state}`}>
            <span className="tour-panel__connection-dot" aria-hidden="true" />
            {tour.connection_state === 'connected' ? 'Agent listening' : 'Agent disconnected'}
          </span>
          <button type="button" onClick={() => setBriefingOpen(true)}>Briefing</button>
          <button
            type="button"
            className={conversationOpen ? 'is-active' : ''}
            onClick={() => setConversationOpen((open) => !open)}
          >
            Conversation{tour.transcript.length > 0 ? ` ${tour.transcript.length}` : ''}
          </button>
          <button
            type="button"
            className="tour-panel__send-review"
            data-tour-submit
            aria-label="Send review to agent"
            onClick={() => void handleSubmit(false)}
            disabled={busy !== null || tour.status === 'ended'}
          >
            {busy === 'submit' ? 'Sending...' : reviewSent ? 'Review sent' : 'Send review'}
          </button>
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
          <button type="button" className="tour-panel__close" onClick={onClose} aria-label="Close tour">
            Close
          </button>
        </div>
      </header>

      {tour.connection_state === 'disconnected' && tour.status === 'active' ? (
        <div className="tour-panel__notice">
          The Tour is preserved. The agent can re-run <code>attn tour start</code> to reattach.
        </div>
      ) : null}
      {tour.warnings.length > 0 ? (
        <div className="tour-panel__warnings">{tour.warnings.join(' ')}</div>
      ) : null}
      {error ? <div className="tour-panel__error">{error}</div> : null}

      <div className="tour-panel__layout">
        <nav className="tour-panel__navigator" aria-label="Tour route">
          <section className="tour-panel__coverage">
            <div className="tour-panel__section-kicker">Change coverage</div>
            <div className="tour-panel__coverage-grid">
              <div><strong>{tour.files.length}</strong><span>Total</span></div>
              <div className="is-curated"><strong>{curatedFiles.length}</strong><span>Curated</span></div>
              <div className="is-other"><strong>{otherFiles.length}</strong><span>Other</span></div>
              <div className="is-skipped"><strong>{skippedFiles.length}</strong><span>Skipped</span></div>
            </div>
          </section>

          <section className="tour-panel__search">
            <input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search files or chapters"
              aria-label="Search Tour files or chapters"
            />
            <div className="tour-panel__filters">
              <button
                type="button"
                className={hotspotsOnly ? 'is-active' : ''}
                onClick={() => setHotspotsOnly((value) => !value)}
              >
                Hotspots
              </button>
              <button
                type="button"
                className={unreviewedOnly ? 'is-active' : ''}
                onClick={() => setUnreviewedOnly((value) => !value)}
              >
                Unreviewed
              </button>
            </div>
          </section>

          <div className="tour-panel__route">
            <div className="tour-panel__route-summary">
              <span>{filteredSections.reduce((count, section) => count + section.files.length, 0)} visible files</span>
              <button type="button" onClick={selectNextUnreviewed}>Next unreviewed</button>
            </div>
            {filteredSections.length === 0 ? (
              <div className="tour-panel__no-results">No files match these filters.</div>
            ) : filteredSections.map((section, sectionIndex) => {
              const expanded = expandedSections.has(section.id) || search.trim() !== '' || hotspotsOnly || unreviewedOnly;
              const reviewed = section.files.filter((file) => reviewedPaths.has(file.path)).length;
              const sectionProgress = section.files.length > 0 ? (reviewed / section.files.length) * 100 : 0;
              const active = section.files.some((file) => file.path === selectedFile?.path);
              return (
                <section
                  className={`tour-panel__chapter ${active ? 'is-active' : ''}`}
                  key={section.id}
                >
                  <button
                    type="button"
                    className="tour-panel__chapter-heading"
                    onClick={() => {
                      setExpandedSections((current) => {
                        const next = new Set(current);
                        if (next.has(section.id)) next.delete(section.id);
                        else next.add(section.id);
                        return next;
                      });
                    }}
                    aria-expanded={expanded}
                  >
                    <span className="tour-panel__chapter-number">
                      {section.group === 'tour' ? String(sectionIndex + 1).padStart(2, '0') : section.group.toUpperCase().slice(0, 2)}
                    </span>
                    <span className="tour-panel__chapter-copy">
                      <strong>{section.title}</strong>
                      <small>{section.summary || `${section.files.length} ${section.files.length === 1 ? 'file' : 'files'}`}</small>
                    </span>
                    <span className="tour-panel__chapter-state">
                      <span>{reviewed}/{section.files.length}</span>
                      <span className="tour-panel__chapter-progress"><span style={{ width: `${sectionProgress}%` }} /></span>
                    </span>
                  </button>
                  {expanded ? (
                    <div className="tour-panel__chapter-files">
                      {section.files.map((file) => {
                        const fileDraft = tour.drafts.find((entry) => entry.path === file.path);
                        return (
                          <button
                            type="button"
                            key={file.path}
                            data-file-path={file.path}
                            className={[
                              'tour-panel__file-row',
                              file.path === selectedFile?.path ? 'is-active' : '',
                              fileDraft?.reviewed ? 'is-reviewed' : '',
                              file.risk_note ? 'is-hotspot' : '',
                            ].filter(Boolean).join(' ')}
                            onClick={() => selectFile(file)}
                          >
                            <span className="tour-panel__file-copy">
                              <strong>{fileName(file.path)}</strong>
                              <small>{fileDirectory(file.path)}</small>
                            </span>
                            <span className="tour-panel__file-signals">
                              {file.risk_note ? <em>Hotspot</em> : null}
                              {fileDraft?.reviewed ? <em className="is-reviewed">Reviewed</em> : null}
                              <FileStats file={file} />
                            </span>
                          </button>
                        );
                      })}
                    </div>
                  ) : null}
                </section>
              );
            })}
          </div>

          <footer className="tour-panel__resume">
            <div>
              <strong>Resume point</strong>
              <span>{selectedFile?.path || 'No file selected'}</span>
            </div>
            <button type="button" onClick={selectNextUnreviewed}>Continue</button>
          </footer>
        </nav>

        <main className="tour-panel__main">
          {selectedFile ? (
            <>
              <header className="tour-panel__file-heading">
                <div>
                  <span className="tour-panel__file-status">
                    {selectedFile.status} / {selectedSection?.title || selectedFile.group}
                  </span>
                  <h3>{selectedFile.path}</h3>
                </div>
                <div className="tour-panel__file-actions">
                  <FileStats file={selectedFile} />
                  {selectedFile.risk_note ? <span className="tour-panel__hotspot-badge">Hotspot</span> : null}
                  {canRenderMarkdown ? (
                    <div className="tour-panel__view-toggle" role="group" aria-label="Markdown display mode">
                      <button
                        type="button"
                        className={renderMarkdown ? 'is-active' : ''}
                        aria-pressed={renderMarkdown}
                        onClick={() => {
                          setMarkdownRenderOverrides((current) => ({
                            ...current,
                            [selectedFile.path]: true,
                          }));
                        }}
                      >
                        Rendered
                      </button>
                      <button
                        type="button"
                        className={!renderMarkdown ? 'is-active' : ''}
                        aria-pressed={!renderMarkdown}
                        onClick={() => {
                          setMarkdownRenderOverrides((current) => ({
                            ...current,
                            [selectedFile.path]: false,
                          }));
                        }}
                      >
                        {selectedFile.view === 'content' ? 'Source' : 'Changes'}
                      </button>
                    </div>
                  ) : null}
                  <label>
                    <input
                      type="checkbox"
                      checked={draft.reviewed}
                      onChange={(event) => void persistDraft({ ...draft, reviewed: event.target.checked })}
                    />
                    Reviewed
                  </label>
                </div>
              </header>

              <section className="tour-panel__lens">
                <div className="tour-panel__lens-mark" aria-hidden="true">L</div>
                <div className="tour-panel__lens-content">
                  <div className="tour-panel__section-kicker">Reading lens</div>
                  {selectedFile.note ? (
                    <TourMarkdown resolvedTheme={resolvedTheme} uiScale={uiScale}>
                      {selectedFile.note}
                    </TourMarkdown>
                  ) : (
                    <p>
                      This file is part of {selectedSection?.title || 'the complete change set'}.
                      Read it in context with the surrounding chapter.
                    </p>
                  )}
                </div>
                <button type="button" onClick={selectNextUnreviewed}>Next unreviewed</button>
              </section>

              {selectedFile.risk_note ? (
                <section className="tour-panel__hotspot">
                  <div className="tour-panel__section-kicker">Review hotspot</div>
                  <TourMarkdown resolvedTheme={resolvedTheme} uiScale={uiScale}>
                    {selectedFile.risk_note}
                  </TourMarkdown>
                </section>
              ) : null}

              <div className={`tour-panel__code ${renderMarkdown ? 'tour-panel__code--markdown' : ''}`.trim()}>
                {renderMarkdown ? (
                  <article
                    className="tour-panel__markdown-preview"
                    aria-label={`Rendered Markdown: ${selectedFile.path}`}
                  >
                    <TourMarkdown resolvedTheme={resolvedTheme} uiScale={uiScale}>
                      {selectedFile.modified}
                    </TourMarkdown>
                  </article>
                ) : selectedFile.view === 'content' ? (
                  <pre><code>{selectedFile.modified}</code></pre>
                ) : (
                  <DiffView
                    original={selectedFile.original}
                    modified={selectedFile.modified}
                    filePath={selectedFile.path}
                    comments={diffComments}
                    editingCommentId={null}
                    resolvedTheme={resolvedTheme}
                    fontSize={13 * uiScale}
                    diffStyle="unified"
                    expandUnchanged={false}
                    onAddComment={addLineComment}
                    onEditComment={noop}
                    onStartEdit={noop}
                    onCancelEdit={noop}
                    onResolveComment={noop}
                    onDeleteComment={noop}
                  />
                )}
              </div>
            </>
          ) : <div className="tour-panel__empty">No changed files in this Tour.</div>}
        </main>
      </div>

      {conversationOpen ? (
        <aside className="tour-panel__conversation" aria-label="Tour conversation">
          <header className="tour-panel__conversation-heading">
            <div>
              <div className="tour-panel__section-kicker">Conversation</div>
              <h3>Questions and review notes</h3>
              <p>Your agent keeps listening while this drawer is closed.</p>
            </div>
            <button type="button" onClick={() => setConversationOpen(false)} aria-label="Close conversation">
              Close
            </button>
          </header>

          <div className="tour-panel__conversation-scroll">
            <section className="tour-panel__notes">
              <h4>File feedback</h4>
              <textarea
                value={noteInputs[draft.path] ?? draft.note}
                placeholder="Feedback on this file"
                onChange={(event) => {
                  setReviewSent(false);
                  setNoteInputs((current) => ({ ...current, [draft.path]: event.target.value }));
                }}
                onBlur={(event) => {
                  if ((event.relatedTarget as HTMLElement | null)?.closest('[data-tour-submit]')) return;
                  void persistDraft({ ...draft, note: event.target.value });
                }}
              />
            </section>

            {selectedFile?.annotations.map((annotation) => {
              const reply = draft.annotation_replies.find((entry) => entry.id === annotation.id)?.body || '';
              return (
                <section className="tour-panel__annotation" key={annotation.id}>
                  <h4>Lines {annotation.line_start}{annotation.line_end !== annotation.line_start ? `-${annotation.line_end}` : ''}</h4>
                  {annotation.comments.map((comment, index) => (
                    <div className="tour-panel__annotation-comment" key={`${annotation.id}:${index}`}>
                      <strong>{comment.author}</strong>
                      <TourMarkdown resolvedTheme={resolvedTheme} uiScale={uiScale}>
                        {comment.body}
                      </TourMarkdown>
                    </div>
                  ))}
                  <textarea
                    value={replyInputs[`${draft.path}:${annotation.id}`] ?? reply}
                    placeholder="Reply to this annotation"
                    onChange={(event) => {
                      setReviewSent(false);
                      const key = `${draft.path}:${annotation.id}`;
                      setReplyInputs((current) => ({ ...current, [key]: event.target.value }));
                    }}
                    onBlur={(event) => {
                      if ((event.relatedTarget as HTMLElement | null)?.closest('[data-tour-submit]')) return;
                      updateAnnotationReply(annotation.id, event.target.value);
                    }}
                  />
                </section>
              );
            })}

            <section className="tour-panel__question">
              <h4>Ask the agent</h4>
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
              <h4>Conversation history</h4>
              {tour.transcript.length === 0 ? <p>No questions yet.</p> : tour.transcript.map((entry) => (
                <div className={`tour-panel__message tour-panel__message--${entry.role}`} key={entry.id}>
                  <strong>{entry.role === 'agent' ? 'Agent' : 'You'}</strong>
                  <TourMarkdown resolvedTheme={resolvedTheme} uiScale={uiScale}>
                    {entry.body}
                  </TourMarkdown>
                </div>
              ))}
            </section>
          </div>

          <footer className="tour-panel__submit">
            <button type="button" data-tour-submit onClick={() => void handleSubmit(false)} disabled={busy !== null || tour.status === 'ended'}>
              {busy === 'submit' ? 'Sending...' : reviewSent ? 'Review sent' : 'Send review to agent'}
            </button>
            <button type="button" data-tour-submit className="tour-panel__end" onClick={() => void handleSubmit(true)} disabled={busy !== null || tour.status === 'ended'}>
              {tour.status === 'ended' ? 'Tour ended' : busy === 'finish' ? 'Ending...' : 'End tour'}
            </button>
            <small>Feedback stays local to attn. Nothing is submitted to GitHub.</small>
          </footer>
        </aside>
      ) : null}

      {briefingOpen ? (
        <div className="tour-panel__briefing-backdrop">
          <article className="tour-panel__briefing" role="dialog" aria-modal="true" aria-label="Tour briefing">
            <header>
              <div>
                <div className="tour-panel__section-kicker">Tour briefing</div>
                <h3>How to read this change</h3>
              </div>
              <button type="button" onClick={() => setBriefingOpen(false)} aria-label="Close briefing">Close</button>
            </header>
            <div className="tour-panel__briefing-content">
              <TourMarkdown resolvedTheme={resolvedTheme} uiScale={uiScale}>
                {tour.summary}
              </TourMarkdown>
            </div>
            <footer>
              <span>
                {curatedFiles.length} curated / {otherFiles.length} other / {skippedFiles.length} skipped
              </span>
              <button type="button" onClick={() => setBriefingOpen(false)}>Start reading</button>
            </footer>
          </article>
        </div>
      ) : null}
    </section>
  );
}
