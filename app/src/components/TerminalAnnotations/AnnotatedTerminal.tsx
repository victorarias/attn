// An agent pane's terminal, plus the annotation surface over it.
//
// The terminal paints; this owns everything else: which messages are
// annotatable, the popup a highlight opens, the panel that collects the set,
// the moment it is typed back into the session — and the persistence that makes
// all of it outlive the pane.
//
// Two inversions matter here. The message comes from the transcript, not the
// screen. And the annotations come from the daemon, not from this component's
// memory: every mutation is written through before it is anything else, so an
// unmount, a crash, or a quit costs nothing. See
// docs/decisions/2026-08-02-terminal-annotations-anchor-to-the-transcript.md.

import { forwardRef, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import {
  GhosttyTerminal,
  type GhosttyTerminalHandle,
  type GhosttyTerminalProps,
} from '../GhosttyTerminal';
import {
  TerminalAnnotationStore,
  type AnnotatableMessage,
  type MessageAnchor,
  type TerminalAnnotation,
} from '../../utils/terminalAnnotations';
import { QUICK_LABELS, buildAnnotationPayload } from './quickLabels';
import { clampToViewport, placePopup, type Placement } from './placement';
import type { UISessionState } from '../../types/sessionState';
import './TerminalAnnotations.css';

// The states in which the agent has stopped talking, so its newest message is
// complete and worth offering for annotation. Mid-turn the message is still
// being written and its offsets would be replaced moments later. Older messages
// in the window are settled by definition.
const SETTLED_STATES: ReadonlyArray<UISessionState> = ['idle', 'waiting_input', 'pending_approval'];

export interface SessionMessagesResult {
  messages: AnnotatableMessage[];
  truncated: boolean;
}

export interface SessionAnnotationsResult {
  annotations: TerminalAnnotation[];
  generation: number;
}

// The daemon calls this surface needs. Bundled rather than threaded one by one:
// annotations are useless without all four, and a partial set would silently
// degrade to the in-memory behaviour this exists to replace.
export interface SessionAnnotationApi {
  fetchMessages: (sessionId: string) => Promise<SessionMessagesResult>;
  fetchAnnotations: (sessionId: string) => Promise<SessionAnnotationsResult>;
  saveAnnotations: (
    sessionId: string,
    annotations: readonly TerminalAnnotation[],
    generation: number,
  ) => Promise<{ stale: boolean }>;
  clearAnnotations: (sessionId: string, generation: number) => Promise<{ generation: number }>;
}

type TerminalProps = Omit<
  GhosttyTerminalProps,
  'annotations' | 'annotationsVersion' | 'onAnnotationAnchor' | 'onAnnotationActivate'
>;

export interface AnnotatedTerminalProps extends TerminalProps {
  sessionId: string;
  // Drives when the message window is (re)fetched. Absent disables annotation.
  sessionState?: UISessionState;
  annotationApi?: SessionAnnotationApi;
  // Types the composed feedback into the session. Absent disables sending, so
  // the surface is never offered when there is nowhere for it to go.
  onSubmitAnnotations?: (text: string) => void;
}

interface Composer {
  annotationId: string;
  clientX: number;
  clientY: number;
  // The comment box only opens on 💬 or on reopening an annotation that has
  // one; the label row alone is a one-click gesture and must not be buried
  // under a textarea.
  writing: boolean;
}

export const AnnotatedTerminal = forwardRef<GhosttyTerminalHandle, AnnotatedTerminalProps>(
  function AnnotatedTerminal(
    { sessionId, sessionState, annotationApi, onSubmitAnnotations, ...terminalProps },
    ref,
  ) {
    // Built once. A `useRef(new TerminalAnnotationStore())` would construct a
    // store on every render and throw it away, which is waste on a component
    // that re-renders per repaint.
    const [store] = useState(() => new TerminalAnnotationStore());
    // Mutating the store is invisible to React; this is what re-renders the
    // panel and tells the terminal to repaint.
    const [version, setVersion] = useState(0);
    const [composer, setComposer] = useState<Composer | null>(null);
    const [sentCount, setSentCount] = useState(0);
    const [draft, setDraft] = useState('');
    const commentRef = useRef<HTMLTextAreaElement>(null);
    const popupRef = useRef<HTMLDivElement>(null);
    // Where the popup ended up after being fitted to the window. Null until it
    // has been measured, which is the first frame it exists.
    const [popupAt, setPopupAt] = useState<Placement | null>(null);
    const panelRef = useRef<HTMLDivElement>(null);
    // Null while the panel sits in its default corner; set once dragged.
    const [panelAt, setPanelAt] = useState<Placement | null>(null);
    const [panelDragging, setPanelDragging] = useState(false);
    const panelGrabRef = useRef<{ dx: number; dy: number } | null>(null);
    // The generation floor this client has to beat to write. Seeded by the
    // hydrate, raised by every save, and re-seeded whenever the daemon refuses
    // one as stale.
    const generationRef = useRef(0);
    const enabled = Boolean(annotationApi && onSubmitAnnotations);

    const bump = useCallback(() => setVersion((value) => value + 1), []);

    // Write the whole list through to the daemon. Called after every mutation
    // rather than debounced: the mutations are discrete clicks and committed
    // comments, not keystrokes, so there is no burst to smooth out — and no
    // window in which a pending write can be lost to an unmount.
    const persist = useCallback(() => {
      if (!annotationApi) return;
      generationRef.current += 1;
      const generation = generationRef.current;
      const annotations = store.list().map((entry) => ({ ...entry }));
      void annotationApi.saveAnnotations(sessionId, annotations, generation)
        .then((result) => {
          if (!result.stale) return;
          // Someone else's write won. Theirs is the truth; take it rather than
          // keep insisting on a list the store already rejected.
          return annotationApi.fetchAnnotations(sessionId).then((stored) => {
            store.hydrate(stored.annotations);
            generationRef.current = stored.generation;
            bump();
          });
        })
        .catch(() => {
          // The daemon is unreachable. What is on screen is still the user's
          // work; the next mutation retries the whole list.
        });
    }, [annotationApi, bump, sessionId, store]);

    // Hydrate before anything can be drawn: annotations made in an earlier app
    // run are the ones most likely to be forgotten about, so they have to be
    // there the moment the pane appears.
    useEffect(() => {
      if (!enabled || !sessionId) return;
      let cancelled = false;
      void annotationApi!.fetchAnnotations(sessionId)
        .then((stored) => {
          if (cancelled) return;
          store.hydrate(stored.annotations);
          generationRef.current = stored.generation;
          bump();
        })
        .catch(() => {
          // Nothing stored, or the daemon is unreachable. Starting empty is the
          // honest fallback; a later save re-establishes the row.
        });
      return () => {
        cancelled = true;
      };
    }, [annotationApi, bump, enabled, sessionId, store]);

    // Fetch the annotatable window when the agent settles. Messages already in
    // the window come back unchanged, so this neither disturbs the store nor
    // repaints unless a new turn actually arrived.
    useEffect(() => {
      if (!enabled || !sessionId) return;
      if (!sessionState || !SETTLED_STATES.includes(sessionState)) return;
      let cancelled = false;
      void annotationApi!.fetchMessages(sessionId)
        .then((result) => {
          if (cancelled) return;
          if (store.setMessages(result.messages)) bump();
        })
        .catch(() => {
          // A session with no readable transcript simply has nothing to
          // annotate. Leave whatever is already anchored alone.
        });
      return () => {
        cancelled = true;
      };
    }, [annotationApi, bump, enabled, sessionId, sessionState, store]);

    const closeComposer = useCallback(() => {
      setComposer(null);
      setPopupAt(null);
      setDraft('');
    }, []);

    // A highlight with neither a label nor a comment says nothing, so dismissing
    // the popup without choosing either removes it rather than leaving a blank
    // wash on the message.
    const dismissComposer = useCallback(() => {
      const current = composer;
      if (current) {
        const annotation = store.list().find((entry) => entry.id === current.annotationId);
        if (annotation && !annotation.emoji && !annotation.comment) {
          store.remove(current.annotationId);
          persist();
          bump();
        }
      }
      closeComposer();
    }, [bump, closeComposer, composer, persist, store]);

    const handleAnchor = useCallback(
      (anchor: MessageAnchor, at: { clientX: number; clientY: number }) => {
        const annotation = store.add(anchor.messageKey, anchor.start, anchor.end);
        if (!annotation) return;
        setDraft('');
        setPopupAt(null);
        setComposer({ annotationId: annotation.id, clientX: at.clientX, clientY: at.clientY, writing: false });
        bump();
      },
      [bump],
    );

    // Reopening an annotation already made. Its comment becomes the draft, and
    // a comment-carrying annotation opens straight into the editor: the user
    // clicked it to change what it says, not to hunt for the box.
    const openAnnotation = useCallback(
      (annotationId: string, at: { clientX: number; clientY: number }) => {
        const annotation = store.list().find((entry) => entry.id === annotationId);
        if (!annotation) return;
        setDraft(annotation.comment);
        setPopupAt(null);
        setComposer({
          annotationId,
          clientX: at.clientX,
          clientY: at.clientY,
          writing: Boolean(annotation.comment),
        });
      },
      [],
    );

    useEffect(() => {
      if (!composer) return;
      const onKey = (event: KeyboardEvent) => {
        if (event.key === 'Escape') {
          event.stopPropagation();
          dismissComposer();
        }
      };
      window.addEventListener('keydown', onKey, true);
      return () => window.removeEventListener('keydown', onKey, true);
    }, [composer, dismissComposer]);

    // A press anywhere else closes the popup. Capture phase, so the terminal's
    // own mousedown still runs afterwards and a click outside can start the next
    // selection in the same gesture rather than only dismissing.
    useEffect(() => {
      if (!composer) return;
      const onDown = (event: MouseEvent) => {
        if (popupRef.current?.contains(event.target as Node)) return;
        dismissComposer();
      };
      window.addEventListener('mousedown', onDown, true);
      return () => window.removeEventListener('mousedown', onDown, true);
    }, [composer, dismissComposer]);

    // Fit the popup to the window once it has a size. In a layout effect so the
    // corrected position is in place before the browser paints; measuring in a
    // plain effect would show one frame at the unclamped spot.
    useLayoutEffect(() => {
      const node = popupRef.current;
      if (!composer || !node) return;
      const rect = node.getBoundingClientRect();
      setPopupAt(placePopup(
        { x: composer.clientX, y: composer.clientY },
        { width: rect.width, height: rect.height },
        { width: window.innerWidth, height: window.innerHeight },
      ));
    }, [composer]);

    useEffect(() => {
      if (composer?.writing) commentRef.current?.focus();
    }, [composer?.writing, composer?.annotationId]);

    const annotations = store.list();
    const composed = composer
      ? annotations.find((entry) => entry.id === composer.annotationId) ?? null
      : null;

    const applyLabel = (emoji: string) => {
      if (!composed) return;
      const next = composed.emoji === emoji ? '' : emoji;
      store.update(composed.id, { emoji: next });
      // Toggling the only label off leaves an annotation that says nothing.
      // Drop it rather than leaving a wash the agent will never be told about.
      if (!next && !composed.comment) store.remove(composed.id);
      persist();
      bump();
      if (!composed.comment) closeComposer();
    };

    const saveComment = () => {
      if (!composed) return;
      store.update(composed.id, { comment: draft.trim() });
      if (!draft.trim() && !composed.emoji) store.remove(composed.id);
      persist();
      bump();
      closeComposer();
    };

    const removeAnnotation = (id: string) => {
      store.remove(id);
      if (composer?.annotationId === id) closeComposer();
      persist();
      bump();
    };

    const reopen = (annotation: TerminalAnnotation, event: React.MouseEvent) => {
      openAnnotation(annotation.id, { clientX: event.clientX, clientY: event.clientY });
    };

    // The panel is dragged by its header. It starts pinned to a corner and only
    // becomes positioned once moved, so the default costs no state.
    const startPanelDrag = (event: React.MouseEvent) => {
      const node = panelRef.current;
      if (!node || event.button !== 0) return;
      const rect = node.getBoundingClientRect();
      panelGrabRef.current = { dx: event.clientX - rect.left, dy: event.clientY - rect.top };
      setPanelAt({ left: rect.left, top: rect.top });
      setPanelDragging(true);
      event.preventDefault();
    };

    const send = () => {
      if (annotations.length === 0 || !onSubmitAnnotations) return;
      const payload = buildAnnotationPayload(annotations.map((entry) => ({
        quote: entry.quote,
        emoji: entry.emoji,
        comment: entry.comment,
        start: entry.start,
      })));
      onSubmitAnnotations(payload);
      setSentCount(annotations.length);
      store.clear();
      closeComposer();
      bump();
      // A tombstone rather than a save of the empty list: it also refuses any
      // save that was already in flight, so sent marks cannot reappear.
      if (annotationApi) {
        generationRef.current += 1;
        void annotationApi.clearAnnotations(sessionId, generationRef.current)
          .then((result) => {
            generationRef.current = Math.max(generationRef.current, result.generation);
          })
          .catch(() => {
            // The marks are typed into the session either way; the next
            // mutation's save re-establishes the row.
          });
      }
    };

    // The confirmation replaces the panel's footer rather than a toast, and only
    // for as long as it takes to read: the panel is where the user was looking.
    useEffect(() => {
      if (sentCount === 0) return;
      const timer = window.setTimeout(() => setSentCount(0), 2200);
      return () => window.clearTimeout(timer);
    }, [sentCount]);

    // The drag runs on the window, not the header: the pointer routinely leaves
    // a 300px panel mid-drag, and a header-bound listener would drop it there.
    useEffect(() => {
      if (!panelDragging) return;
      const onMove = (event: MouseEvent) => {
        const grab = panelGrabRef.current;
        const node = panelRef.current;
        if (!grab || !node) return;
        const rect = node.getBoundingClientRect();
        setPanelAt(clampToViewport(
          { left: event.clientX - grab.dx, top: event.clientY - grab.dy },
          { width: rect.width, height: rect.height },
          { width: window.innerWidth, height: window.innerHeight },
        ));
      };
      const onUp = () => setPanelDragging(false);
      window.addEventListener('mousemove', onMove);
      window.addEventListener('mouseup', onUp);
      return () => {
        window.removeEventListener('mousemove', onMove);
        window.removeEventListener('mouseup', onUp);
      };
    }, [panelDragging]);

    // A window that shrinks under a dragged panel would strand it off-screen,
    // where nothing can reach it again.
    useEffect(() => {
      if (!panelAt) return;
      const onResize = () => {
        const node = panelRef.current;
        if (!node) return;
        const rect = node.getBoundingClientRect();
        setPanelAt((current) => (current ? clampToViewport(
          current,
          { width: rect.width, height: rect.height },
          { width: window.innerWidth, height: window.innerHeight },
        ) : current));
      };
      window.addEventListener('resize', onResize);
      return () => window.removeEventListener('resize', onResize);
    }, [panelAt]);

    const panelOpen = annotations.length > 0 || sentCount > 0;

    return (
      <>
        <GhosttyTerminal
          {...terminalProps}
          ref={ref}
          annotations={enabled ? store : undefined}
          annotationsVersion={version}
          onAnnotationAnchor={enabled ? handleAnchor : undefined}
          onAnnotationActivate={enabled ? openAnnotation : undefined}
        />
        {composed && composer ? (
          <div
            ref={popupRef}
            className={`anno-popup${popupAt ? ' anno-popup--placed' : ''}`}
            data-testid="annotation-popup"
            style={popupAt
              ? { left: popupAt.left, top: popupAt.top }
              : { left: composer.clientX, top: composer.clientY }}
            onMouseDown={(event) => event.preventDefault()}
          >
            <div className="anno-popup-labels">
              {QUICK_LABELS.map((label) => (
                <button
                  key={label.id}
                  type="button"
                  className={`anno-popup-label${composed.emoji === label.emoji ? ' anno-popup-label--on' : ''}`}
                  title={label.text}
                  aria-label={label.text}
                  onClick={() => applyLabel(label.emoji)}
                >
                  {label.emoji}
                </button>
              ))}
              <span className="anno-popup-divider" />
              <button
                type="button"
                className="anno-popup-label"
                title="Write a comment"
                aria-label="Write a comment"
                onClick={() => setComposer((current) => (current ? { ...current, writing: true } : current))}
              >
                💬
              </button>
              <button
                type="button"
                className="anno-popup-label anno-popup-label--delete"
                title="Remove this annotation"
                aria-label="Remove this annotation"
                onClick={() => removeAnnotation(composed.id)}
              >
                🗑
              </button>
            </div>
            {composer.writing ? (
              <div className="anno-popup-compose">
                <blockquote className="anno-popup-quote">{composed.quote}</blockquote>
                <textarea
                  ref={commentRef}
                  className="anno-popup-text"
                  value={draft}
                  // The placeholder disappears on the first keystroke, so it
                  // cannot be the only name this box has.
                  aria-label="Annotation comment"
                  placeholder="What should change here?"
                  onChange={(event) => setDraft(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
                      event.preventDefault();
                      saveComment();
                    }
                  }}
                />
                <div className="anno-popup-actions">
                  <button type="button" className="anno-popup-cancel" onClick={dismissComposer}>
                    Cancel
                  </button>
                  <button type="button" className="anno-popup-save" onClick={saveComment}>
                    Comment
                  </button>
                </div>
              </div>
            ) : null}
          </div>
        ) : null}
        {panelOpen ? (
          <div
            ref={panelRef}
            className={`anno-panel${panelDragging ? ' anno-panel--dragging' : ''}`}
            data-testid="annotation-panel"
            style={panelAt ? { left: panelAt.left, top: panelAt.top, right: 'auto', bottom: 'auto' } : undefined}
          >
            <div className="anno-panel-head" onMouseDown={startPanelDrag}>
              <span className="anno-panel-grip" aria-hidden="true">⠿</span>
              <span className="anno-panel-title">Annotations</span>
              <span className="anno-panel-count">{annotations.length}</span>
            </div>
            <div className="anno-panel-body">
              {annotations.map((annotation) => (
                <div
                  key={annotation.id}
                  className="anno-card"
                  role="button"
                  tabIndex={0}
                  onClick={(event) => reopen(annotation, event)}
                >
                  <span className="anno-card-chip">
                    {annotation.emoji || '💬'}
                  </span>
                  <span className="anno-card-quote">{annotation.quote}</span>
                  {annotation.comment ? (
                    <span className="anno-card-comment">{annotation.comment}</span>
                  ) : null}
                  <button
                    type="button"
                    className="anno-card-remove"
                    aria-label="Remove annotation"
                    onClick={(event) => {
                      event.stopPropagation();
                      removeAnnotation(annotation.id);
                    }}
                  >
                    ✕
                  </button>
                </div>
              ))}
            </div>
            <div className="anno-panel-foot">
              {sentCount > 0 ? (
                <span className="anno-panel-sent">✓ typed {sentCount} into the session</span>
              ) : (
                <>
                  <span className="anno-panel-n">
                    {annotations.length} annotation{annotations.length === 1 ? '' : 's'}
                  </span>
                  <button type="button" className="anno-panel-send" onClick={send}>
                    Send all
                  </button>
                </>
              )}
            </div>
          </div>
        ) : null}
      </>
    );
  },
);
