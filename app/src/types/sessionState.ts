// app/src/types/sessionState.ts
// Re-export SessionState from generated types for backward compatibility
export { SessionState } from './generated';
import { SessionState } from './generated';

// Type aliases for backward compatibility
export type DaemonSessionState = SessionState;
export type UISessionState =
  | 'launching'
  | 'working'
  | 'waiting_input'
  | 'idle'
  | 'pending_approval'
  | 'unknown';

// Normalize daemon state to UI state
export function normalizeSessionState(state: string): UISessionState {
  switch (state) {
    case 'launching':
    case 'working':
    case 'waiting_input':
    case 'idle':
    case 'pending_approval':
    case 'unknown':
      return state;
    default:
      return 'unknown';
  }
}

export function isAttentionSessionState(state: UISessionState): boolean {
  return state === 'waiting_input' || state === 'pending_approval' || state === 'unknown';
}
