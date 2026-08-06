// The annotation surface end to end, from the terminal reporting an anchor to
// the payload typed back into the session — and, in between, everything the
// daemon holds on the user's behalf. The terminal itself is mocked: what it
// does with the store is covered by terminalAnnotations.gate.test.ts, and what
// matters here is the flow the user drives and what survives it.
//
// The daemon is a real little server rather than a bag of spies: the ordering
// rule it enforces (a save must beat the stored generation and the tombstone)
// is what makes the client's generation bookkeeping load-bearing, and a spy
// that accepts everything would let that bookkeeping rot unnoticed.

import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { AnnotatedTerminal, type SessionAnnotationApi } from './AnnotatedTerminal';
import type {
  AnnotatableMessage,
  MessageAnchor,
  TerminalAnnotation,
  TerminalAnnotationStore,
} from '../../utils/terminalAnnotations';
import type { UISessionState } from '../../types/sessionState';

interface CapturedProps {
  annotations?: TerminalAnnotationStore;
  onAnnotationAnchor?: (anchor: MessageAnchor, at: { clientX: number; clientY: number }) => void;
  onAnnotationMiss?: (
    reason: 'no-messages' | 'outside-messages',
    at: { clientX: number; clientY: number },
  ) => void;
  onAnnotationActivate?: (annotationId: string, at: { clientX: number; clientY: number }) => void;
}

let terminal: CapturedProps = {};
// The surface calls focus() on the terminal handle to hand the keyboard back.
// A real handle rather than a spy on the DOM node: that is the seam the
// component uses, and the count is what proves focus was returned exactly when
// it should be.
let terminalFocusCalls = 0;

vi.mock('../GhosttyTerminal', () => ({
  GhosttyTerminal: React.forwardRef(function MockTerminal(props: CapturedProps, ref: React.Ref<unknown>) {
    terminal = props;
    React.useImperativeHandle(ref, () => ({
      focus: () => {
        terminalFocusCalls += 1;
        return true;
      },
      // jsdom lays nothing out, so there is no pane rect to report. Null is the
      // honest answer and the one placement already handles: it falls back to
      // the window. Placement inside a pane is covered in placement.test.ts,
      // where the geometry can be stated instead of measured.
      getBounds: () => null,
    }), []);
    return <div data-testid="terminal" />;
  }),
}));

const TURN_1 = 'The parser already handles CRLF, so the retry wrapper is safe to land as is.';
const TURN_2 = 'Added the retry test and pushed; CI is green on the second run.';

/**
 * The daemon's half of the contract: a stored list under a generation, plus the
 * tombstone a send raises. Mirrors internal/store/annotation_drafts.go.
 */
class FakeAnnotationDaemon implements SessionAnnotationApi {
  messages: AnnotatableMessage[] = [{ key: 'turn-1', markdown: TURN_1 }];
  truncated = false;
  annotations: TerminalAnnotation[] = [];
  note = '';
  generation = 0;
  tombstone = 0;
  calls = { fetchMessages: 0, fetchAnnotations: 0, saveAnnotations: 0, clearAnnotations: 0 };
  // Set to fail the next save the way a competing writer would, so the client's
  // stale path can be driven without a second component in the test.
  stealNextSave: TerminalAnnotation[] | null = null;
  // Every payload the client asked to have delivered, in order — including the
  // ones the daemon then refused, which is how "it tried but nothing was sent"
  // stays distinguishable from "it never tried".
  submitted: string[] = [];
  // How the next submit answers. The refusals are real protocol outcomes the
  // client has to keep the user's marks through.
  nextSubmitStatus: 'delivered' | 'skipped_pending_approval' | 'error' = 'delivered';
  submitRejection: Error | null = null;
  // Held open to drive the in-flight window: resolve it to let the send answer.
  releaseSubmit: (() => void) | null = null;

  submitAnnotations = async (_sessionId: string, text: string) => {
    this.submitted.push(text);
    if (this.releaseSubmit !== null) {
      await new Promise<void>((resolve) => {
        this.releaseSubmit = () => {
          this.releaseSubmit = null;
          resolve();
        };
      });
    }
    if (this.submitRejection) throw this.submitRejection;
    return { status: this.nextSubmitStatus };
  };

  fetchMessages = async (_sessionId: string) => {
    this.calls.fetchMessages += 1;
    return { messages: this.messages.map((message) => ({ ...message })), truncated: this.truncated };
  };

  fetchAnnotations = async (_sessionId: string) => {
    this.calls.fetchAnnotations += 1;
    return {
      annotations: this.annotations.map((annotation) => ({ ...annotation })),
      note: this.note,
      generation: this.generation,
    };
  };

