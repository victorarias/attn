/**
 * SessionReviewLoopBar Test Harness
 *
 * Renders SessionReviewLoopBar with configurable loop states for visual inspection.
 */
import { useEffect, useMemo, useState } from 'react';
import { SessionReviewLoopBar } from '../../src/components/SessionReviewLoopBar';
import { SettingsProvider } from '../../src/contexts/SettingsContext';
import type { ReviewLoopState } from '../../src/hooks/useDaemonSocket';
import type { HarnessProps } from '../types';
import '../../src/components/SessionReviewLoopBar.css';

function createLoopState(status: string, overrides: Partial<ReviewLoopState> = {}): ReviewLoopState {
  const base: ReviewLoopState = {
    loop_id: 'loop-1',
    source_session_id: 'session-1',
    repo_path: '/test/repo',
    status: status as ReviewLoopState['status'],
    resolved_prompt: 'Review the diff and improve the implementation.',
    iteration_count: 1,
    iteration_limit: 3,
    stop_requested: false,
    advance_token: 'token',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    latest_iteration: {
      id: 'iter-1',
      loop_id: 'loop-1',
      iteration_number: 1,
      status: 'running',
      summary: '## Progress\n\n- Reviewing changed files\n- Checking edge cases',
      result_text: '',
      changes_made: true,
      files_touched: ['app/src/App.tsx'],
      assistant_trace_json: 'Starting review loop...\n\nInspecting `app/src/App.tsx`',
      started_at: new Date().toISOString(),
    },
  };

  return {
    ...base,
    ...overrides,
    latest_iteration: overrides.latest_iteration
      ? { ...base.latest_iteration, ...overrides.latest_iteration }
      : base.latest_iteration,
  };
}

export function SessionReviewLoopBarHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const mode = params.get('mode') || 'running';
  const [tick, setTick] = useState(0);

  useEffect(() => {
    setTriggerRerender(() => setTick((value) => value + 1));
  }, [setTriggerRerender]);

  useEffect(() => {
    const timer = setTimeout(() => onReady(), 150);
    return () => clearTimeout(timer);
  }, [onReady]);

  useEffect(() => {
    if (mode !== 'live') {
      return;
    }
    const interval = window.setInterval(() => {
      setTick((value) => value + 1);
    }, 900);
    return () => window.clearInterval(interval);
  }, [mode]);

  const loopState = useMemo<ReviewLoopState | null>(() => {
    if (mode === 'empty') {
      return null;
    }

    if (mode === 'awaiting') {
      return createLoopState('awaiting_user', {
        iteration_count: 2,
        latest_iteration: {
          iteration_number: 2,
          status: 'awaiting_user',
          summary: '## Need clarification\n\nThe loop needs one decision before continuing.',
          assistant_trace_json: 'Completed the second pass.\n\nBlocked on deployment target choice.',
          files_touched: ['app/src/App.tsx', 'app/src/components/Sidebar.tsx'],
        },
        pending_interaction: {
          id: 'interaction-1',
          loop_id: 'loop-1',
          iteration_id: 'iter-2',
          kind: 'question_answer',
          question: 'Should the dock prefer a wider default on laptop layouts?',
          status: 'pending',
          created_at: new Date().toISOString(),
        },
      });
    }

    if (mode === 'completed') {
      return createLoopState('completed', {
        iteration_count: 3,
        last_result_summary: '## Final pass\n\n- Converged cleanly\n- No additional follow-up found',
        latest_iteration: {
          iteration_number: 3,
          status: 'completed',
          summary: '## Final pass\n\n- Converged cleanly\n- No additional follow-up found',
          assistant_trace_json: 'All requested checks completed.\n\nNo new issues found.',
          files_touched: [
            'app/src/App.tsx',
            'app/src/components/SessionReviewLoopBar.tsx',
            'internal/daemon/review_loop.go',
          ],
        },
        completed_at: new Date().toISOString(),
      });
    }

    if (mode === 'live') {
      const files = [
        'internal/daemon/review_loop.go',
        'app/src/components/SessionReviewLoopBar.tsx',
        'app/src/components/SessionReviewLoopBar.css',
        'app/src/App.tsx',
      ].slice(0, Math.min(4, 1 + (tick % 4)));

      const logLines = [
        'Starting SDK review loop...',
        'Inspecting `internal/daemon/review_loop.go`',
        'Tracking tool inputs for live touched files',
        'Preparing markdown summary output',
      ].slice(0, Math.min(4, 1 + (tick % 4)));

      return createLoopState('running', {
        latest_iteration: {
          status: 'running',
          summary: `## Live summary\n\n- ${files.length} file${files.length === 1 ? '' : 's'} touched so far\n- Streaming assistant trace below`,
          assistant_trace_json: logLines.join('\n\n'),
          files_touched: files,
        },
      });
    }

    return createLoopState('running');
  }, [mode, tick]);

  return (
    <SettingsProvider settings={{ review_loop_model: 'claude-sonnet-4-6' }} setSetting={() => {}}>
      <div style={{ minHeight: '100vh', background: '#0d0d0f', position: 'relative' }}>
        <div style={{ position: 'absolute', inset: 0, background: 'linear-gradient(135deg, rgba(255,107,53,0.08), transparent 45%)' }} />
        <div style={{ position: 'absolute', inset: 0 }}>
          <SessionReviewLoopBar
            sessionId="session-1"
            sessionLabel="attn"
            loopState={loopState}
            onClose={() => window.__HARNESS__.recordCall('onClose', [])}
            waitingReviewSessions={mode === 'awaiting' ? [{
              sessionId: 'session-1',
              label: 'attn',
              loopState: loopState!,
            }, {
              sessionId: 'session-2',
              label: 'api',
              loopState: createLoopState('awaiting_user', {
                source_session_id: 'session-2',
                iteration_count: 1,
                latest_iteration: {
                  iteration_number: 1,
                  status: 'awaiting_user',
                  files_touched: ['internal/daemon/websocket.go'],
                  assistant_trace_json: 'Question pending for api session.',
                },
                pending_interaction: {
                  id: 'interaction-2',
                  loop_id: 'loop-2',
                  kind: 'question_answer',
                  question: 'Should this also update the websocket retry path?',
                  status: 'pending',
                  created_at: new Date().toISOString(),
                },
              }),
            }] : []}
            onSelectSession={(sessionId) => window.__HARNESS__.recordCall('onSelectSession', [sessionId])}
            onStart={async (prompt, iterationLimit, presetId) => {
              window.__HARNESS__.recordCall('onStart', [prompt, iterationLimit, presetId]);
            }}
            onStop={async () => {
              window.__HARNESS__.recordCall('onStop', []);
            }}
            onSetIterations={async (iterationLimit) => {
              window.__HARNESS__.recordCall('onSetIterations', [iterationLimit]);
            }}
            onAnswer={async (loopId, interactionId, answer) => {
              window.__HARNESS__.recordCall('onAnswer', [loopId, interactionId, answer]);
            }}
          />
        </div>
      </div>
    </SettingsProvider>
  );
}
