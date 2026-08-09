import { useCallback, useMemo, useState } from 'react';
import { PatchDiff } from '@pierre/diffs/react';
import type { FileDiffOptions } from '@pierre/diffs';
import type { ResolvedTheme } from '../../hooks/useTheme';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import type { ConversationToolCall } from '../../store/conversations';

interface ToolCardProps {
  sessionId: string;
  tool: ConversationToolCall;
  resolvedTheme?: ResolvedTheme;
}

const statusLabel: Record<ConversationToolCall['status'], string> = {
  running: 'running',
  ok: 'done',
  error: 'failed',
};

/**
 * One tool call in the transcript, collapsed to a line until it is opened.
 *
 * Collapsed is the whole point. The card shows what the agent did — the tool's
 * name, the command or path it did it to, whether it worked — and none of what
 * it read or printed. A transcript of a long session is mostly tool output, and
 * keeping that output out of the app until someone asks for it is why a session
 * that has run a thousand tools still scrolls.
 *
 * Opening the card asks the host for the detail. It arrives as its own envelope
 * and lands in the store, so the card fills in when it does; nothing here waits
 * on a promise, and the same card opened in two windows costs one read.
 */
export function ToolCard({ sessionId, tool, resolvedTheme = 'dark' }: ToolCardProps) {
  const { sendAgentToolDetail } = useDaemonApi();
  const [expanded, setExpanded] = useState(false);
  // What has been asked for, so re-opening a card does not ask again and the
  // "show the whole thing" button does not fire twice on a slow read.
  const [requested, setRequested] = useState<'none' | 'clipped' | 'full'>('none');

  const detail = tool.detail;

  const fetchDetail = useCallback((full: boolean) => {
    sendAgentToolDetail(sessionId, tool.callId, full);
    setRequested(full ? 'full' : 'clipped');
  }, [sendAgentToolDetail, sessionId, tool.callId]);

  // The read is decided here rather than inside the updater: React may replay a
  // state updater, and a replayed one would ask the host for the same detail
  // twice.
  const toggle = useCallback(() => {
    const opening = !expanded;
    if (opening && tool.hasDetail && !detail && requested === 'none') fetchDetail(false);
    setExpanded(opening);
  }, [detail, expanded, fetchDetail, requested, tool.hasDetail]);

  // The patch pi hands us is already a unified diff against the file as it was,
  // so it is rendered as one rather than reconstructed into before/after text —
  // reconstruction from a 4-line-context patch can only guess at the rest of
  // the file, and guesses show up as wrong line numbers in the gutter.
  const patchOptions = useMemo<FileDiffOptions<undefined>>(() => ({
    diffStyle: 'unified',
    expandUnchanged: false,
    diffIndicators: 'classic',
    theme: { dark: 'pierre-dark', light: 'pierre-light' },
    themeType: resolvedTheme,
    // Same reason as DiffView: the pure-JS Shiki engine, because WASM fetching
    // inside the Tauri webview is unreliable under the custom protocol + CSP.
    preferredHighlighter: 'shiki-js',
  }), [resolvedTheme]);

  const waiting = expanded && tool.hasDetail && !detail;

  return (
    <div
      className={`conversation-tool conversation-tool--${tool.status}`}
      data-testid={`conversation-tool-${tool.callId}`}
      data-tool-name={tool.name}
      data-tool-status={tool.status}
      data-expanded={expanded ? 'true' : 'false'}
    >
      <button
        type="button"
        className="conversation-tool-header"
        data-testid="conversation-tool-toggle"
        aria-expanded={expanded}
        // A card with nothing behind it still opens: the header says so, and a
        // control that silently does nothing is worse than one that explains.
        onClick={toggle}
      >
        <span className="conversation-tool-caret" aria-hidden="true">{expanded ? '▾' : '▸'}</span>
        <span className="conversation-tool-name">{tool.name || 'tool'}</span>
        <span className="conversation-tool-summary">{tool.summary}</span>
        <span className="conversation-tool-status">{statusLabel[tool.status]}</span>
      </button>
      {tool.error && (
        <div className="conversation-tool-error" data-testid="conversation-tool-error">{tool.error}</div>
      )}
      {expanded && (
        <div className="conversation-tool-body" data-testid="conversation-tool-body">
          {tool.files.length > 0 && (
            <div className="conversation-tool-files" data-testid="conversation-tool-files">
              {tool.files.map((file) => <code key={file}>{file}</code>)}
            </div>
          )}
          {!tool.hasDetail && (
            <div className="conversation-tool-note">
              {tool.status === 'running' ? 'Still running.' : 'This tool produced no output.'}
            </div>
          )}
          {waiting && <div className="conversation-tool-note" data-testid="conversation-tool-waiting">Loading output...</div>}
          {detail?.error && (
            <div className="conversation-tool-note conversation-tool-note--error" data-testid="conversation-tool-detail-error">
              {detail.error}
              <button
                type="button"
                className="conversation-tool-action"
                data-testid="conversation-tool-retry"
                onClick={() => fetchDetail(requested === 'full')}
              >
                Try again
              </button>
            </div>
          )}
          {detail?.patch && (
            <div className="conversation-tool-patch" data-testid="conversation-tool-patch">
              <PatchDiff patch={detail.patch} options={patchOptions} disableWorkerPool />
            </div>
          )}
          {detail && detail.text !== '' && (
            <pre className="conversation-tool-output" data-testid="conversation-tool-output">{detail.text}</pre>
          )}
          {detail?.truncated && !detail.full && (
            // pi keeps the whole output on disk when it clips what it gives the
            // model, so the clip is a display decision the user can undo. When
            // there is no file behind it, say that instead of offering a button
            // that cannot deliver. A `full` answer that is still truncated hit
            // the host's own read cap and says so in its error, so it needs
            // nothing here.
            <div className="conversation-tool-note">
              {tool.fullOutput ? (
                <button
                  type="button"
                  className="conversation-tool-action"
                  data-testid="conversation-tool-full"
                  disabled={requested === 'full'}
                  onClick={() => fetchDetail(true)}
                >
                  {requested === 'full' ? 'Loading the full output...' : 'Show the full output'}
                </button>
              ) : (
                <span data-testid="conversation-tool-truncated">The rest of this output was not kept.</span>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