  saveAnnotations = async (
    _sessionId: string,
    annotations: readonly TerminalAnnotation[],
    note: string,
    generation: number,
  ) => {
    this.calls.saveAnnotations += 1;
    if (this.stealNextSave) {
      // Another writer got there first: its list is stored at a generation this
      // save cannot beat, exactly as the store's ordering rule would leave it.
      this.annotations = this.stealNextSave.map((annotation) => ({ ...annotation }));
      this.generation = Math.max(this.generation, generation) + 1;
      this.stealNextSave = null;
      return { stale: true };
    }
    if (generation <= this.generation || generation <= this.tombstone) return { stale: true };
    this.annotations = annotations.map((annotation) => ({ ...annotation }));
    this.note = note;
    this.generation = generation;
    return { stale: false };
  };

  clearAnnotations = async (_sessionId: string, generation: number) => {
    this.calls.clearAnnotations += 1;
    if (generation > this.tombstone) this.tombstone = generation;
    if (generation > this.generation) {
      this.annotations = [];
      // The note is composed with the marks and goes with them. See
      // annotationDraftTable.clear.
      this.note = '';
      this.generation = generation;
    }
    return { generation: this.generation };
  };
}

function props(overrides: {
  state?: UISessionState;
  api?: SessionAnnotationApi;
  paneActive?: boolean;
}) {
  return {
    sessionId: 'session-1',
    sessionState: overrides.state ?? ('idle' as UISessionState),
    annotationApi: overrides.api,
    paneActive: overrides.paneActive ?? false,
    fontSize: 13,
    debugName: 'test',
    onInput: () => {},
    onReady: () => {},
    onResize: () => {},
  };
}

function renderTerminal(overrides: {
  state?: UISessionState;
  api?: FakeAnnotationDaemon;
  paneActive?: boolean;
} = {}) {
  const daemon = overrides.api ?? new FakeAnnotationDaemon();
  const view = render(<AnnotatedTerminal {...props({ ...overrides, api: daemon })} />);
  const rerender = (next: { state?: UISessionState; paneActive?: boolean } = {}) =>
    view.rerender(
      <AnnotatedTerminal {...props({
        state: next.state ?? overrides.state,
        paneActive: next.paneActive ?? overrides.paneActive,
        api: daemon,
      })} />,
    );
  return { ...view, rerender, daemon };
}

/** Waits for the annotatable window to have reached the store. */
async function windowReady(...keys: string[]) {
  await waitFor(() => expect(terminal.annotations?.messageKeys()).toEqual(keys));
}

/** Drives the gesture the terminal reports after an alt-drag over a message. */
function anchor(messageKey: string, start: number, end: number) {
  const quote = terminal.annotations?.markdownFor(messageKey)?.slice(start, end) ?? '';
  act(() => {
    terminal.onAnnotationAnchor?.({ messageKey, start, end, quote }, { clientX: 120, clientY: 200 });
  });
}

/** Drives an alt-drag the terminal could not resolve to an anchor. */
function miss(reason: 'no-messages' | 'outside-messages') {
  act(() => {
    terminal.onAnnotationMiss?.(reason, { clientX: 120, clientY: 200 });
  });
}

function notice(): string | null {
  return screen.queryByTestId('annotation-notice')?.textContent ?? null;
}

/** Drives the alt-click the terminal reports over an existing wash. */
function activate(annotationId: string) {
  act(() => {
    terminal.onAnnotationActivate?.(annotationId, { clientX: 140, clientY: 220 });
  });
}

function stored() {
  return terminal.annotations?.list() ?? [];
}

/** The panel row for the nth annotation, and its two controls. */
function card(index = 0) {
  const cards = document.querySelectorAll('.anno-card');
  const node = cards[index] as HTMLElement;
  return {
    node,
    open: node.querySelector('.anno-card-open') as HTMLElement,
    remove: node.querySelector('.anno-card-remove') as HTMLElement,
  };
}

