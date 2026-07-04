import { describe, expect, it } from 'vitest';
import { presentationNeedsNotice, seedPresentationNotices, upsertPresentationNotice } from './App';
import type { Presentation } from './types/generated';

// Pure-logic coverage for the main-window presentation banner's notice list.
// App.tsx itself has no dedicated render-level test suite for its banner
// stack (see App.sessionlessWorkspace.test.tsx / App.worktreeCleanup.test.tsx
// for the render-level tests that do exist, none of which touch banners), so
// this exercises the extracted reducer functions directly rather than
// standing up a full App render + mocked WebSocket.

function makePresentation(overrides: Partial<Presentation> = {}): Presentation {
  return {
    id: 'p1',
    created_at: '2026-07-01T00:00:00Z',
    kind: 'pr',
    latest_round_seq: 1,
    latest_round_submitted: false,
    repo_path: '/repo',
    session_id: 'session-1',
    status: 'open',
    title: 'My change',
    ...overrides,
  };
}

describe('presentationNeedsNotice', () => {
  it('is true for an open, unsubmitted presentation', () => {
    expect(presentationNeedsNotice(makePresentation())).toBe(true);
  });

  it('is false once the latest round is submitted', () => {
    expect(presentationNeedsNotice(makePresentation({ latest_round_submitted: true }))).toBe(false);
  });

  it('is false when the presentation is not open', () => {
    expect(presentationNeedsNotice(makePresentation({ status: 'closed' }))).toBe(false);
  });
});

describe('seedPresentationNotices', () => {
  it('keeps only presentations that need a notice', () => {
    const open = makePresentation({ id: 'open' });
    const submitted = makePresentation({ id: 'submitted', latest_round_submitted: true });
    const closed = makePresentation({ id: 'closed', status: 'closed' });

    expect(seedPresentationNotices([open, submitted, closed])).toEqual([open]);
  });
});

describe('upsertPresentationNotice', () => {
  it('adds a new presentation that needs a notice', () => {
    const notices = upsertPresentationNotice([], makePresentation());
    expect(notices).toHaveLength(1);
  });

  it('replaces an existing entry for the same id rather than duplicating it', () => {
    const first = makePresentation({ title: 'Original title' });
    const updated = makePresentation({ title: 'Updated title' });

    const notices = upsertPresentationNotice([first], updated);

    expect(notices).toHaveLength(1);
    expect(notices[0].title).toBe('Updated title');
  });

  it('drops the notice once the latest round is submitted', () => {
    const open = makePresentation();
    const submitted = makePresentation({ latest_round_submitted: true });

    const notices = upsertPresentationNotice([open], submitted);

    expect(notices).toEqual([]);
  });

  it('drops the notice once the presentation is closed', () => {
    const open = makePresentation();
    const closed = makePresentation({ status: 'closed' });

    const notices = upsertPresentationNotice([open], closed);

    expect(notices).toEqual([]);
  });

  it('leaves other presentations untouched', () => {
    const other = makePresentation({ id: 'other' });
    const updated = makePresentation({ id: 'p1' });

    const notices = upsertPresentationNotice([other], updated);

    expect(notices).toHaveLength(2);
    expect(notices.map((n) => n.id).sort()).toEqual(['other', 'p1']);
  });
});
