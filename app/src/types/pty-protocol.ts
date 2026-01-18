// PTY Protocol Types
// Shared between pty-server (Node.js) and frontend (React/Tauri)

// Command constants
export const PTY_COMMANDS = {
  SPAWN: 'spawn',
  WRITE: 'write',
  RESIZE: 'resize',
  KILL: 'kill',
} as const;

// Event constants
export const PTY_EVENTS = {
  SPAWNED: 'spawned',
  DATA: 'data',
  EXIT: 'exit',
  ERROR: 'error',
  TRANSCRIPT: 'transcript',
} as const;

// Command types (client → server)
export interface PtySpawnCommand {
  cmd: typeof PTY_COMMANDS.SPAWN;
  id: string;
  cwd?: string;
  cols?: number;
  rows?: number;
}

export interface PtyWriteCommand {
  cmd: typeof PTY_COMMANDS.WRITE;
  id: string;
  data: string;
}

export interface PtyResizeCommand {
  cmd: typeof PTY_COMMANDS.RESIZE;
  id: string;
  cols: number;
  rows: number;
}

export interface PtyKillCommand {
  cmd: typeof PTY_COMMANDS.KILL;
  id: string;
}

export type PtyCommand =
  | PtySpawnCommand
  | PtyWriteCommand
  | PtyResizeCommand
  | PtyKillCommand;

// Event types (server → client)
export interface PtySpawnedEvent {
  event: typeof PTY_EVENTS.SPAWNED;
  id: string;
  pid: number;
}

export interface PtyDataEvent {
  event: typeof PTY_EVENTS.DATA;
  id: string;
  data: string; // base64 encoded
}

export interface PtyExitEvent {
  event: typeof PTY_EVENTS.EXIT;
  id: string;
  code: number;
}

export interface PtyErrorEvent {
  event: typeof PTY_EVENTS.ERROR;
  cmd: string;
  error: string;
}

export interface PtyTranscriptEvent {
  event: typeof PTY_EVENTS.TRANSCRIPT;
  id: string;
  matched: boolean;
}

export type PtyEvent =
  | PtySpawnedEvent
  | PtyDataEvent
  | PtyExitEvent
  | PtyErrorEvent
  | PtyTranscriptEvent;

// Type guards for events
export function isPtySpawnedEvent(event: PtyEvent): event is PtySpawnedEvent {
  return event.event === PTY_EVENTS.SPAWNED;
}

export function isPtyDataEvent(event: PtyEvent): event is PtyDataEvent {
  return event.event === PTY_EVENTS.DATA;
}

export function isPtyExitEvent(event: PtyEvent): event is PtyExitEvent {
  return event.event === PTY_EVENTS.EXIT;
}

export function isPtyErrorEvent(event: PtyEvent): event is PtyErrorEvent {
  return event.event === PTY_EVENTS.ERROR;
}

export function isPtyTranscriptEvent(event: PtyEvent): event is PtyTranscriptEvent {
  return event.event === PTY_EVENTS.TRANSCRIPT;
}