beforeEach(() => {
  terminal = {};
  terminalFocusCalls = 0;
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('AnnotatedTerminal', () => {
  it('hands the terminal the annotatable window once the turn has settled', async () => {
    const { daemon } = renderTerminal();

    await windowReady('turn-1');
    expect(terminal.annotations?.markdownFor('turn-1')).toBe(TURN_1);
    expect(daemon.calls.fetchMessages).toBe(1);
  });

  it('does not read a message the agent is still writing', async () => {
    // Mid-turn the newest message is incomplete, and every offset taken against
    // it would address text that is about to be replaced.
    const { daemon } = renderTerminal({ state: 'working' });

    await act(async () => {});
    expect(daemon.calls.fetchMessages).toBe(0);
  });

  it('keeps annotations when a re-fetch returns the same window', async () => {
    const { rerender, daemon } = renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 4, 10);
    fireEvent.click(screen.getByLabelText('Show the receipt'));
    expect(stored()).toHaveLength(1);

    rerender({ state: 'waiting_input' });

    await waitFor(() => expect(daemon.calls.fetchMessages).toBe(2));
    expect(stored()).toHaveLength(1);
  });

  it('keeps a past turn annotated when a new turn arrives', async () => {
    // The whole point of anchoring to the transcript. A new turn extends the
    // window; the annotation on the previous one addresses a key that is still
    // in it, so it keeps its quote, its place in the panel, and its wash.
    const { rerender, daemon } = renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 4, 10);
    fireEvent.click(screen.getByLabelText('Show the receipt'));
    const before = stored()[0];

    daemon.messages = [
      { key: 'turn-1', markdown: TURN_1 },
      { key: 'turn-2', markdown: TURN_2 },
    ];
    rerender({ state: 'waiting_input' });

    await windowReady('turn-1', 'turn-2');
    expect(stored()).toHaveLength(1);
    expect(stored()[0]).toEqual(before);
    expect(screen.getByTestId('annotation-panel')).toBeTruthy();
  });

  it('keeps an annotation whose turn has scrolled out of the window', async () => {
    // Losing the user's work because a turn aged out is the same bug as losing
    // it on a new turn. It stops painting; it does not stop existing.
    const { rerender, daemon } = renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 4, 10);
    fireEvent.click(screen.getByLabelText('Show the receipt'));

    daemon.messages = [{ key: 'turn-2', markdown: TURN_2 }];
    rerender({ state: 'waiting_input' });

    await windowReady('turn-2');
    expect(stored()).toHaveLength(1);
    expect(stored()[0].quote).toBe(TURN_1.slice(4, 10));
  });

  it('opens the label popup on an anchor and files the annotation on a pick', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    expect(screen.getByTestId('annotation-popup')).toBeTruthy();

    fireEvent.click(screen.getByLabelText('Verify this'));

    // One click is the whole gesture: the popup closes and the panel takes over.
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
    expect(screen.getByTestId('annotation-panel')).toBeTruthy();
    expect(stored()[0]?.emoji).toBe('🔍');
    expect(stored()[0]?.quote).toBe(TURN_1.slice(0, 26));
  });

  it('discards a highlight dismissed without a label or a comment', async () => {
    // A wash with nothing attached says nothing to the agent and would sit on
    // the message as noise.
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    expect(stored()).toHaveLength(1);

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(stored()).toHaveLength(0);
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('carries a written comment onto the annotation', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    fireEvent.change(screen.getByPlaceholderText('What should change here?'), {
      target: { value: 'CRLF is handled downstream, not here.' },
    });
    fireEvent.click(screen.getByText('Comment'));

    expect(stored()[0]?.comment).toBe('CRLF is handled downstream, not here.');
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('sends the whole set to the session and clears it', async () => {
    const { daemon } = renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    anchor('turn-1', 31, 55);
    fireEvent.click(screen.getByLabelText('Show the receipt'));

    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(screen.getByText(/sent 2 to the session/)).toBeTruthy());
    expect(daemon.submitted).toHaveLength(1);
    expect(daemon.submitted[0]).toContain(TURN_1.slice(0, 26));
    expect(daemon.submitted[0]).toContain(TURN_1.slice(31, 55));
    // Sending is the end of the set: leaving it behind would re-send the same
    // feedback on the next click.
    expect(stored()).toHaveLength(0);
  });

  it('reopens a reaction from the message, and lets it be changed', async () => {
    // The wash lives on the message, so the message is the only place the user
    // can be expected to go back to it.
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));

    activate(stored()[0].id);
    expect(screen.getByTestId('annotation-popup')).toBeTruthy();

    fireEvent.click(screen.getByLabelText('Show the receipt'));

    expect(stored()).toHaveLength(1);
    expect(stored()[0].emoji).toBe('🧾');
  });

  it('reopens a comment straight into its editor, prefilled', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    fireEvent.change(screen.getByPlaceholderText('What should change here?'), {
      target: { value: 'first take' },
    });
    fireEvent.click(screen.getByText('Comment'));

    activate(stored()[0].id);

    // Reopening to edit means the text is already there to edit.
    const box = screen.getByPlaceholderText('What should change here?') as HTMLTextAreaElement;
    expect(box.value).toBe('first take');

    fireEvent.change(box, { target: { value: 'second take' } });
    fireEvent.click(screen.getByText('Comment'));
    expect(stored()[0].comment).toBe('second take');
  });

  it('removes a reopened annotation from its own popup', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    activate(stored()[0].id);

    fireEvent.click(screen.getByLabelText('Remove this annotation'));

    expect(stored()).toHaveLength(0);
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('drops a reaction toggled back off rather than leaving a blank wash', async () => {
    // Same rule as dismissing an untouched highlight: an annotation with
    // nothing on it paints over the message and tells the agent nothing.
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    activate(stored()[0].id);

    fireEvent.click(screen.getByLabelText('Verify this'));

    expect(stored()).toHaveLength(0);
  });

  it('discards a bare highlight when the press lands outside the popup', async () => {
    // Clicking away is how people dismiss a popup; without this the highlight
    // is stranded on the message with no label and no way back to it.
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    expect(stored()).toHaveLength(1);

    fireEvent.mouseDown(screen.getByTestId('terminal'));

    expect(screen.queryByTestId('annotation-popup')).toBeNull();
    expect(stored()).toHaveLength(0);
  });

  it('keeps the popup open for a press inside it', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.mouseDown(screen.getByLabelText('Verify this'));

    expect(screen.getByTestId('annotation-popup')).toBeTruthy();
  });

  it('moves the panel with a drag on its header', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    const panel = screen.getByTestId('annotation-panel');
    // Pinned to its corner until moved, so the default costs no state.
    expect(panel.style.left).toBe('');

    // The panel follows the pointer's delta from where it was grabbed, not the
    // pointer itself: grabbing the header near its right edge and dragging must
    // not teleport that edge under the cursor.
    fireEvent.mouseDown(panel.querySelector('.anno-panel-head')!, { clientX: 100, clientY: 100 });
    fireEvent.mouseMove(window, { clientX: 260, clientY: 340 });
    fireEvent.mouseUp(window);

    expect(panel.style.left).toBe('160px');
    expect(panel.style.top).toBe('240px');
    // A dropped panel stays put rather than continuing to track the pointer.
    fireEvent.mouseMove(window, { clientX: 700, clientY: 700 });
    expect(panel.style.left).toBe('160px');
  });

  it('removes an annotation from the panel', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    fireEvent.click(screen.getByLabelText('Remove annotation'));

    expect(stored()).toHaveLength(0);
    expect(screen.queryByTestId('annotation-panel')).toBeNull();
  });

  it('keeps the panel row\'s remove control beside the row, not below it', async () => {
    // A row is two controls side by side. The earlier layout nested the remove
    // button inside the clickable row and let the grid auto-place it after a
    // comment that spanned the row, which dropped it onto a line of its own the
    // moment an annotation carried text.
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    fireEvent.change(screen.getByPlaceholderText('What should change here?'), {
      target: { value: 'long enough to wrap onto its own line in the row' },
    });
    fireEvent.click(screen.getByText('Comment'));

    const { node, open, remove } = card();
    expect(open).toBeTruthy();
    expect(remove.parentElement).toBe(node);
    // The comment lives inside the row's own control, so it can never be a
    // sibling the remove button has to be placed around.
    expect(open.querySelector('.anno-card-comment')).toBeTruthy();
  });

  it('opens the editor when a panel row is clicked, even for a bare reaction', async () => {
    // A row in a list of what you wrote is clicked to change what it says. A
    // reaction-only annotation used to reopen into the icon row alone, which
    // answers a question nobody asked at that moment.
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));

    fireEvent.click(card().open);

    const box = screen.getByPlaceholderText('What should change here?') as HTMLTextAreaElement;
    expect(box.value).toBe('');
    expect(document.activeElement).toBe(box);
  });

  it('puts the caret after a prefilled comment rather than at its start', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    fireEvent.change(screen.getByPlaceholderText('What should change here?'), {
      target: { value: 'first take' },
    });
    fireEvent.click(screen.getByText('Comment'));

    fireEvent.click(card().open);

    const box = screen.getByPlaceholderText('What should change here?') as HTMLTextAreaElement;
    expect(document.activeElement).toBe(box);
    expect(box.selectionStart).toBe('first take'.length);
  });

  it('removes the annotation from the open editor by name', async () => {
    // The way out of a comment cannot be an emoji in a row of eight other
    // emoji; from the editor it is a named button.
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    fireEvent.change(screen.getByPlaceholderText('What should change here?'), {
      target: { value: 'wrong on reflection' },
    });
    fireEvent.click(screen.getByText('Comment'));

    fireEvent.click(card().open);
    fireEvent.click(screen.getByText('Remove'));

    expect(stored()).toHaveLength(0);
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('hands the keyboard back to the terminal when the editor closes', async () => {
    // The surface borrowed focus from the grid; leaving it typing into nothing
    // is what makes an overlay feel like a trap.
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    const box = screen.getByPlaceholderText('What should change here?');
    expect(document.activeElement).toBe(box);
    fireEvent.change(box, { target: { value: 'say this' } });

    terminalFocusCalls = 0;
    fireEvent.click(screen.getByText('Comment'));
    expect(terminalFocusCalls).toBe(1);

    fireEvent.click(card().open);
    terminalFocusCalls = 0;
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(terminalFocusCalls).toBe(1);
  });

  it('leaves focus alone when the press that closed the popup landed elsewhere', async () => {
    // That press decides where focus goes — including a press on another row of
    // the panel, which is about to open this same popup on a different mark.
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    anchor('turn-1', 31, 55);
    fireEvent.click(screen.getByLabelText('Show the receipt'));

    fireEvent.click(card(0).open);
    terminalFocusCalls = 0;
    fireEvent.mouseDown(card(1).open);

    expect(terminalFocusCalls).toBe(0);
  });

  it('sends the set on the send shortcut while the pane holds focus', async () => {
    const { daemon } = renderTerminal({ paneActive: true });
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));

    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });

    await waitFor(() => expect(stored()).toHaveLength(0));
    expect(daemon.submitted).toHaveLength(1);
    expect(daemon.submitted[0]).toContain(TURN_1.slice(0, 26));
  });

  it('commits the comment being typed when the send shortcut fires', async () => {
    // Half-written feedback is still what the user meant to say; dropping it on
    // the way out would be silent data loss.
    const { daemon } = renderTerminal({ paneActive: true });
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    fireEvent.change(screen.getByPlaceholderText('What should change here?'), {
      target: { value: 'still typing this' },
    });

    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });

    await waitFor(() => expect(daemon.submitted).toHaveLength(1));
    expect(daemon.submitted[0]).toContain('still typing this');
  });

  it('leaves the send keystroke to the PTY when the pane is not the focused one', async () => {
    // Registration is the gate: the dispatcher consumes ⌘Enter whenever a
    // handler exists, so a pane that is merely mounted must not register one.
    const { daemon } = renderTerminal({ paneActive: false });
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));

    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });

    expect(daemon.submitted).toEqual([]);
    expect(stored()).toHaveLength(1);
  });

  it('leaves the send keystroke to the PTY when there is nothing to send', async () => {
    const { daemon, rerender } = renderTerminal({ paneActive: true });
    await windowReady('turn-1');
    rerender({ paneActive: true });

    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });

    expect(daemon.submitted).toEqual([]);
  });

  it('offers no annotation surface without a daemon to hold the marks', async () => {
    // Annotations that cannot be persisted or delivered have nowhere to go, so
    // the terminal is never given a store and the alt-drag stays an ordinary
    // selection.
    render(<AnnotatedTerminal {...props({ api: undefined })} />);

    await act(async () => {});
    expect(terminal.annotations).toBeUndefined();
    expect(terminal.onAnnotationAnchor).toBeUndefined();
  });
});

