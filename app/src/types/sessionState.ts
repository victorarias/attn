// app/src/types/sessionState.ts
// Re-export SessionState from generated types for backward compatibility
export { SessionState } from './generated';
import { SessionState } from './generated';

// Type aliases for backward compatibility
export type DaemonSessionState = SessionState;
export type UISessionState = 'working' | 'waiting_input' | 'idle' | 'pending_approval';

// Normalize daemon state to UI state
// Daemon always sends 'waiting_input' for sessions (from SessionState enum)
// The 'waiting' string is only used for PR state (PRStateWaiting), not sessions
export function normalizeSessionState(state: string): UISessionState {
  if (state === 'waiting_input') {
    return 'waiting_input';
  }
  if (state === 'working') {
    return 'working';
  }
  if (state === 'pending_approval') {
    return 'pending_approval';
  }
  return 'idle';
}
