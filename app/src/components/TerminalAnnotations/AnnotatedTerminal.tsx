// An agent pane's terminal, plus the annotation surface over it: message window,
// popup, panel, and the send that types the set back into the session.
//
// Two inversions: the message comes from the transcript, not the screen, and the
// annotations come from the daemon, not this component's memory — every mutation
// is written through first. See
// docs/decisions/2026-08-02-terminal-annotations-anchor-to-the-transcript.md.

import React, { forwardRef, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
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
import { QUICK_LABEL_GROUPS, buildAnnotationPayload, labelByEmoji } from './quickLabels';
import { clampToViewport, placePopup, type PlaceOptions, type Placement } from './placement';
import { useShortcut } from '../../shortcuts/useShortcut';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import './TerminalAnnotations.css';

export interface SessionMessagesResult {
  messages: AnnotatableMessage[];
  status: 'discovering' | 'ready' | 'unavailable';
  detail?: string;
  truncated: boolean;
}

export interface SessionAnnotationsResult {
  annotations: TerminalAnnotation[];
  // What the user wants to say about the turn as a whole; empty when none.
  note: string;
  generation: number;
}

// What a send did. `delivered` is the only outcome that spent the marks.
export type SessionAnnotationsSubmitStatus =
  | 'delivered'
  | 'skipped_pending_approval'
  | (string & {});

// The daemon calls this surface needs. Omitting the whole surface disables
// annotations for terminal-only mounts.
export interface SessionAnnotationApi {
  fetchMessages: (sessionId: string) => Promise<SessionMessagesResult>;
  subscribeMessagesChanged: (sessionId: string, listener: () => void) => () => void;
  fetchAnnotations: (sessionId: string) => Promise<SessionAnnotationsResult>;
  saveAnnotations: (
    sessionId: string,
    annotations: readonly TerminalAnnotation[],
    note: string,
    generation: number,
  ) => Promise<{ stale: boolean }>;
  clearAnnotations: (sessionId: string, generation: number) => Promise<{ generation: number }>;
  // Type the composed feedback into the session AND submit it. Daemon-side so it
  // can refuse while an approval prompt is up, and so the Enter lands as a
  // keypress rather than as pasted text.
  submitAnnotations: (
    sessionId: string,
    text: string,
  ) => Promise<{ status: SessionAnnotationsSubmitStatus }>;
}

type TerminalProps = Omit<
  GhosttyTerminalProps,
  'annotations' | 'annotationsVersion' | 'onAnnotationAnchor' | 'onAnnotationActivate'
>;

export interface AnnotatedTerminalProps extends TerminalProps {
  sessionId: string;
  annotationApi?: SessionAnnotationApi;
  // Gates the send shortcut's *registration*: the dispatcher consumes ⌘Enter
  // whenever a handler exists, so every mounted pane registering would eat it.
  paneActive?: boolean;
}

interface Notice {
  text: string;
  clientX: number;
  clientY: number;
  // Distinguishes one miss from the next at the same spot, restarting the timer.
  seq: number;
}

// The footer's whole vocabulary. `sending` exists because the delivery is a
// round trip with a pause in it, and an idle-looking button gets pressed twice.
type SendOutcome =
  | { kind: 'sending' }
  // Annotated mid-flight, so not part of the send. Reported, not assumed zero:
  // a panel that does not empty otherwise reads as a failure.
  | { kind: 'sent'; count: number; kept: number }
  | { kind: 'skipped' }
  | { kind: 'error'; message: string };

interface Composer {
  annotationId: string;
  clientX: number;
  clientY: number;
  // Opens only on 💬, on reopening an annotation with one, or on a panel click:
  // the label row alone must stay a one-click gesture.
  writing: boolean;
}

export const AnnotatedTerminal = forwardRef<GhosttyTerminalHandle, AnnotatedTerminalProps>(
  function AnnotatedTerminal(
    { sessionId, annotationApi, paneActive = false, ...terminalProps },
    ref,
  ) {
    // Built once: `useRef(new …)` would construct and discard a store per render.
    const [store] = useState(() => new TerminalAnnotationStore());
    // Mutating the store is invisible to React; this drives the repaint.
    const [version, setVersion] = useState(0);
    const [composer, setComposer] = useState<Composer | null>(null);
    // What the last send did, shown in the panel's footer. Null between sends.
    const [outcome, setOutcome] = useState<SendOutcome | null>(null);
    // Guards the round trip: the send is reachable from the button and ⌘Enter.
    const sendingRef = useRef(false);
    // What a gesture that resolved to nothing has to say, and where. Null
    // otherwise: it answers a question the pointer just asked.
    const [notice, setNotice] = useState<Notice | null>(null);
    const noticeRef = useRef<HTMLDivElement>(null);
    const [noticeAt, setNoticeAt] = useState<Placement | null>(null);
    // Why the last window fetch produced nothing, in the daemon's words. A ref:
    // re-rendering the terminal to record a failed background fetch repaints
    // for no one.
    const windowErrorRef = useRef<string | null>(null);
    const windowStatusRef = useRef<SessionMessagesResult['status']>('discovering');
    const windowDetailRef = useRef<string | null>(null);
    const [draft, setDraft] = useState('');

    // What the chip row is about to do, named in words under it, because the
    // native tooltip arrives a second late. Always drawn, so naming a chip
    // cannot change the popup's height and move it out from under the pointer.
    const [hint, setHint] = useState<string | null>(null);
    const hintProps = useCallback((text: string) => ({
      onMouseEnter: () => setHint(text),
      onMouseLeave: () => setHint((current) => (current === text ? null : current)),
      onFocus: () => setHint(text),
      onBlur: () => setHint((current) => (current === text ? null : current)),
    }), []);
    // The note the set is sent with. Mirrored into a ref because the writes
    // carrying it read it in the same tick as a state update that has not
    // landed, which would re-save what was just spent.
    const [note, setNote] = useState('');
    const noteRef = useRef('');
    const writeNote = useCallback((next: string) => {
      noteRef.current = next;
      setNote(next);
    }, []);
    // A pending keystroke and its timer, so an unmount flushes it.
    const noteSaveTimerRef = useRef<number | null>(null);
    const commentRef = useRef<HTMLTextAreaElement>(null);
    const popupRef = useRef<HTMLDivElement>(null);
    // Where the popup landed after fitting; null until its first measured frame.
    const [popupAt, setPopupAt] = useState<Placement | null>(null);
    const panelRef = useRef<HTMLDivElement>(null);
    // Null while the panel sits in its default corner; set once dragged.
    const [panelAt, setPanelAt] = useState<Placement | null>(null);
    const [panelDragging, setPanelDragging] = useState(false);
    const panelGrabRef = useRef<{ dx: number; dy: number } | null>(null);
    // The generation floor a write must beat: seeded by the hydrate, raised by
    // every save, re-seeded whenever the daemon refuses one as stale.
    const generationRef = useRef(0);
    const enabled = Boolean(annotationApi);

    // The surface borrows the keyboard and must give it back, so it keeps its
    // own terminal handle alongside whatever the owner asked for.
    const terminalRef = useRef<GhosttyTerminalHandle | null>(null);
    const attachTerminal = useCallback((handle: GhosttyTerminalHandle | null) => {
      terminalRef.current = handle;
      if (typeof ref === 'function') ref(handle);
      else if (ref) (ref as React.MutableRefObject<GhosttyTerminalHandle | null>).current = handle;
    }, [ref]);

    const bump = useCallback(() => setVersion((value) => value + 1), []);

    // Write the whole list through after every mutation rather than debouncing:
    // clicks have no burst to smooth out, and nothing is lost to an unmount.
    const persist = useCallback(() => {
      if (!annotationApi) return;
      generationRef.current += 1;
      const generation = generationRef.current;
      const annotations = store.list().map((entry) => ({ ...entry }));
      void annotationApi.saveAnnotations(sessionId, annotations, noteRef.current, generation)
        .then((result) => {
          if (!result.stale) return;
          // Someone else's write won; take theirs rather than keep insisting.
          return annotationApi.fetchAnnotations(sessionId).then((stored) => {
            store.hydrate(stored.annotations);
            writeNote(stored.note);
            generationRef.current = stored.generation;
            bump();
          });
        })
        .catch(() => {
          // Unreachable daemon: the next mutation retries the whole list.
        });
    }, [annotationApi, bump, sessionId, store, writeNote]);

    // The latest persist, for the flush on the way out: the cleanup registers
    // once, and writing after commit keeps a discarded render's copy out.
    const persistRef = useRef(persist);
    useLayoutEffect(() => {
      persistRef.current = persist;
    }, [persist]);

    // Written through on a typing pause, not per keystroke. Measured: 400ms
    // clears an ordinary inter-keystroke gap (~100-250ms).
    const NOTE_SAVE_PAUSE_MS = 400;
    const scheduleNoteSave = useCallback(() => {
      if (noteSaveTimerRef.current !== null) window.clearTimeout(noteSaveTimerRef.current);
      noteSaveTimerRef.current = window.setTimeout(() => {
        noteSaveTimerRef.current = null;
        persistRef.current();
      }, NOTE_SAVE_PAUSE_MS);
    }, []);

    // A pane closed mid-sentence flushes its pending write on the way out.
    const flushNoteSave = useCallback(() => {
      if (noteSaveTimerRef.current === null) return;
      window.clearTimeout(noteSaveTimerRef.current);
      noteSaveTimerRef.current = null;
      persistRef.current();
    }, []);
    useEffect(() => flushNoteSave, [flushNoteSave]);

    // Hydrate before anything is drawn, so an earlier run's annotations are
    // there the moment the pane appears.
    useEffect(() => {
      if (!enabled || !sessionId) return;
      let cancelled = false;
      void annotationApi!.fetchAnnotations(sessionId)
        .then((stored) => {
          if (cancelled) return;
          store.hydrate(stored.annotations);
          writeNote(stored.note);
          generationRef.current = stored.generation;
          bump();
        })
        .catch(() => {
          // Nothing stored, or unreachable; a later save re-establishes the row.
        });
      return () => {
        cancelled = true;
      };
    }, [annotationApi, bump, enabled, sessionId, store]);

    // Fetch once on mount, then whenever the watcher records a complete
    // assistant entry. The event is only an invalidation; this canonical read
    // still owns stable keys, ordering, and limits. Concurrent reads are
    // last-result-wins so an older window cannot replace a newer one.
    useEffect(() => {
      if (!enabled || !sessionId) return;
      let cancelled = false;
      let request = 0;
      const refresh = () => {
        const current = ++request;
        void annotationApi!.fetchMessages(sessionId)
          .then((result) => {
            if (cancelled || current !== request) return;
            windowErrorRef.current = null;
            windowStatusRef.current = result.status;
            windowDetailRef.current = result.detail ?? null;
            if (store.setMessages(result.messages)) bump();
          })
          .catch((error: unknown) => {
            if (cancelled || current !== request) return;
            const detail = error instanceof Error ? error.message : String(error);
            windowStatusRef.current = 'unavailable';
            windowDetailRef.current = detail;
            windowErrorRef.current = detail;
            // Logged as well as shown: the daemon's wording is what is searchable.
            console.warn(`[annotations] ${sessionId}: message window unavailable: ${detail}`);
          });
      };
      const unsubscribe = annotationApi!.subscribeMessagesChanged(sessionId, refresh);
      refresh();
      return () => {
        cancelled = true;
        unsubscribe();
      };
    }, [annotationApi, bump, enabled, sessionId, store]);

    // Closing hands the keyboard back to the terminal, unless a press landed
    // elsewhere deliberately: that press owns where focus goes.
    const closeComposer = useCallback((restoreFocus = true) => {
      setComposer(null);
      setPopupAt(null);
      setDraft('');
      if (restoreFocus) terminalRef.current?.focus();
    }, []);

    // A highlight with neither label nor comment says nothing, so dismissing
    // without either removes it rather than leaving a blank wash.
    const dismissComposer = useCallback((restoreFocus = true) => {
      const current = composer;
      if (current) {
        const annotation = store.list().find((entry) => entry.id === current.annotationId);
        if (annotation && !annotation.emoji && !annotation.comment) {
          store.remove(current.annotationId);
          persist();
          bump();
        }
      }
      closeComposer(restoreFocus);
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

    // A gesture that resolved to nothing. Four causes look identical on screen
    // and each has a different remedy, so the notice names the one that applies.
    const missSeqRef = useRef(0);
    const handleMiss = useCallback(
      (reason: 'no-messages' | 'outside-messages', at: { clientX: number; clientY: number }) => {
        const text = reason === 'outside-messages'
          ? 'Only what the agent wrote can be annotated. This text is the TUI’s own, your own, or from a turn that has scrolled out of the window.'
          : windowErrorRef.current
            ? 'No transcript could be read for this session, so there is nothing to annotate. The daemon log names the lookup that failed.'
            : windowStatusRef.current === 'discovering'
              ? 'The agent’s first completed message is still being recorded. Try again in a moment.'
              : windowStatusRef.current === 'unavailable'
                ? windowDetailRef.current || 'No exact transcript is available for this session.'
              : 'The agent has not written a message to annotate yet.';
        setNoticeAt(null);
        setNotice({ text, ...at, seq: missSeqRef.current++ });
      },
      [],
    );

    // Reopening an annotation: its comment becomes the draft, and one that
    // carries a comment opens straight into the editor. `writing` forces the
    // editor open regardless, which is what the panel passes.
    const openAnnotation = useCallback(
      (
        annotationId: string,
        at: { clientX: number; clientY: number },
        options?: { writing?: boolean },
      ) => {
        const annotation = store.list().find((entry) => entry.id === annotationId);
        if (!annotation) return;
        setDraft(annotation.comment);
        setPopupAt(null);
        setComposer({
          annotationId,
          clientX: at.clientX,
          clientY: at.clientY,
          writing: options?.writing || Boolean(annotation.comment),
        });
      },
      [store],
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

    // A press elsewhere closes the popup; capture phase, so the terminal's own
    // mousedown still runs and the click can start the next selection.
    useEffect(() => {
      if (!composer) return;
      const onDown = (event: MouseEvent) => {
        if (popupRef.current?.contains(event.target as Node)) return;
        // The press decides where focus lands, including one in the panel.
        dismissComposer(false);
      };
      window.addEventListener('mousedown', onDown, true);
      return () => window.removeEventListener('mousedown', onDown, true);
    }, [composer, dismissComposer]);

    // What placement respects beyond the window: the popup's pane and the panel
    // it must not cover, both read at placement time since both move.
    const placementOptions = useCallback((): PlaceOptions => ({
      bounds: terminalRef.current?.getBounds() ?? null,
      avoid: panelRef.current?.getBoundingClientRect() ?? null,
    }), []);

    // Fit a pointer-anchored floater to the pane once it has a size; idempotent
    // and cheap, so every trigger that can change the answer just calls it.
    const fitToPane = useCallback((
      node: HTMLElement | null,
      anchor: { clientX: number; clientY: number } | null,
      apply: (next: Placement) => void,
    ) => {
      if (!node || !anchor) return;
      const rect = node.getBoundingClientRect();
      apply(placePopup(
        { x: anchor.clientX, y: anchor.clientY },
        { width: rect.width, height: rect.height },
        { width: window.innerWidth, height: window.innerHeight },
        placementOptions(),
      ));
    }, [placementOptions]);

    // A ref: the resize observer and window listener fire outside React's render.
    const composerRef = useRef<Composer | null>(null);

    // Only writes when the answer moved: a no-op re-render would repaint the
    // pane underneath for nothing.
    const applyPopupAt = useCallback((next: Placement) => {
      setPopupAt((current) => (
        current && current.left === next.left && current.top === next.top ? current : next
      ));
    }, []);

    const repositionPopup = useCallback(() => {
      fitToPane(popupRef.current, composerRef.current, applyPopupAt);
    }, [applyPopupAt, fitToPane]);

    // A layout effect, so the corrected position lands before paint. `version`
    // and the panel geometry are dependencies: both move what it steps around.
    useLayoutEffect(() => {
      composerRef.current = composer;
      repositionPopup();
    }, [composer, panelAt, repositionPopup, version]);

    // The popup changes size under its own feet (the box opens, the textarea is
    // dragged taller, a font loads), and none of those is a render this sees.
    useEffect(() => {
      const node = popupRef.current;
      if (!composer || !node || typeof ResizeObserver === 'undefined') return;
      const observer = new ResizeObserver(() => repositionPopup());
      observer.observe(node);
      return () => observer.disconnect();
    }, [composer, repositionPopup]);

    useEffect(() => {
      if (!composer) return;
      window.addEventListener('resize', repositionPopup);
      return () => window.removeEventListener('resize', repositionPopup);
    }, [composer, repositionPopup]);

    // The box takes the keyboard as it appears, caret at the end: reopening a
    // comment is for adding to it, and focus() alone lands at the top.
    useEffect(() => {
      if (!composer?.writing) return;
      const box = commentRef.current;
      if (!box) return;
      box.focus();
      box.setSelectionRange(box.value.length, box.value.length);
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

    // `restoreFocus` false for the panel's remove: the next click is another row.
    const removeAnnotation = (id: string, restoreFocus = true) => {
      store.remove(id);
      if (composer?.annotationId === id) closeComposer(restoreFocus);
      persist();
      bump();
    };

    const reopen = (annotation: TerminalAnnotation, at: { clientX: number; clientY: number }) => {
      openAnnotation(annotation.id, at, { writing: true });
    };

    // A popup opened from the panel points at the card's top edge, not the
    // pointer, so it does not land somewhere new on every click.
    const reopenFromCard = (annotation: TerminalAnnotation, event: React.MouseEvent) => {
      const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
      reopen(annotation, { clientX: rect.left + rect.width / 2, clientY: rect.top });
    };

    // Dragged by its header; pinned to a corner until moved, costing no state.
    const startPanelDrag = (event: React.MouseEvent) => {
      const node = panelRef.current;
      if (!node || event.button !== 0) return;
      const rect = node.getBoundingClientRect();
      panelGrabRef.current = { dx: event.clientX - rect.left, dy: event.clientY - rect.top };
      setPanelAt({ left: rect.left, top: rect.top });
      setPanelDragging(true);
      event.preventDefault();
    };

    // Delivers the set and submits it. The marks are spent only on `delivered`:
    // clearing undelivered work is the one failure the user cannot undo.
    const send = () => {
      if (annotations.length === 0 || !annotationApi || sendingRef.current) return;
      // Land any note still waiting on its typing pause before composing.
      flushNoteSave();
      // A comment being typed when the send fires is part of what was meant.
      if (composed && composer?.writing) {
        const comment = draft.trim();
        store.update(composed.id, { comment });
        if (!comment && !composed.emoji) store.remove(composed.id);
      }
      // Re-read after that commit; the render's list predates it. Copied, not
      // referenced: `list()` hands back the store's own array, and this is held
      // across the round trip, where a mark made mid-flight must not land.
      const sending = store.list().map((entry) => ({ ...entry }));
      closeComposer();
      bump();
      if (sending.length === 0) {
        persist();
        return;
      }
      // Snapshotted for the same reason: the box stays typable across the trip.
      const sendingNote = note.trim();
      const payload = buildAnnotationPayload(sending.map((entry) => ({
        quote: entry.quote,
        emoji: entry.emoji,
        comment: entry.comment,
        start: entry.start,
      })), sendingNote);
      sendingRef.current = true;
      setOutcome({ kind: 'sending' });
      void annotationApi.submitAnnotations(sessionId, payload)
        .then((result) => {
          if (result.status !== 'delivered') {
            setOutcome(result.status === 'skipped_pending_approval'
              ? { kind: 'skipped' }
              : { kind: 'error', message: 'The session did not take the feedback. Nothing was sent.' });
            return;
          }
          // The surface stays live across the round trip, so an entry is spent
          // only if it still reads exactly as when composed.
          const current = new Map(store.list().map((entry) => [entry.id, entry]));
          sending.forEach((entry) => {
            const now = current.get(entry.id);
            if (!now) return;
            if (now.emoji !== entry.emoji || now.comment !== entry.comment) return;
            store.remove(entry.id);
          });
          // Same rule for the note: typed over mid-flight, it belongs to the next.
          if (sendingNote && noteRef.current.trim() === sendingNote) writeNote('');
          const kept = store.list().length;
          setOutcome({ kind: 'sent', count: sending.length, kept });
          bump();
          // Either way the generation is raised, refusing a save already in
          // flight. A tombstone only when nothing survived — including a note
          // typed over mid-flight, since the clear zeroes the note column.
          if (kept > 0 || noteRef.current.trim()) {
            persist();
            return;
          }
          generationRef.current += 1;
          return annotationApi.clearAnnotations(sessionId, generationRef.current)
            .then((cleared) => {
              generationRef.current = Math.max(generationRef.current, cleared.generation);
            })
            .catch(() => {
              // The feedback is in the session either way; the next save re-adds it.
            });
        })
        .catch((error: unknown) => {
          setOutcome({
            kind: 'error',
            message: error instanceof Error ? error.message : 'Send failed',
          });
        })
        .finally(() => {
          sendingRef.current = false;
        });
    };

    // Registration-gated, not handler-gated: the dispatcher consumes ⌘Enter
    // whenever a handler is registered, so an always-on no-op would swallow the
    // terminal's. `editableTarget: 'native'` keeps it out of the comment box.
    useShortcut(
      'terminal.sendAnnotations',
      send,
      enabled && paneActive && annotations.length > 0,
    );

    // The confirmation lives in the panel's footer and expires on its own. A
    // refusal does not: it is why the marks are still there.
    useEffect(() => {
      if (outcome?.kind !== 'sent') return;
      const timer = window.setTimeout(() => setOutcome(null), 2200);
      return () => window.clearTimeout(timer);
    }, [outcome]);

    // Same measure-then-fit as the popup: the pointer is routinely near an edge.
    useLayoutEffect(() => {
      fitToPane(noticeRef.current, notice, setNoticeAt);
    }, [fitToPane, notice]);

    // Long enough to read a sentence: it explains a gesture already finished, so
    // it must not become something to dismiss.
    useEffect(() => {
      if (!notice) return;
      const timer = window.setTimeout(() => setNotice(null), 5000);
      return () => window.clearTimeout(timer);
    }, [notice]);

    // A successful annotation answers the question the notice was asking.
    useEffect(() => {
      if (composer) setNotice(null);
    }, [composer]);

    // The drag runs on the window: the pointer routinely leaves a 300px panel.
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

    // A window shrinking under a dragged panel would strand it off-screen.
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

    const panelOpen = annotations.length > 0 || outcome?.kind === 'sent';

    // The sentence above the footer: why the marks survived a refusal, or why
    // the panel did not empty after a partial send.
    const outcomeText = outcome?.kind === 'skipped'
      ? 'Not sent — the session is waiting on an approval, where the sending Enter would answer it. Send again once you have answered.'
      : outcome?.kind === 'error'
        ? outcome.message
        : outcome?.kind === 'sent' && outcome.kept > 0
          ? `✓ sent ${outcome.count} to the session. ${outcome.kept} still here — annotated or changed while it was sending, so not part of what went.`
          : null;

    return (
      <>
        <GhosttyTerminal
          {...terminalProps}
          ref={attachTerminal}
          annotations={enabled ? store : undefined}
          annotationsVersion={version}
          onAnnotationAnchor={enabled ? handleAnchor : undefined}
          onAnnotationMiss={enabled ? handleMiss : undefined}
          onAnnotationActivate={enabled ? openAnnotation : undefined}
        />
        {/* Portalled out of the pane. Both of these are positioned against the
            window and routinely reach past the pane's edge — a popup on the
            first column of a line overlaps the sidebar — and inside the pane's
            stacking context that is drawn under the app's chrome rather than
            over it. There is nothing to inherit from the pane here: they carry
            their own position and their own z-index. */}
        {createPortal(
          <>
        {notice ? (
          <div
            ref={noticeRef}
            className={`anno-notice${noticeAt ? ' anno-notice--placed' : ''}`}
            data-testid="annotation-notice"
            role="status"
            style={noticeAt
              ? { left: noticeAt.left, top: noticeAt.top }
              : { left: notice.clientX, top: notice.clientY }}
          >
            {notice.text}
          </div>
        ) : null}
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
              {QUICK_LABEL_GROUPS.map((group, groupIndex) => (
                <React.Fragment key={group[0].id}>
                  {groupIndex > 0 ? <span className="anno-popup-divider" /> : null}
                  {group.map((label) => (
                    <button
                      key={label.id}
                      type="button"
                      className={`anno-popup-label${composed.emoji === label.emoji ? ' anno-popup-label--on' : ''}`}
                      title={label.text}
                      aria-label={label.text}
                      onClick={() => applyLabel(label.emoji)}
                      {...hintProps(label.text)}
                    >
                      {label.emoji}
                    </button>
                  ))}
                </React.Fragment>
              ))}
              <span className="anno-popup-divider" />
              <button
                type="button"
                className="anno-popup-label"
                title="Write a comment"
                aria-label="Write a comment"
                onClick={() => setComposer((current) => (current ? { ...current, writing: true } : current))}
                {...hintProps('Write a comment')}
              >
                💬
              </button>
              {/* Tinted at rest rather than only on hover: sat among eight
                  reaction emoji, an untinted glyph reads as a ninth reaction
                  and the way back out of an annotation stays unfindable. */}
              <button
                type="button"
                className="anno-popup-label anno-popup-label--delete"
                title="Remove this annotation"
                aria-label="Remove this annotation"
                onClick={() => removeAnnotation(composed.id)}
                {...hintProps('Remove this annotation')}
              >
                🗑
              </button>
            </div>
            {/* Hovering names what a chip does; at rest the line names the mark
                this annotation already carries, so reopening one says what it
                says without a click. */}
            <div className="anno-popup-hint" data-testid="annotation-popup-hint">
              {hint ?? labelByEmoji(composed.emoji)?.text ?? 'Pick a label, or write a comment'}
            </div>
            {composer.writing ? (
              <div className="anno-popup-compose">
                <blockquote className="anno-popup-quote">{composed.quote}</blockquote>
                <textarea
                  ref={commentRef}
                  className="anno-popup-text"
                  value={draft}
                  // The placeholder goes on the first keystroke.
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
                  {/* Named, not a glyph. Removing what you wrote is the second
                      thing anyone wants from an open comment, and it should not
                      cost a hunt through the icon row above. */}
                  <button
                    type="button"
                    className="anno-popup-remove"
                    onClick={() => removeAnnotation(composed.id)}
                  >
                    Remove
                  </button>
                  <span className="anno-popup-actions-gap" />
                  <button type="button" className="anno-popup-cancel" onClick={() => dismissComposer()}>
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
          </>,
          document.body,
        )}
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
              {/* The row is two buttons side by side rather than a button
                  nested in a clickable div: the remove control then sits in its
                  own grid track instead of being auto-placed after a
                  comment that spans the row — which is what pushed it onto a
                  line of its own whenever an annotation carried text. */}
              {annotations.map((annotation) => (
                <div key={annotation.id} className="anno-card">
                  <button
                    type="button"
                    className="anno-card-open"
                    title="Edit this annotation"
                    onClick={(event) => reopenFromCard(annotation, event)}
                  >
                    <span className="anno-card-chip">
                      {annotation.emoji || '💬'}
                    </span>
                    <span className="anno-card-quote">{annotation.quote}</span>
                    {annotation.comment ? (
                      <span className="anno-card-comment">{annotation.comment}</span>
                    ) : null}
                  </button>
                  <button
                    type="button"
                    className="anno-card-remove"
                    title="Remove annotation"
                    aria-label="Remove annotation"
                    onClick={() => removeAnnotation(annotation.id, false)}
                  >
                    ✕
                  </button>
                </div>
              ))}
            </div>
            {/* What the marks cannot say. A pass over an answer is usually one
                instruction plus the places it lands, and without somewhere to
                put the instruction it gets typed into the terminal separately —
                which then has to explain itself ("…and also see the feedback
                below"). Here it goes out ahead of the marks, in the same
                keystroke. */}
            <textarea
              className="anno-panel-note"
              data-testid="annotation-note"
              value={note}
              aria-label="Note sent with these annotations"
              placeholder="Anything else to say with these?"
              onChange={(event) => {
                writeNote(event.target.value);
                scheduleNoteSave();
              }}
              onBlur={flushNoteSave}
              // ⌘Enter sends the whole set from in here too: the dispatcher stays
              // out of native editable targets, so this is what binds it.
              onKeyDown={(event) => {
                if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
                  event.preventDefault();
                  send();
                }
              }}
            />
            {/* A refusal sits above the footer rather than replacing it: it is
                asking to be retried, and the button that retries has to stay
                where the eye already is. */}
            {outcomeText ? (
              <div className="anno-panel-outcome" data-testid="annotation-send-note" role="status">
                {outcomeText}
              </div>
            ) : null}
            <div className="anno-panel-foot">
              {outcome?.kind === 'sent' && outcome.kept === 0 ? (
                <span className="anno-panel-sent">✓ sent {outcome.count} to the session</span>
              ) : (
                <>
                  <span className="anno-panel-n">
                    {annotations.length} annotation{annotations.length === 1 ? '' : 's'}
                  </span>
                  <button
                    type="button"
                    className="anno-panel-send"
                    onClick={send}
                    disabled={outcome?.kind === 'sending'}
                  >
                    {outcome?.kind === 'sending' ? 'Sending…' : 'Send all'}
                    {/* The key is only live while this pane holds focus, so the
                        hint is only shown then — a printed shortcut that does
                        nothing where it is printed is worse than none. */}
                    {paneActive && outcome?.kind !== 'sending' ? (
                      <span className="anno-panel-send-key">{formatShortcut('terminal.sendAnnotations')}</span>
                    ) : null}
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
