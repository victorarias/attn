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
  onAnnotationActivate?: (annotationId: string, at: { clientX: number; clientY: number }) => void;
}

let terminal: CapturedProps = {};

vi.mock('../GhosttyTerminal', () => ({
  GhosttyTerminal: React.forwardRef(function MockTerminal(props: CapturedProps, _ref: React.Ref<unknown>) {
    terminal = props;
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
  generation = 0;
  tombstone = 0;
  calls = { fetchMessages: 0, fetchAnnotations: 0, saveAnnotations: 0, clearAnnotations: 0 };
  // Set to fail the next save the way a competing writer would, so the client's
  // stale path can be driven without a second component in the test.
  stealNextSave: TerminalAnnotation[] | null = null;

  fetchMessages = async (_sessionId: string) => {
    this.calls.fetchMessages += 1;
    return { messages: this.messages.map((message) => ({ ...message })), truncated: this.truncated };
  };

  fetchAnnotations = async (_sessionId: string) => {
    this.calls.fetchAnnotations += 1;
    return {
      annotations: this.annotations.map((annotation) => ({ ...annotation })),
      generation: this.generation,
    };
  };

  saveAnnotations = async (
    _sessionId: string,
    annotations: readonly TerminalAnnotation[],
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
    this.generation = generation;
    return { stale: false };
  };

  clearAnnotations = async (_sessionId: string, generation: number) => {
    this.calls.clearAnnotations += 1;
    if (generation > this.tombstone) this.tombstone = generation;
    if (generation > this.generation) {
      this.annotations = [];
      this.generation = generation;
    }
    return { generation: this.generation };
  };
}

function props(overrides: {
  state?: UISessionState;
  api?: SessionAnnotationApi;
  submit?: (text: string) => void;
}) {
  return {
    sessionId: 'session-1',
    sessionState: overrides.state ?? ('idle' as UISessionState),
    annotationApi: overrides.api,
    onSubmitAnnotations: overrides.submit,
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
  submit?: (text: string) => void;
} = {}) {
  const daemon = overrides.api ?? new FakeAnnotationDaemon();
  const submit = overrides.submit ?? vi.fn();
  const view = render(<AnnotatedTerminal {...props({ ...overrides, api: daemon, submit })} />);
  const rerender = (next: { state?: UISessionState } = {}) =>
    view.rerender(
      <AnnotatedTerminal {...props({ state: next.state ?? overrides.state, api: daemon, submit })} />,
    );
  return { ...view, rerender, daemon, submit };
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

/** Drives the alt-click the terminal reports over an existing wash. */
function activate(annotationId: string) {
  act(() => {
    terminal.onAnnotationActivate?.(annotationId, { clientX: 140, clientY: 220 });
  });
}

function stored() {
  return terminal.annotations?.list() ?? [];
}

beforeEach(() => {
  terminal = {};
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
    fireEvent.click(screen.getByLabelText('Needs tests'));
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
    fireEvent.click(screen.getByLabelText('Needs tests'));
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
    fireEvent.click(screen.getByLabelText('Needs tests'));

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

  it('types the whole set into the session and clears it', async () => {
    const submit = vi.fn();
    renderTerminal({ submit });
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Verify this'));
    anchor('turn-1', 31, 55);
    fireEvent.click(screen.getByLabelText('Needs tests'));

    fireEvent.click(screen.getByText('Send all'));

    expect(submit).toHaveBeenCalledTimes(1);
    const payload = submit.mock.calls[0][0] as string;
    expect(payload).toContain(TURN_1.slice(0, 26));
    expect(payload).toContain(TURN_1.slice(31, 55));
    // Sending is the end of the set: leaving it behind would re-send the same
    // feedback on the next click.
    expect(stored()).toHaveLength(0);
    expect(screen.getByText(/typed 2 into the session/)).toBeTruthy();
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

    fireEvent.click(screen.getByLabelText('Needs tests'));

    expect(stored()).toHaveLength(1);
    expect(stored()[0].emoji).toBe('🧪');
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

  it('offers no annotation surface when the session cannot be sent to', async () => {
    // Without a submit path an annotation has nowhere to go, so the terminal is
    // never given a store and the alt-drag stays an ordinary selection.
    const daemon = new FakeAnnotationDaemon();
    render(<AnnotatedTerminal {...props({ api: daemon, submit: undefined })} />);

    await act(async () => {});
    expect(terminal.annotations).toBeUndefined();
    expect(terminal.onAnnotationAnchor).toBeUndefined();
    expect(daemon.calls.fetchMessages).toBe(0);
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
      emoji: '🧪',
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
    }], daemon.tombstone);
    expect(late.stale).toBe(true);
    expect(daemon.annotations).toHaveLength(0);
  });
});
