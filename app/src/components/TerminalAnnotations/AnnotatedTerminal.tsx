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
  // What the user wants to say about the turn as a whole, beside the marks on
  // its parts. Empty when there is none.
  note: string;
  generation: number;
}

// What a send did. `delivered` is the only one that spent the marks; every
// other outcome leaves them on the screen to send again.
export type SessionAnnotationsSubmitStatus =
  | 'delivered'
  | 'skipped_pending_approval'
  | (string & {});

// The daemon calls this surface needs. Bundled rather than threaded one by one:
// annotations are useless without all five, and a partial set would silently
// degrade to the in-memory behaviour this exists to replace.
export interface SessionAnnotationApi {
  fetchMessages: (sessionId: string) => Promise<SessionMessagesResult>;
  fetchAnnotations: (sessionId: string) => Promise<SessionAnnotationsResult>;
  saveAnnotations: (
    sessionId: string,
    annotations: readonly TerminalAnnotation[],
    note: string,
    generation: number,
  ) => Promise<{ stale: boolean }>;
  clearAnnotations: (sessionId: string, generation: number) => Promise<{ generation: number }>;
  // Type the composed feedback into the session AND submit it. Daemon-side, so
  // it can refuse while an approval prompt is up — where the submitting Enter
  // would answer the prompt — and so the Enter lands as a keypress rather than
  // as part of the pasted text.
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
  // Drives when the message window is (re)fetched. Absent disables annotation.
  sessionState?: UISessionState;
  // Absent disables annotation, so the surface is never offered when there is
  // nowhere for it to go.
  annotationApi?: SessionAnnotationApi;
  // Whether this pane is the focused leaf of the visible session. Gates the
  // send shortcut's *registration*: the dispatcher consumes ⌘Enter whenever a
  // handler exists for it, so registering from every mounted pane would eat the
  // keystroke in whichever pane the user is actually typing in.
  paneActive?: boolean;
}

interface Notice {
  text: string;
  clientX: number;
  clientY: number;
  // Distinguishes one miss from the next at the same spot, so a repeated
  // gesture restarts the dismiss timer instead of letting the first one expire
  // under the second.
  seq: number;
}

// The footer's whole vocabulary. `sending` exists because the delivery is a
// round trip with a deliberate pause in it (the daemon's gap between the paste
// and its Enter), and a button that looks idle for a fifth of a second is a
// button people press twice.
type SendOutcome =
  | { kind: 'sending' }
  // `kept` is what was annotated while the send was in flight and therefore
  // was not part of it. Reported rather than assumed to be zero: a panel that
  // does not empty after a successful send otherwise reads as a failed one.
  | { kind: 'sent'; count: number; kept: number }
  | { kind: 'skipped' }
  | { kind: 'error'; message: string };

interface Composer {
  annotationId: string;
  clientX: number;
  clientY: number;
  // The comment box only opens on 💬, on reopening an annotation that has one,
  // or on a click in the panel; the label row alone is a one-click gesture and
  // must not be buried under a textarea.
  writing: boolean;
}

