import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import {
  useConversationsStore,
  selectConversation,
  conversationItemKey,
  type AgentPromptMode,
  type ConversationItem,
} from '../../store/conversations';
import { useSessionStore } from '../../store/sessions';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import type { ResolvedTheme } from '../../hooks/useTheme';
import type { UISessionState } from '../../types/sessionState';
import { ToolCard } from './ToolCard';
import { Markdown, ReaderPresentation } from '../Markdown';
import { MarkdownBoundary } from '../Markdown/MarkdownBoundary';
import './ConversationPane.css';

interface ConversationPaneProps {
  sessionId: string;
  // Focused leaf of the visible session: only that pane's composer takes focus
  // on its own, so a split never steals the caret from the pane you are in.
  paneActive: boolean;
  // The daemon's word on the session. `recoverable` is the one this pane acts
  // on: the host died and its conversation is waiting in a session file.
  sessionState?: UISessionState;
  // Passed through to the diff an edit tool's card draws.
  resolvedTheme?: ResolvedTheme;
}

/** How much text an item contributes, for the follow-the-stream check. */
function itemLength(item: ConversationItem): number {
  if (item.kind === 'message') return item.text.length;
  if (item.kind === 'tool') return item.summary.length;
  return item.text.length;
}

/**
 * The surface a conversation session is drawn on, in place of a terminal.
 *
 * The whole pane is the host's envelope stream rendered: a transcript of what
 * the agent said and did, and a composer. It sends prompts and reads the store;
 * it holds no picture of the session that the stream did not give it.
 */