// Fifteen emoji-only chips is a row nobody can read. The line under it is what
// makes the row learnable without hovering each one and waiting for a tooltip.
describe('AnnotatedTerminal label hint', () => {
  function hint(): string {
    return screen.getByTestId('annotation-popup-hint').textContent ?? '';
  }

  it('names a chip while the pointer is on it', async () => {
    renderTerminal();
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);

    fireEvent.mouseEnter(screen.getByLabelText('Show the receipt'));

    expect(hint()).toBe('Show the receipt');
  });

  it('goes back to naming the mark that is already on this annotation', async () => {
    // Reopening a mark should say what it says. Leaving the last hovered chip
    // in the line would name a label the annotation does not carry.
    renderTerminal();
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Show the receipt'));
    // Marking closes the popup, so this is the reopen a user does from the grid.
    activate(stored()[0].id);

    expect(hint()).toBe('Show the receipt');
    fireEvent.mouseEnter(screen.getByLabelText('This is wrong'));
    expect(hint()).toBe('This is wrong');
    fireEvent.mouseLeave(screen.getByLabelText('This is wrong'));

    expect(hint()).toBe('Show the receipt');
  });

  it('says what to do when nothing is marked or hovered', async () => {
    renderTerminal();
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);

    expect(hint()).toBe('Pick a label, or write a comment');
  });
});

