export type UISessionState =
  | 'launching'
  | 'working'
  | 'waiting_input'
  | 'idle'
  | 'pending_approval'
  | 'scheduled'
  | 'recoverable'
  | 'unknown';

// Normalize daemon state to UI state
export function normalizeSessionState(state: string): UISessionState {
  switch (state) {
    case 'launching':
    case 'working':
    case 'waiting_input':
    case 'idle':
    case 'pending_approval':
    case 'scheduled':
    case 'recoverable':
    case 'unknown':
      return state;
    default:
      return 'unknown';
  }
}

// `scheduled` and `recoverable` are intentionally excluded: a session parked
// on a /loop or cron will auto-resume on its own, and a recoverable session
// auto-revives on open; neither needs steering, so both stay quiet (no attention
// badge, no drawer entry).
export function isAttentionSessionState(state: UISessionState): boolean {
  return state === 'waiting_input' || state === 'pending_approval' || state === 'unknown';
}
