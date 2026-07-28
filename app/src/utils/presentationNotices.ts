import type { Presentation } from '../types/generated';

// A presentation is worth a notice (surfaced as a chip in its triggering
// session's pane header) while its latest round is open for review (status
// "open") and hasn't been submitted yet.
export function presentationNeedsNotice(presentation: Presentation): boolean {
  return presentation.status === 'open' && !presentation.latest_round_submitted;
}

// Pure reducer for the presentation-notice list, shared by the initial
// getPresentations() seed and the presentation_added/updated broadcasts.
// Keeps at most one entry per presentation id; a presentation that stops
// needing a notice (submitted or closed) is dropped.
export function upsertPresentationNotice(notices: Presentation[], updated: Presentation): Presentation[] {
  const withoutExisting = notices.filter((n) => n.id !== updated.id);
  return presentationNeedsNotice(updated) ? [...withoutExisting, updated] : withoutExisting;
}

export function seedPresentationNotices(all: Presentation[]): Presentation[] {
  return all.filter(presentationNeedsNotice);
}

// A session can trigger more than one presentation over time; the pane
// header chip only ever shows the newest one that still needs review.
export function latestPresentationBySessionId(notices: Presentation[]): Map<string, Presentation> {
  const bySessionId = new Map<string, Presentation>();
  for (const p of notices) {
    const existing = bySessionId.get(p.session_id);
    if (!existing || p.created_at > existing.created_at) {
      bySessionId.set(p.session_id, p);
    }
  }
  return bySessionId;
}