// The note is the other half of a pass over an answer: one instruction, plus
// the places it lands. Before it existed the instruction was typed into the
// terminal on its own and had to explain its relationship to the marks.
describe('AnnotatedTerminal note', () => {
  /** The box the note is typed into, and typing into it. */
  function noteBox(): HTMLTextAreaElement {
    return screen.getByTestId('annotation-note') as HTMLTextAreaElement;
  }

  function writeNote(text: string) {
    fireEvent.change(noteBox(), { target: { value: text } });
  }

  it('has nowhere to be written until something is marked', async () => {
    // The panel is what hosts it, and the panel is what a mark opens. A note
    // with no marks is an ordinary message and belongs in the terminal.
    renderTerminal();
    await windowReady('turn-1');

    expect(screen.queryByTestId('annotation-note')).toBeNull();
  });

  it('is written through to the daemon on a pause in typing', async () => {
    const { daemon } = renderTerminal();
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));

    writeNote('Split this into two PRs.');

    await waitFor(() => expect(daemon.note).toBe('Split this into two PRs.'));
  });

  it('survives the pane it was typed in', async () => {
    // The whole point of the daemon holding the draft. A note kept in this
    // component's memory would be gone the next time the session is opened —
    // and it is the part of the draft with the most thought in it.
    const daemon = new FakeAnnotationDaemon();
    const first = renderTerminal({ api: daemon });
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    writeNote('Split this into two PRs.');
    await waitFor(() => expect(daemon.note).toBe('Split this into two PRs.'));
    first.unmount();

    renderTerminal({ api: daemon });

    await waitFor(() => expect(noteBox().value).toBe('Split this into two PRs.'));
  });

  it('goes out ahead of the marks in one keystroke', async () => {
    const { daemon } = renderTerminal({ paneActive: true });
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    writeNote('Split this into two PRs.');

    // From inside the box: the last sentence is typed there, and reaching for
    // the button afterwards is the reach the note exists to remove.
    fireEvent.keyDown(noteBox(), { key: 'Enter', metaKey: true });

    await waitFor(() => expect(daemon.submitted).toHaveLength(1));
    const payload = daemon.submitted[0];
    expect(payload.indexOf('Split this into two PRs.')).toBeLessThan(payload.indexOf('## 1.'));
  });

  it('is spent by the send that delivered it', async () => {
    const { daemon } = renderTerminal({ paneActive: true });
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    writeNote('Split this into two PRs.');
    await waitFor(() => expect(daemon.note).toBe('Split this into two PRs.'));

    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(daemon.submitted).toHaveLength(1));
    await waitFor(() => expect(daemon.note).toBe(''));
  });

  it('is kept by a send the session refused', async () => {
    // The same asymmetry the marks have: clearing work that was never
    // delivered is the one failure the user cannot undo.
    const { daemon } = renderTerminal({ paneActive: true });
    daemon.nextSubmitStatus = 'skipped_pending_approval';
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    writeNote('Split this into two PRs.');

    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(screen.getByTestId('annotation-send-note')).toBeTruthy());
    expect(noteBox().value).toBe('Split this into two PRs.');
    await waitFor(() => expect(daemon.note).toBe('Split this into two PRs.'));
  });
});

