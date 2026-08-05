import { useCallback, useEffect, useRef, useState } from 'react';
import { useConversationsStore, selectConversation } from '../../store/conversations';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import './ConversationPane.css';

interface ConversationPaneProps {
  sessionId: string;
  // Focused leaf of the visible session: only that pane's composer takes focus
  // on its own, so a split never steals the caret from the pane you are in.
  paneActive: boolean;
}

/**
 * The surface a conversation session is drawn on, in place of a terminal.
 *
 * The whole pane is the host's envelope stream rendered: a message list and a
 * composer. It sends prompts and reads the store; it holds no picture of the
 * session that the stream did not give it.
 */
export function ConversationPane({ sessionId, paneActive }: ConversationPaneProps) {
  const conversation = useConversationsStore(selectConversation(sessionId));
  const promptSent = useConversationsStore((state) => state.promptSent);
  const { sendAgentPrompt } = useDaemonApi();
  const [draft, setDraft] = useState('');
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);

  const { running, ready, messages } = conversation;
  const canSend = ready && !running;

  // Follow the stream. Only when the reader is already at the bottom: scrolling
  // back to re-read something must not be yanked away by the next delta.
  const lastLength = messages.reduce((total, message) => total + message.text.length, 0);
  useEffect(() => {
    const list = listRef.current;
    if (!list) return;
    const distanceFromBottom = list.scrollHeight - list.scrollTop - list.clientHeight;
    if (distanceFromBottom < 80) {
      list.scrollTop = list.scrollHeight;
    }
  }, [lastLength, messages.length]);

  // The composer opens the moment the run closes, with the caret already in it.
  useEffect(() => {
    if (paneActive && canSend) inputRef.current?.focus();
  }, [paneActive, canSend]);

  const submit = useCallback(() => {
    const text = draft.trim();
    if (!text || !canSend) return;
    sendAgentPrompt(sessionId, text);
    // Shut the composer now, not when the host reports the run open: the
    // acknowledgement is a round trip away and a second Enter inside it is
    // refused by the host with only a log line. See promptSent.
    promptSent(sessionId);
    setDraft('');
  }, [canSend, draft, promptSent, sendAgentPrompt, sessionId]);

  const handleKeyDown = useCallback((event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // Enter sends, Shift+Enter breaks the line. Same bargain every chat
    // composer in this app makes.
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      submit();
    }
  }, [submit]);

  return (
    <div className="conversation-pane" data-testid={`conversation-pane-${sessionId}`}>
      <div className="conversation-pane-messages" ref={listRef} data-testid="conversation-messages">
        {messages.length === 0 ? (
          <div className="conversation-pane-empty">
            {ready ? 'Ask this agent something.' : 'Starting the agent...'}
          </div>
        ) : (
          messages.map((message) => (
            <div
              key={message.id}
              className={`conversation-message conversation-message--${message.role}`}
              data-testid={`conversation-message-${message.id}`}
              data-role={message.role}
              data-streaming={message.streaming ? 'true' : 'false'}
            >
              <div className="conversation-message-role">{message.role}</div>
              <div className="conversation-message-text">{message.text}</div>
            </div>
          ))
        )}
      </div>
      <div className="conversation-pane-composer">
        <textarea
          ref={inputRef}
          className="conversation-pane-input"
          data-testid="conversation-input"
          value={draft}
          disabled={!canSend}
          placeholder={running ? 'Working...' : ready ? 'Message the agent' : 'Waiting for the agent'}
          rows={2}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={handleKeyDown}
        />
        <button
          type="button"
          className="conversation-pane-send"
          data-testid="conversation-send"
          disabled={!canSend || draft.trim() === ''}
          onClick={submit}
        >
          Send
        </button>
      </div>
    </div>
  );
}