export function ConversationPane({ sessionId, paneActive, sessionState, resolvedTheme }: ConversationPaneProps) {
  const conversation = useConversationsStore(selectConversation(sessionId));
  const promptSent = useConversationsStore((state) => state.promptSent);
  const historyRequested = useConversationsStore((state) => state.historyRequested);
  const reloadSession = useSessionStore((state) => state.reloadSession);
  const { sendAgentPrompt, sendAgentClearQueue, sendAgentAttach, sendAgentHistory, sendAgentSetModel } = useDaemonApi();
  const [draft, setDraft] = useState('');
  const [reloading, setReloading] = useState(false);
  const [reloadError, setReloadError] = useState<string | null>(null);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);
  const attachedRef = useRef<string | null>(null);

  const { running, awaitingRun, ready, items, queue, lastSeq, hasMoreBefore, loadingHistory, droppedBefore, model, models } = conversation;
  const recoverable = sessionState === 'recoverable';

  // Ask the host for a snapshot when this client has never seen its stream: a
  // second window, or this one after a restart. `lastSeq` is the whole test —
  // it is 0 exactly when nothing from this host has been applied — and the ref
  // keeps a remount from asking twice for the same session.
  //
  // Only a session the daemon says is up gets asked. A launching one is about
  // to volunteer its own `session_ready` and snapshot; a recoverable one has no
  // host to answer and needs the reload below instead. Asking either would put
  // a command error on the socket describing a race rather than a fault.
  const hostShouldAnswer = sessionState !== undefined
    && sessionState !== 'launching'
    && sessionState !== 'recoverable'
    && sessionState !== 'unknown';
  useEffect(() => {
    if (!hostShouldAnswer || lastSeq > 0) return;
    if (attachedRef.current === sessionId) return;
    attachedRef.current = sessionId;
    sendAgentAttach(sessionId);
  }, [hostShouldAnswer, lastSeq, sendAgentAttach, sessionId]);
  // Open for all of a run, shut only for the round trip that opens one. While
  // the run is live the two sends are steer and follow-up instead of prompt —
  // that is the whole difference, and it is why the user is never left with
  // something to say and nowhere to type it.
  const canSend = ready && !awaitingRun;
  const pending = [...queue.steering, ...queue.followUp];

  // Follow the stream. Only when the reader is already at the bottom: scrolling
  // back to re-read something must not be yanked away by the next delta.
  //
  // Whether they are at the bottom is decided by the reader's own scrolling, not
  // by measuring after the delta landed. Markdown makes a delta grow the
  // document by a whole block — an opening code fence measured 133px in one
  // paint, against a tolerance of 80 — so a measurement taken afterwards reads
  // that growth as the reader having scrolled back, and follow mode never
  // returns. Appending content does not move scrollTop and fires no scroll
  // event, so this ref only moves when the reader (or the line below) moves it.
  const followingRef = useRef(true);
  const lastLength = items.reduce((total, item) => total + itemLength(item), 0);
  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list) return;
    if (followingRef.current) list.scrollTop = list.scrollHeight;
  }, [lastLength, items.length]);

  // A mermaid diagram appears one frame AFTER the text that carried it — its
  // fence settles, then mermaid draws, and the document grows with no delta to
  // notice. A follower would be left the diagram's height off the bottom.
  const followDiagramGrowth = useCallback(() => {
    const list = listRef.current;
    if (list && followingRef.current) list.scrollTop = list.scrollHeight;
  }, []);

  // Paging older history in puts content ABOVE what the reader is looking at,
  // and the browser keeps scrollTop — so the page they were reading slides down
  // the screen by however much arrived. Anchoring on the distance from the
  // bottom, which the prepend does not change, is what keeps the view still.
  //
  // The measurement is taken on scroll rather than only here, because the reader
  // goes on scrolling while a page is in flight.
  const oldestKey = items.length > 0 ? conversationItemKey(items[0]) : '';
  const anchorRef = useRef<{ key: string; fromBottom: number } | null>(null);
  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list) return;
    const anchor = anchorRef.current;
    if (anchor && anchor.key !== '' && anchor.key !== oldestKey) {
      list.scrollTop = list.scrollHeight - anchor.fromBottom;
    }
    anchorRef.current = { key: oldestKey, fromBottom: list.scrollHeight - list.scrollTop };
  }, [oldestKey]);

  // Ask for the conversation behind what is drawn. Addressed by the oldest item
  // held, which is also why only one can be in flight: a second request while
  // one is out would ask for the same page again.
  const loadEarlier = useCallback(() => {
    if (!hasMoreBefore || loadingHistory || items.length === 0) return;
    historyRequested(sessionId);
    sendAgentHistory(sessionId, conversationItemKey(items[0]));
  }, [hasMoreBefore, historyRequested, items, loadingHistory, sendAgentHistory, sessionId]);

  const handleScroll = useCallback(() => {
    const list = listRef.current;
    if (!list) return;
    anchorRef.current = { key: oldestKey, fromBottom: list.scrollHeight - list.scrollTop };
    followingRef.current = list.scrollHeight - list.scrollTop - list.clientHeight < 80;
    // Fetch before the reader arrives at the top. The threshold is a screen of
    // reading, so on a fast host the page has landed by the time they get there
    // and the conversation just keeps going up.
    if (list.scrollTop < list.clientHeight) loadEarlier();
  }, [loadEarlier, oldestKey]);

  // The composer opens the moment the run closes, with the caret already in it.
  useEffect(() => {
    if (paneActive && canSend) inputRef.current?.focus();
  }, [paneActive, canSend]);

  const send = useCallback((mode: AgentPromptMode) => {
    const text = draft.trim();
    if (!text || !canSend) return;
    sendAgentPrompt(sessionId, text, mode);
    if (mode === 'prompt') {
      // Open the run now, not when the host reports it: the acknowledgement is
      // a round trip away and a second prompt inside it is refused by the host
      // with only a log line. See promptSent. A steer or follow-up needs none
      // of this — the run is already open, and what the agent has not read yet
      // comes back as a queue_update.
      promptSent(sessionId);
    }
    setDraft('');
  }, [canSend, draft, promptSent, sendAgentPrompt, sessionId]);

  // Bring the conversation back. The daemon relaunches the host from this
  // session's stored launch intent and the replacement reopens the same session
  // file, so what comes back is this conversation and not a new one. Reload is
  // also in the session actions menu; it is here because this pane is where the
  // user finds out, and a dead conversation with no visible way back is a
  // one-way door.
  const reload = useCallback(() => {
    setReloading(true);
    setReloadError(null);
    void reloadSession(sessionId)
      .catch((error: unknown) => {
        setReloadError(error instanceof Error ? error.message : String(error));
      })
      .finally(() => setReloading(false));
  }, [reloadSession, sessionId]);

  // What Enter does. A run in progress makes it a steer — the interruption is
  // the common case while an agent works, and the follow-up is the one you go
  // out of your way for.
  const primary: AgentPromptMode = running ? 'steer' : 'prompt';

  const handleKeyDown = useCallback((event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // Enter sends, Shift+Enter breaks the line. Same bargain every chat
    // composer in this app makes.
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      send(primary);
    }
  }, [primary, send]);

  return (
    <div className="conversation-pane" data-testid={`conversation-pane-${sessionId}`}>
      {models.length > 0 && (
        <div className="conversation-pane-header" data-testid="conversation-header">
          <label className="conversation-model-label" htmlFor={`conversation-model-${sessionId}`}>Model</label>
          {/* The value is the host's word, not this select's: a switch pi
              refuses comes back as the model still in force and the picker
              snaps back to it. */}
          <select
            id={`conversation-model-${sessionId}`}
            className="conversation-model"
            data-testid="conversation-model"
            value={models.includes(model) ? model : ''}
            disabled={!ready}
            onChange={(event) => {
              if (event.target.value !== '') sendAgentSetModel(sessionId, event.target.value);
            }}
          >
            {!models.includes(model) && <option value="">{model || 'Unknown'}</option>}
            {models.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
        </div>
      )}
      <div
        className="conversation-pane-messages"
        ref={listRef}
        onScroll={handleScroll}
        data-testid="conversation-messages"
      >
        {droppedBefore > 0 && !hasMoreBefore && (
          // The conversation is longer than anything anyone can still be shown:
          // the host's retention budget dropped the start for good, and no page
          // will answer for it. Saying so is the difference between a history
          // that begins mid-thought and one that explains why.
          <div
            className="conversation-notice conversation-pane-dropped"
            data-testid="conversation-history-dropped"
            data-dropped={droppedBefore}
          >
            {`${droppedBefore.toLocaleString()} earlier ${droppedBefore === 1 ? 'item' : 'items'} are no longer kept for this conversation.`}
          </div>
        )}
        {hasMoreBefore && (
          // Auto-loading covers the reader who scrolls; this covers the one
          // whose transcript is shorter than the pane, where there is no room
          // to scroll and therefore no scroll event to fire.
          <button
            type="button"
            className="conversation-pane-earlier"
            data-testid="conversation-load-earlier"
            disabled={loadingHistory}
            onClick={loadEarlier}
          >
            {loadingHistory ? 'Loading earlier messages...' : 'Load earlier messages'}
          </button>
        )}
        {items.length === 0 ? (
          <div className="conversation-pane-empty">
            {ready ? 'Ask this agent something.' : recoverable ? '' : 'Starting the agent...'}
          </div>
        ) : (
          items.map((item) => {
            if (item.kind === 'tool') {
              return (
                <ToolCard
                  key={`tool:${item.callId}`}
                  sessionId={sessionId}
                  tool={item}
                  resolvedTheme={resolvedTheme}
                />
              );
            }
            if (item.kind === 'notice') {
              return (
                <div
                  key={`notice:${item.id}`}
                  className={`conversation-notice conversation-notice--${item.level}`}
                  data-testid={`conversation-notice-${item.id}`}
                  data-level={item.level}
                  data-done={item.done ? 'true' : 'false'}
                >
                  {item.text}
                </div>
              );
            }
            return (
              <div
                key={`message:${item.id}`}
                className={`conversation-message conversation-message--${item.role}`}
                data-testid={`conversation-message-${item.id}`}
                data-role={item.role}
                data-streaming={item.streaming ? 'true' : 'false'}
              >
                <div className="conversation-message-role">{item.role}</div>
                {/* The agent writes markdown; the user writes into a textarea,
                    where Enter is a line break the way it is in every other
                    composer in this app. Hence `breaks` on one side only. */}
                <MarkdownBoundary
                  key={`md:${item.id}`}
                  fallback={<div className="conversation-message-text conversation-message-text--raw">{item.text}</div>}
                >
                  {/* A transcript is read, not glanced at: a diagram too wide for
                      the column gets the reader's own size detection, focus view
                      and zoom rather than being silently squeezed. */}
                  <ReaderPresentation>
                  <Markdown
                    className="conversation-message-text"
                    breaks={item.role === 'user'}
                    streaming={item.streaming}
                    onDiagramLayoutChange={followDiagramGrowth}
                  >
                    {item.text}
                  </Markdown>
                  </ReaderPresentation>
                </MarkdownBoundary>
              </div>
            );
          })
        )}
      </div>
      {recoverable && (
        <div className="conversation-pane-recoverable" data-testid="conversation-recoverable">
          <span className="conversation-pane-recoverable-text">
            {reloadError ?? 'This agent stopped. Reload to pick the conversation back up.'}
          </span>
          <button
            type="button"
            className="conversation-pane-reload"
            data-testid="conversation-reload"
            disabled={reloading}
            onClick={reload}
          >
            {reloading ? 'Reloading...' : 'Reload'}
          </button>
        </div>
      )}
      {pending.length > 0 && (
        <div className="conversation-pane-queue" data-testid="conversation-queue">
          <div className="conversation-pane-queue-header">
            <span className="conversation-queued-label">Not read yet</span>
            {/* pi clears both queues or neither — it offers no way to drop one
                entry — so this says what it does. The strip empties on pi's own
                queue_update, not on this click. */}
            <button
              type="button"
              className="conversation-queue-clear"
              data-testid="conversation-queue-clear"
              title="Drop everything the agent has not read yet"
              onClick={() => sendAgentClearQueue(sessionId)}
            >
              Cancel all
            </button>
          </div>
          {pending.map((entry, index) => (
            <div className="conversation-queued" key={`${index}-${entry}`} data-testid="conversation-queued">
              <span className="conversation-queued-label">
                {index < queue.steering.length ? 'Steering' : 'Follow-up'}
              </span>
              <span className="conversation-queued-text">{entry}</span>
            </div>
          ))}
        </div>
      )}
      <div className="conversation-pane-composer">
        <textarea
          ref={inputRef}
          className="conversation-pane-input"
          data-testid="conversation-input"
          value={draft}
          disabled={!canSend}
          placeholder={awaitingRun ? 'Sending...' : running ? 'Steer the agent' : ready ? 'Message the agent' : recoverable ? 'Reload to continue' : 'Waiting for the agent'}
          rows={2}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={handleKeyDown}
        />
        {running && (
          <button
            type="button"
            className="conversation-pane-followup"
            data-testid="conversation-follow-up"
            title="Queue this until the agent finishes what it is doing"
            disabled={!canSend || draft.trim() === ''}
            onClick={() => send('follow_up')}
          >
            Follow up
          </button>
        )}
        <button
          type="button"
          className="conversation-pane-send"
          data-testid="conversation-send"
          disabled={!canSend || draft.trim() === ''}
          onClick={() => send(primary)}
        >
          {running ? 'Steer' : 'Send'}
        </button>
      </div>
    </div>
  );
}
