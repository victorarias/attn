// app/src/types/sessionState.ts

// Session state - consistent across daemon and UI
export type SessionState = 'working' | 'waiting' | 'idle';

// Daemon sends 'waiting', UI displays as 'waiting_input'
export type DaemonSessionState = 'working' | 'waiting' | 'idle';
export type UISessionState = 'working' | 'waiting_input' | 'idle';

// Normalize daemon state to UI state
// Daemon uses 'waiting', UI uses 'waiting_input'
export function normalizeSessionState(state: string): UISessionState {
  if (state === 'waiting' || state === 'waiting_input') {
    return 'waiting_input';
  }
  if (state === 'working') {
    return 'working';
  }
  return 'idle';
}