// Sending is the moment the user's marks stop being theirs and become a turn.
// Everything here is about the one asymmetry that makes it dangerous: a
// delivered send SHOULD clear them, and every other outcome MUST NOT.
describe('AnnotatedTerminal sending', () => {
  it('keeps the marks and says why when the session is on an approval prompt', async () => {
    // pending_approval is annotatable — an approval prompt is exactly when a
    // user wants to push back — but it is also where the submitting Enter would
    // answer the prompt. The daemon refuses; the marks have to survive it.
    const { daemon } = renderTerminal({ state: 'pending_approval' as UISessionState });
    daemon.nextSubmitStatus = 'skipped_pending_approval';
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(screen.getByTestId('annotation-send-note')).toBeTruthy());
    expect(screen.getByTestId('annotation-send-note').textContent).toMatch(/waiting on an approval/i);
    expect(daemon.submitted).toHaveLength(1);
    expect(stored()).toHaveLength(1);
    // And the way to retry is still there, under the reason it did not go.
    expect(screen.getByText('Send all')).toBeTruthy();
  });

  it('keeps the marks and shows the failure when delivery fails', async () => {
    const { daemon } = renderTerminal();
    daemon.submitRejection = new Error('Session annotation send timed out');
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(screen.getByTestId('annotation-send-note')).toBeTruthy());
    expect(screen.getByTestId('annotation-send-note').textContent).toContain('timed out');
    expect(stored()).toHaveLength(1);
    // The tombstone is what spends the marks daemon-side. A failed send must
    // never raise it, or a reload would come back empty.
    expect(daemon.calls.clearAnnotations).toBe(0);
  });

  it('refuses a second send while the first is still in flight', async () => {
    // The send is reachable from the button and from ⌘Enter, and the marks are
    // not cleared until the first answers — so an unguarded second one delivers
    // the same feedback twice.
    const { daemon } = renderTerminal({ paneActive: true });
    daemon.releaseSubmit = () => {};
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(screen.getByText('Sending…')).toBeTruthy());
    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });
    fireEvent.click(screen.getByText('Sending…'));

    expect(daemon.submitted).toHaveLength(1);

    await act(async () => {
      daemon.releaseSubmit?.();
    });
    await waitFor(() => expect(stored()).toHaveLength(0));
  });

  it('keeps an annotation made while an earlier send was in flight', async () => {
    // The round trip has a deliberate pause in it (the daemon's gap between the
    // paste and its Enter) and the surface stays live throughout, so a mark can
    // be made after the payload was composed. It was never in that payload, so
    // spending it on the delivery would delete work the user can never send.
    const { daemon } = renderTerminal();
    daemon.releaseSubmit = () => {};
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    const sentId = stored()[0].id;

    fireEvent.click(screen.getByText('Send all'));
    await waitFor(() => expect(daemon.submitted).toHaveLength(1));
    // Mid-flight, the user marks something else.
    anchor('turn-1', 31, 55);
    fireEvent.click(screen.getByLabelText('Show the receipt'));
    const keptId = stored().find((entry) => entry.id !== sentId)!.id;

    await act(async () => {
      daemon.releaseSubmit?.();
    });

    await waitFor(() => expect(stored().map((entry) => entry.id)).toEqual([keptId]));
    // Only the sent annotation's own words went out.
    expect(daemon.submitted[0]).toContain(TURN_1.slice(0, 26));
    expect(daemon.submitted[0]).not.toContain(TURN_1.slice(31, 55));
    // And the survivor is written through rather than tombstoned away with the
    // rest, so a reload still has it.
    await waitFor(() => expect(daemon.annotations.map((entry) => entry.id)).toEqual([keptId]));
    expect(daemon.calls.clearAnnotations).toBe(0);
    // The panel says why it did not empty, and still offers the send.
    expect(screen.getByTestId('annotation-send-note').textContent).toMatch(/1 still here/);
    expect(screen.getByText('Send all')).toBeTruthy();
  });

  it('keeps an annotation edited while the send carrying it was in flight', async () => {
    // Same window, the other way in: the mark WAS in the payload, but the user
    // changed it before the send answered. The agent got the old version, so
    // spending it would delete a revision that has never been sent.
    const { daemon } = renderTerminal();
    daemon.releaseSubmit = () => {};
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    const id = stored()[0].id;

    fireEvent.click(screen.getByText('Send all'));
    await waitFor(() => expect(daemon.submitted).toHaveLength(1));
    expect(daemon.submitted[0]).toContain('🔍 Verify this');

    // Mid-flight, the user changes their mind about the label.
    activate(id);
    fireEvent.click(screen.getByLabelText('Show the receipt'));

    await act(async () => {
      daemon.releaseSubmit?.();
    });

    await waitFor(() => expect(screen.getByTestId('annotation-send-note')).toBeTruthy());
    expect(stored()).toHaveLength(1);
    expect(stored()[0].id).toBe(id);
    expect(stored()[0].emoji).toBe('🧾');
    await waitFor(() => expect(daemon.annotations.map((entry) => entry.emoji)).toEqual(['🧾']));
    expect(daemon.calls.clearAnnotations).toBe(0);
  });

  it('spends a mark the send carried unchanged, even beside an edited one', async () => {
    // The keep is per entry, not per send: an edit to one annotation must not
    // strand the others the same payload delivered.
    const { daemon } = renderTerminal();
    daemon.releaseSubmit = () => {};
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    const editedId = stored()[0].id;
    anchor('turn-1', 31, 55);
    fireEvent.click(screen.getByLabelText('Show the receipt'));

    fireEvent.click(screen.getByText('Send all'));
    await waitFor(() => expect(daemon.submitted).toHaveLength(1));

    activate(editedId);
    fireEvent.click(screen.getByLabelText('This is wrong'));

    await act(async () => {
      daemon.releaseSubmit?.();
    });

    await waitFor(() => expect(stored().map((entry) => entry.id)).toEqual([editedId]));
    expect(stored()[0].emoji).toBe('❌');
  });

  it('tombstones the daemon draft only once the send is delivered', async () => {
    const { daemon } = renderTerminal();
    daemon.releaseSubmit = () => {};
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    await waitFor(() => expect(daemon.annotations).toHaveLength(1));

    fireEvent.click(screen.getByText('Send all'));
    await waitFor(() => expect(daemon.submitted).toHaveLength(1));
    // In flight: the daemon still holds them, because a send that never
    // arrives must leave something to come back to.
    expect(daemon.annotations).toHaveLength(1);
    expect(daemon.calls.clearAnnotations).toBe(0);

    await act(async () => {
      daemon.releaseSubmit?.();
    });
    await waitFor(() => expect(daemon.calls.clearAnnotations).toBe(1));
    expect(daemon.annotations).toHaveLength(0);
  });
});