export const AnnotatedTerminal = forwardRef<GhosttyTerminalHandle, AnnotatedTerminalProps>(
  function AnnotatedTerminal(
    { sessionId, sessionState, annotationApi, paneActive = false, ...terminalProps },
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
    // What the last send did, shown in the panel's footer. Null between sends.
    const [outcome, setOutcome] = useState<SendOutcome | null>(null);
    // Guards the round trip. The send is reachable from both the button and
    // ⌘Enter, and a second one landing mid-flight would deliver the same
    // feedback twice — the marks are not cleared until the first answers.
    const sendingRef = useRef(false);
    // What an annotate gesture that resolved to nothing has to say for itself,
    // and where it was made. Null the rest of the time — this is not a status
    // line, it answers a question the user just asked with the pointer.
    const [notice, setNotice] = useState<Notice | null>(null);
    const noticeRef = useRef<HTMLDivElement>(null);
    const [noticeAt, setNoticeAt] = useState<Placement | null>(null);
    // Why the last window fetch produced nothing, in the daemon's own words.
    // A ref because nothing renders from it directly: it is read at the moment
    // a miss needs explaining, and re-rendering the terminal to record a failed
    // background fetch would be a repaint for no one.
    const windowErrorRef = useRef<string | null>(null);
    const [draft, setDraft] = useState('');

    // What the chip row is about to do, named in words under it. Fifteen
    // emoji-only buttons is a row you have to hover one at a time to read, and
    // the native tooltip arrives a second late — long enough that the fast way
    // to find a label is to click one and undo it. The line is always drawn so
    // that naming a chip cannot change the popup's height and move it out from
    // under the pointer.
    const [hint, setHint] = useState<string | null>(null);
    const hintProps = useCallback((text: string) => ({
      onMouseEnter: () => setHint(text),
      onMouseLeave: () => setHint((current) => (current === text ? null : current)),
      onFocus: () => setHint(text),
      onBlur: () => setHint((current) => (current === text ? null : current)),
    }), []);
    // The note the whole set is sent with. Hydrated with the annotations,
    // written through on a pause in typing, and spent by the send that
    // delivered it.
    //
    // Mirrored into a ref because the writes that carry it read it in the same
    // tick as the state update that has not landed yet — a send that clears the
    // note then persists the survivors would otherwise re-save what it just
    // spent.
    const [note, setNote] = useState('');
    const noteRef = useRef('');
    const writeNote = useCallback((next: string) => {
      noteRef.current = next;
      setNote(next);
    }, []);
    // Whether a keystroke is waiting to be written through, and the pending
    // timer, so the pane going away mid-sentence flushes rather than drops it.
    const noteSaveTimerRef = useRef<number | null>(null);
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
    const enabled = Boolean(annotationApi);

    // The surface borrows the keyboard from the terminal and has to give it
    // back, so it keeps its own handle on the terminal alongside whatever the
    // owner asked for.
    const terminalRef = useRef<GhosttyTerminalHandle | null>(null);
    const attachTerminal = useCallback((handle: GhosttyTerminalHandle | null) => {
      terminalRef.current = handle;
      if (typeof ref === 'function') ref(handle);
      else if (ref) (ref as React.MutableRefObject<GhosttyTerminalHandle | null>).current = handle;
    }, [ref]);

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
      void annotationApi.saveAnnotations(sessionId, annotations, noteRef.current, generation)
        .then((result) => {
          if (!result.stale) return;
          // Someone else's write won. Theirs is the truth; take it rather than
          // keep insisting on a list the store already rejected.
          return annotationApi.fetchAnnotations(sessionId).then((stored) => {
            store.hydrate(stored.annotations);
            writeNote(stored.note);
            generationRef.current = stored.generation;
            bump();
          });
        })
        .catch(() => {
          // The daemon is unreachable. What is on screen is still the user's
          // work; the next mutation retries the whole list.
        });
    }, [annotationApi, bump, sessionId, store, writeNote]);

    // The latest persist, for the flush on the way out. The cleanup that runs
    // it is registered once, so it cannot close over the render's copy. Written
    // after the commit, not during render: a render React discards must not
    // leave its callback behind as the one the unmount flush will call.
    const persistRef = useRef(persist);
    useLayoutEffect(() => {
      persistRef.current = persist;
    }, [persist]);

    // Write the note through on a pause in typing rather than per keystroke:
    // one save per pause is all a burst of typing deserves, and everything else
    // on this surface persists on the mutation itself because a click has no
    // burst to smooth out. 400ms clears an ordinary inter-keystroke gap
    // (~100-250ms) while keeping what a crash could cost to a fragment.
    const NOTE_SAVE_PAUSE_MS = 400;
    const scheduleNoteSave = useCallback(() => {
      if (noteSaveTimerRef.current !== null) window.clearTimeout(noteSaveTimerRef.current);
      noteSaveTimerRef.current = window.setTimeout(() => {
        noteSaveTimerRef.current = null;
        persistRef.current();
      }, NOTE_SAVE_PAUSE_MS);
    }, []);

    // A pane closed or a session switched mid-sentence must not cost the
    // sentence: the pending write is flushed on the way out, which is the same
    // guarantee every other mutation here already has.
    const flushNoteSave = useCallback(() => {
      if (noteSaveTimerRef.current === null) return;
      window.clearTimeout(noteSaveTimerRef.current);
      noteSaveTimerRef.current = null;
      persistRef.current();
    }, []);
    useEffect(() => flushNoteSave, [flushNoteSave]);

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
          writeNote(stored.note);
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
    //
    // A failure is remembered rather than swallowed. It is the difference
    // between "this session has no transcript" and "the agent has not spoken
    // yet", which the user can only be told apart if the reason survives the
    // fetch that discovered it.
    useEffect(() => {
      if (!enabled || !sessionId) return;
      if (!sessionState || !SETTLED_STATES.includes(sessionState)) return;
      let cancelled = false;
      void annotationApi!.fetchMessages(sessionId)
        .then((result) => {
          if (cancelled) return;
          windowErrorRef.current = null;
          if (store.setMessages(result.messages)) bump();
        })
        .catch((error: unknown) => {
          if (cancelled) return;
          const detail = error instanceof Error ? error.message : String(error);
          windowErrorRef.current = detail;
          // Logged as well as shown: the notice is a sentence a person reads,
          // and the daemon's own wording is what makes the cause searchable.
          console.warn(`[annotations] ${sessionId}: message window unavailable: ${detail}`);
        });
      return () => {
        cancelled = true;
      };
    }, [annotationApi, bump, enabled, sessionId, sessionState, store]);

    // Closing the popup hands the keyboard back to the terminal: the user came
    // from the grid, and the next thing they type is meant for the agent. The
    // one exception is a press that deliberately lands somewhere else — that
    // press owns where focus goes, and yanking it back would fight the click.
    const closeComposer = useCallback((restoreFocus = true) => {
      setComposer(null);
      setPopupAt(null);
      setDraft('');
      if (restoreFocus) terminalRef.current?.focus();
    }, []);

    // A highlight with neither a label nor a comment says nothing, so dismissing
    // the popup without choosing either removes it rather than leaving a blank
    // wash on the message.
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

    // An annotate gesture that resolved to nothing. Four causes look identical
    // on screen, and each has a different thing the user can do about it, so
    // the notice names the one that actually applies rather than a generic
    // "cannot annotate here".
    // The terminal holds its callbacks in refs, so this changing identity with
    // the session's state costs a prop write rather than a re-subscription —
    // which is why `sessionState` is a dependency here instead of a ref read.
    const missSeqRef = useRef(0);
    const handleMiss = useCallback(
      (reason: 'no-messages' | 'outside-messages', at: { clientX: number; clientY: number }) => {
        const text = reason === 'outside-messages'
          ? 'Only what the agent wrote can be annotated. This text is the TUI’s own, your own, or from a turn that has scrolled out of the window.'
          : windowErrorRef.current
            ? 'No transcript could be read for this session, so there is nothing to annotate. The daemon log names the lookup that failed.'
            : !sessionState || !SETTLED_STATES.includes(sessionState)
              ? 'Annotations open once the agent stops talking. Nothing is anchored while a turn is still running.'
              : 'The agent has not written a message to annotate yet.';
        setNoticeAt(null);
        setNotice({ text, ...at, seq: missSeqRef.current++ });
      },
      [sessionState],
    );

    // Reopening an annotation already made. Its comment becomes the draft, and
    // a comment-carrying annotation opens straight into the editor: the user
    // clicked it to change what it says, not to hunt for the box.
    //
    // `writing` forces the editor open regardless. The panel passes it: a row in
    // a list of what you wrote is clicked to edit it, and offering a bare row of
    // reaction emoji there answers a question nobody asked.
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

    // A press anywhere else closes the popup. Capture phase, so the terminal's
    // own mousedown still runs afterwards and a click outside can start the next
    // selection in the same gesture rather than only dismissing.
    useEffect(() => {
      if (!composer) return;
      const onDown = (event: MouseEvent) => {
        if (popupRef.current?.contains(event.target as Node)) return;
        // The press decides where focus lands — including a press in the panel
        // that is about to open this popup again on another annotation.
        dismissComposer(false);
      };
      window.addEventListener('mousedown', onDown, true);
      return () => window.removeEventListener('mousedown', onDown, true);
    }, [composer, dismissComposer]);

    // What placement has to respect beyond the window: the pane the popup
    // belongs to, and the panel it must not cover. Both are read at placement
    // time rather than remembered — the pane moves on a split or a window
    // resize, and the panel moves on a drag and grows with the list.
    const placementOptions = useCallback((): PlaceOptions => ({
      bounds: terminalRef.current?.getBounds() ?? null,
      avoid: panelRef.current?.getBoundingClientRect() ?? null,
    }), []);

    // Fit a pointer-anchored floater to the pane once it has a size. Idempotent
    // and cheap, so every trigger that can change the answer can just call it.
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

    // The composer as the placement triggers see it. A ref because a resize
    // observer and a window listener fire outside React's render, and the
    // anchor they need is whatever is open right now.
    const composerRef = useRef<Composer | null>(null);

    // Only writes when the answer moved: placement runs on every mutation and
    // on every observed resize, and re-rendering a popup to put it back where
    // it already is repaints the pane underneath it for nothing.
    const applyPopupAt = useCallback((next: Placement) => {
      setPopupAt((current) => (
        current && current.left === next.left && current.top === next.top ? current : next
      ));
    }, []);

    const repositionPopup = useCallback(() => {
      fitToPane(popupRef.current, composerRef.current, applyPopupAt);
    }, [applyPopupAt, fitToPane]);

    // In a layout effect so the corrected position is in place before the
    // browser paints; measuring in a plain effect would show one frame at the
    // unfitted spot. `version` and the panel's own geometry are dependencies
    // because both move the panel the popup is stepping around.
    useLayoutEffect(() => {
      composerRef.current = composer;
      repositionPopup();
    }, [composer, panelAt, repositionPopup, version]);

    // The popup changes size under its own feet — the comment box opens, the
    // textarea is dragged taller, an emoji font finishes loading. Each of those
    // can push a fitted popup back out of the pane, and none of them is a
    // render this component would otherwise see.
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

    // The box is the only reason the editor opened, so it takes the keyboard as
    // it appears. The caret goes to the end: reopening a comment is for adding
    // to what is there, and a focus() alone would leave it at the top.
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

    // `restoreFocus` false for the panel's own remove: the user is working down
    // a list and the next click is another row, not the terminal.
    const removeAnnotation = (id: string, restoreFocus = true) => {
      store.remove(id);
      if (composer?.annotationId === id) closeComposer(restoreFocus);
      persist();
      bump();
    };

    const reopen = (annotation: TerminalAnnotation, at: { clientX: number; clientY: number }) => {
      openAnnotation(annotation.id, at, { writing: true });
    };

    // Where a popup opened from the panel points. The card's own top edge, not
    // the pointer: a row is clicked anywhere along its width, and an editor that
    // lands in a different place each time reads as a different thing.
    const reopenFromCard = (annotation: TerminalAnnotation, event: React.MouseEvent) => {
      const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
      reopen(annotation, { clientX: rect.left + rect.width / 2, clientY: rect.top });
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

    // Delivers the set into the session and submits it. The marks are spent
    // only on a `delivered` answer: a send that was refused — the session is on
    // an approval prompt, the socket is down — leaves every annotation where it
    // is, because clearing work that was never delivered is the one failure the
    // user cannot undo.
    const send = () => {
      if (annotations.length === 0 || !annotationApi || sendingRef.current) return;
      // Land any note still waiting on its typing pause before composing from
      // it. A send that is then refused leaves nothing unwritten behind it.
      flushNoteSave();
      // A comment being typed when the send fires is part of what the user
      // means to say, so commit it rather than dropping it on the floor.
      if (composed && composer?.writing) {
        const comment = draft.trim();
        store.update(composed.id, { comment });
        if (!comment && !composed.emoji) store.remove(composed.id);
      }
      // Re-read after that commit rather than reusing the render's list, which
      // predates it. Committing an emptied comment can leave nothing to send;
      // that is still a mutation the daemon has to hear about.
      //
      // Copied, not referenced: `list()` hands back the store's own array (it
      // is read once per repaint, so it does not allocate), and this one is
      // held across the delivery round trip. A mark made mid-flight would
      // otherwise appear in a payload that was composed before it existed, and
      // be spent by a send it was never part of.
      const sending = store.list().map((entry) => ({ ...entry }));
      closeComposer();
      bump();
      if (sending.length === 0) {
        persist();
        return;
      }
      // Snapshotted alongside the marks and for the same reason: the box stays
      // typable across the round trip, and a sentence added while it was in
      // flight was not part of what went.
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
          // Spend what was actually delivered, and only that. The round trip
          // has a deliberate pause in it and the surface stays live throughout,
          // so by the time it answers the store can hold marks the payload
          // never contained: one made since, and one whose label or comment was
          // changed since. An entry is spent only if it still reads exactly as
          // it did when it was composed — otherwise the agent got the old text
          // and the newer version has yet to be sent.
          const current = new Map(store.list().map((entry) => [entry.id, entry]));
          sending.forEach((entry) => {
            const now = current.get(entry.id);
            if (!now) return;
            if (now.emoji !== entry.emoji || now.comment !== entry.comment) return;
            store.remove(entry.id);
          });
          // The note is spent under the same rule as a mark: only if it still
          // reads as it did when the payload was composed. Typed over while the
          // send was in flight, it belongs to the next one.
          if (sendingNote && noteRef.current.trim() === sendingNote) writeNote('');
          const kept = store.list().length;
          setOutcome({ kind: 'sent', count: sending.length, kept });
          bump();
          // Either way the generation is raised, which is what refuses a save
          // that was already in flight and keeps sent marks from reappearing.
          // A tombstone when nothing survived; an ordinary write-through of the
          // survivors when something did, because a tombstone would take them
          // with it.
          if (kept > 0) {
            persist();
            return;
          }
          generationRef.current += 1;
          return annotationApi.clearAnnotations(sessionId, generationRef.current)
            .then((cleared) => {
              generationRef.current = Math.max(generationRef.current, cleared.generation);
            })
            .catch(() => {
              // The feedback is in the session either way; the next mutation's
              // save re-establishes the row.
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

    // ⌘Enter sends the set without reaching for the panel — the gesture that
    // made the marks was keyboard-adjacent and so is the one that spends them.
    //
    // Registration-gated, not handler-gated: the dispatcher consumes the
    // keystroke whenever a handler is registered, so an always-on no-op would
    // swallow the terminal's ⌘Enter in every pane that has none waiting. The
    // def's `editableTarget: 'native'` additionally keeps it out of the comment
    // box, where ⌘Enter already means "commit this comment".
    useShortcut(
      'terminal.sendAnnotations',
      send,
      enabled && paneActive && annotations.length > 0,
    );

    // The confirmation lives in the panel's footer rather than in a toast: the
    // panel is where the user was looking. It expires on its own — it reports
    // something that already finished. A refusal does not: it is the reason the
    // marks are still on the screen, and it stays until the retry it is asking
    // for succeeds.
    useEffect(() => {
      if (outcome?.kind !== 'sent') return;
      const timer = window.setTimeout(() => setOutcome(null), 2200);
      return () => window.clearTimeout(timer);
    }, [outcome]);

    // Same measure-then-fit as the popup, for the same reason: the pointer is
    // routinely near an edge, and a sentence is wider than the popup is.
    useLayoutEffect(() => {
      fitToPane(noticeRef.current, notice, setNoticeAt);
    }, [fitToPane, notice]);

    // Long enough to read a sentence, then gone. It explains a gesture that has
    // already finished, so it must not become something to dismiss.
    useEffect(() => {
      if (!notice) return;
      const timer = window.setTimeout(() => setNotice(null), 5000);
      return () => window.clearTimeout(timer);
    }, [notice]);

    // A successful annotation answers the question the notice was asking.
    useEffect(() => {
      if (composer) setNotice(null);
    }, [composer]);

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

    const panelOpen = annotations.length > 0 || outcome?.kind === 'sent';

    // The sentence above the footer. A refusal explains why the marks are still
    // there; a send that left some behind explains why the panel did not empty.
    // Both are the answer to "I pressed Send and the list is still here".
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
              // ⌘Enter sends the whole set from in here too. The box is where
              // the last sentence is typed, and reaching for the button after
              // it would be the reach this exists to remove. The shortcut
              // dispatcher stays out of native editable targets, so this is
              // what makes the key work where it is most wanted.
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
