import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { DaemonTour, DaemonTourDraft } from '../../src/hooks/useDaemonSocket';
import { TourPanel } from '../../src/components/TourPanel';
import { TourConnectionState, TourStatus } from '../../src/types/generated';
import type { HarnessProps } from '../types';

function generateFileContents(prefix: string): string {
  return Array.from(
    { length: 180 },
    (_, index) => `export const value_${String(index).padStart(3, '0')} = '${prefix}-${index}';`,
  ).join('\n');
}

const initialTour: DaemonTour = {
  tour_id: 'tour-panel-harness',
  session_id: 'session-1',
  name: 'Tour panel scroll harness',
  repo_path: '/repo',
  guide_path: '/home/user/.attn/tours/guide.yml',
  base_ref: 'main',
  status: TourStatus.Active,
  connection_state: TourConnectionState.Connected,
  summary: '# Harness briefing',
  warnings: [],
  files: [{
    path: 'src/large.ts',
    status: 'modified',
    additions: 180,
    deletions: 180,
    group: 'tour',
    view: 'diff',
    note: 'Review the complete generated table.',
    original: generateFileContents('before'),
    modified: generateFileContents('after'),
    annotations: [],
  }],
  drafts: [],
  transcript: [],
  listener_event_seq: 0,
  created_at: '2026-06-10T10:00:00Z',
  updated_at: '2026-06-10T10:00:00Z',
};

export function TourPanelHarness({ onReady }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const longGuidance = params.get('guidance') === 'long';
  const markdownFile = params.get('file') === 'markdown';
  const outsideAnnotation = params.get('file') === 'outside-annotation';
  const uiScale = Number.parseFloat(params.get('scale') || '1');
  const backgroundKeysRef = useRef<string[]>([]);
  const [tour, setTour] = useState<DaemonTour>(() => {
    if (markdownFile) {
      return {
        ...initialTour,
        files: initialTour.files.map((file) => ({
          ...file,
          path: 'docs/tour-reader.md',
          view: 'content',
          note: 'Review the rendered document, then inspect its source changes.',
          original: '',
          modified: [
            '# Rendered Tour document',
            '',
            'The Tour now presents **Markdown as a document** before asking you to inspect syntax.',
            '',
            '## Review path',
            '',
            '- Read the rendered structure.',
            '- Switch to Changes when exact edits matter.',
            '- Switch back without losing your place in the Tour.',
            '',
            '> The selected display mode belongs to this file.',
          ].join('\n'),
          additions: 8,
          deletions: 1,
          annotations: [{
            id: 'markdown-review-path',
            line_start: 5,
            line_end: 9,
            comments: [{
              author: 'agent',
              body: 'Verify the rendered structure and the exact source tell the same story.',
            }],
          }],
        })),
      };
    }
    if (outsideAnnotation) {
      const original = generateFileContents('stable');
      const modified = original
        .split('\n')
        .map((line, index) => index === 1 ? "export const value_001 = 'changed';" : line)
        .join('\n');
      return {
        ...initialTour,
        files: initialTour.files.map((file) => ({
          ...file,
          original,
          modified,
          additions: 1,
          deletions: 1,
          annotations: [{
            id: 'outside-diff-note',
            line_start: 150,
            line_end: 150,
            comments: [{
              author: 'agent',
              body: 'This note needs local context even though the line did not change.',
            }],
          }],
        })),
      };
    }
    if (longGuidance) {
      return {
        ...initialTour,
        files: initialTour.files.map((file) => ({
          ...file,
          note: [
            '# A deliberately large reading lens',
            '',
            ...Array.from(
              { length: 12 },
              (_, index) => `Paragraph ${index + 1}: preserve access to the diff while authored guidance remains readable.`,
            ),
            '',
            '```mermaid',
            'flowchart LR',
            '  Guide --> Diff',
            '  Diff --> Comment',
            '```',
          ].join('\n\n'),
          risk_note: Array.from(
            { length: 12 },
            (_, index) => `Risk ${index + 1}: verify the bounded guidance area cannot consume the code viewport.`,
          ).join('\n\n'),
        })),
      };
    }
    return initialTour;
  });

  window.localStorage.setItem(`attn.tour.briefing.${initialTour.tour_id}`, '1');

  const saveTourDraft = useCallback(async (_tourId: string, draft: DaemonTourDraft) => {
    setTour((current) => ({
      ...current,
      drafts: [...current.drafts.filter((entry) => entry.path !== draft.path), draft],
    }));
    return tour;
  }, [tour]);

  const returnTour = useCallback(async () => tour, [tour]);
  const props = useMemo(() => ({
    refreshTour: returnTour,
    saveTourDraft,
    askTour: returnTour,
    submitTour: returnTour,
  }), [returnTour, saveTourDraft]);

  useEffect(() => {
    const timer = window.setTimeout(onReady, 400);
    return () => window.clearTimeout(timer);
  }, [onReady]);

  useEffect(() => {
    const api = window.__HARNESS__ as unknown as Record<string, unknown>;
    api.refreshTourSnapshot = () => {
      setTour((current) => ({
        ...current,
        files: current.files.map((file) => ({
          ...file,
          annotations: file.annotations.map((annotation) => ({
            ...annotation,
            comments: annotation.comments.map((comment) => ({ ...comment })),
          })),
        })),
        drafts: current.drafts.map((draft) => ({
          ...draft,
          annotation_replies: draft.annotation_replies.map((reply) => ({ ...reply })),
          line_comments: draft.line_comments.map((comment) => ({ ...comment })),
        })),
        updated_at: new Date().toISOString(),
      }));
    };
    api.backgroundKeys = backgroundKeysRef.current;
  }, []);

  return (
    <>
      <input
        className="terminal-container"
        data-testid="tour-background-terminal"
        onKeyDown={(event) => backgroundKeysRef.current.push(event.key)}
      />
      <TourPanel
        tour={tour}
        resolvedTheme="dark"
        uiScale={uiScale}
        onClose={() => {}}
        {...props}
      />
    </>
  );
}