describe('AnnotatedTerminal persistence', () => {
  it('writes every mutation through to the daemon as it happens', async () => {
    // No debounce and no send-time flush: an annotation is safe the moment it
    // is made, because anything that ends the pane can happen in the next tick.
    const { daemon } = renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    await waitFor(() => expect(daemon.annotations).toHaveLength(1));
    expect(daemon.annotations[0].emoji).toBe('🔍');
    expect(daemon.annotations[0].quote).toBe(TURN_1.slice(0, 26));

    fireEvent.click(screen.getByLabelText('Remove annotation'));
    await waitFor(() => expect(daemon.annotations).toHaveLength(0));
  });

  it('shows what an earlier app run left behind, before anything is drawn', async () => {
    const daemon = new FakeAnnotationDaemon();
    daemon.annotations = [{
      id: 'stored-1',
      messageKey: 'turn-1',
      start: 4,
      end: 10,
      quote: TURN_1.slice(4, 10),
      emoji: '❓',
      comment: 'why this?',
    }];
    daemon.generation = 7;

    renderTerminal({ api: daemon });

    await waitFor(() => expect(stored()).toHaveLength(1));
    expect(stored()[0].comment).toBe('why this?');
    expect(screen.getByTestId('annotation-panel')).toBeTruthy();
  });

  it('survives the pane being unmounted and mounted again', async () => {
    // Pane virtualization unmounts this component outright. Persistence plus
    // hydrate-on-mount is the only reason that is survivable.
    const daemon = new FakeAnnotationDaemon();
    const first = renderTerminal({ api: daemon });
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    await waitFor(() => expect(daemon.annotations).toHaveLength(1));

    first.unmount();
    terminal = {};
    renderTerminal({ api: daemon });

    await waitFor(() => expect(stored()).toHaveLength(1));
    expect(stored()[0].emoji).toBe('🔍');
    expect(stored()[0].quote).toBe(TURN_1.slice(0, 26));
  });

  it('takes the daemon\'s list when a save is refused as stale', async () => {
    // Losing a race is not an error to put in front of the user: the stored
    // list is the truth, so adopt it rather than keep insisting on a rejected
    // one and drifting further away with every later save.
    const { daemon } = renderTerminal();
    await windowReady('turn-1');

    daemon.stealNextSave = [{
      id: 'theirs-1',
      messageKey: 'turn-1',
      start: 31,
      end: 55,
      quote: TURN_1.slice(31, 55),
      emoji: '🧾',
      comment: '',
    }];

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));

    await waitFor(() => expect(stored().map((entry) => entry.id)).toEqual(['theirs-1']));

    // And the client's generation moved with it, so the next mutation writes
    // rather than losing again to a floor it never learned about.
    fireEvent.click(screen.getByLabelText('Remove annotation'));
    await waitFor(() => expect(daemon.annotations).toHaveLength(0));
  });

  it('tombstones the set it typed into the session', async () => {
    // A save already in flight when Send is pressed must not resurrect marks
    // the user has already sent, which is what the tombstone refuses.
    const { daemon } = renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    await waitFor(() => expect(daemon.annotations).toHaveLength(1));

    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(daemon.annotations).toHaveLength(0));
    expect(daemon.tombstone).toBeGreaterThan(0);
    const late = await daemon.saveAnnotations('session-1', [{
      id: 'in-flight',
      messageKey: 'turn-1',
      start: 0,
      end: 26,
      quote: TURN_1.slice(0, 26),
      emoji: '🔍',
      comment: '',
    }], '', daemon.tombstone);
    expect(late.stale).toBe(true);
    expect(daemon.annotations).toHaveLength(0);
  });

  // An annotate gesture that resolves to nothing used to do exactly nothing —
  // no popup, no message, no log line — and the four reasons for it are
  // indistinguishable from the feature being broken.
  describe('when a gesture cannot be annotated', () => {
    it('says the text was not the agent’s when there is a window it missed', async () => {
      renderTerminal();
      await windowReady('turn-1');

      miss('outside-messages');

      expect(notice()).toContain('Only what the agent wrote can be annotated');
    });

    it('names the unreadable transcript rather than blaming the selection', async () => {
      // The daemon's own path: resolveTranscriptPathForSession found nothing, so
      // there is no window and never will be for this session.
      const daemon = new FakeAnnotationDaemon();
      daemon.fetchMessages = async () => {
        throw new Error('session_messages_get: no transcript for session session-1');
      };
      const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
      renderTerminal({ api: daemon });
      await waitFor(() => expect(warn).toHaveBeenCalled());

      miss('no-messages');

      expect(notice()).toContain('No transcript could be read');
      // The sentence is for the user; the daemon's wording is what makes the
      // cause searchable afterwards.
      expect(warn.mock.calls[0][0]).toContain('no transcript for session session-1');
      warn.mockRestore();
    });

    it('points at the running turn when the window was never fetched', async () => {
      // Not an error at all: nothing is anchored mid-turn by design, and the
      // user has only to wait.
      renderTerminal({ state: 'working' });

      miss('no-messages');

      expect(notice()).toContain('once the agent stops talking');
    });

    it('says the agent has not spoken when the window came back empty', async () => {
      const daemon = new FakeAnnotationDaemon();
      daemon.messages = [];
      renderTerminal({ api: daemon });
      await waitFor(() => expect(daemon.calls.fetchMessages).toBe(1));

      miss('no-messages');

      expect(notice()).toContain('has not written a message');
    });

    it('clears itself once an annotation actually lands', async () => {
      renderTerminal();
      await windowReady('turn-1');
      miss('outside-messages');
      expect(notice()).not.toBeNull();

      anchor('turn-1', 0, 26);

      expect(notice()).toBeNull();
      expect(screen.getByTestId('annotation-popup')).toBeTruthy();
    });

    it('goes away on its own rather than becoming something to dismiss', async () => {
      vi.useFakeTimers();
      try {
        renderTerminal();
        // Settle the hydrate/window fetches inside act so their resolution is
        // not mistaken for the timer this test is about.
        await act(async () => {
          await vi.advanceTimersByTimeAsync(0);
        });
        miss('outside-messages');
        expect(notice()).not.toBeNull();

        act(() => {
          vi.advanceTimersByTime(5000);
        });

        expect(notice()).toBeNull();
      } finally {
        vi.useRealTimers();
      }
    });
  });
});
