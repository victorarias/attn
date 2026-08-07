import { useCallback, useEffect, useRef, useState } from 'react';
import { useConversationsStore, selectConversation, type AgentPromptMode } from '../../store/conversations';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import type { ResolvedTheme } from '../../hooks/useTheme';
import { ToolCard } from './ToolCard';
import './ConversationPane.css';

interface ConversationPaneProps {
  sessionId: string;
  // Focused leaf of the visible session: only that pane's composer takes focus
  // on its own, so a split never steals the caret from the pane you are in.
  paneActive: boolean;
  // Passed through to the diff an edit tool's card draws.
  resolvedTheme?: ResolvedTheme;
}

/**
 * The surface a conversation session is drawn on, in place of a terminal.
 *
 * The whole pane is the host's envelope stream rendered: a transcript of what
 * the agent said and did, and a composer. It sends prompts and reads the store;
 * it holds no picture of the session that the stream did not give it.
 */
export function ConversationPane({ sessionId, paneActive, resolvedTheme }: ConversationPaneProps) {
  const conversation = useConversationsStore(selectConversation(sessionId));
  const promptSent = useConversationsStore((state) => state.promptSent);
  const { sendAgentPrompt, sendAgentClearQueue } = useDaemonApi();
  const [draft, setDraft] = useState('');
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);

  const { running, awaitingRun, ready, items, queue } = conversation;
  // Open for all of a run, shut only for the round trip that opens one. While
  // the run is live the two sends are steer and follow-up instead of prompt —
  // that is the whole difference, and it is why the user is never left with
  // something to say and nowhere to type it.
  const canSend = ready && !awaitingRun;
  const pending = [...queue.steering, ...queue.followUp];

  // Follow the stream. Only when the reader is already at the bottom: scrolling
  // back to re-read something must not be yanked away by the next delta.
  const lastLength = items.reduce(
    (total, item) => total + (item.kind === 'message' ? item.text.length : item.summary.length),
    0,
  );
  useEffect(() => {
    const list = listRef.current;
    if (!list) return;
    const distanceFromBottom = list.scrollHeight - list.scrollTop - list.clientHeight;
    if (distanceFromBottom < 80) {
      list.scrollTop = list.scrollHeight;
    }
  }, [lastLength, items.length]);

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
      <div className="conversation-pane-messages" ref={listRef} data-testid="conversation-messages">
        {items.length === 0 ? (
          <div className="conversation-pane-empty">
            {ready ? 'Ask this agent something.' : 'Starting the agent...'}
          </div>
        ) : (
          items.map((item) => (item.kind === 'tool' ? (
            <ToolCard
              key={`tool:${item.callId}`}
              sessionId={sessionId}
              tool={item}
              resolvedTheme={resolvedTheme}
            />
          ) : (
            <div
              key={`message:${item.id}`}
              className={`conversation-message conversation-message--${item.role}`}
              data-testid={`conversation-message-${item.id}`}
              data-role={item.role}
              data-streaming={item.streaming ? 'true' : 'false'}
            >
              <div className="conversation-message-role">{item.role}</div>
              <div className="conversation-message-text">{item.text}</div>
            </div>
          )))
        )}
      </div>
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
          placeholder={awaitingRun ? 'Sending...' : running ? 'Steer the agent' : ready ? 'Message the agent' : 'Waiting for the agent'}
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
