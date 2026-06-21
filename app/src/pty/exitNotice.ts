// Human-facing terminal notice for a PTY process exit.
//
// The attn daemon tears sessions down with SIGTERM (exit code 143) on normal
// completion, stop, and pane-close — see `terminateSession`/`unregisterSession`
// in `internal/daemon`. Rendering that as a bare "[Process exited with code 143]"
// reads like a crash, even though the task finished and its work persisted.
//
// So we present *expected* terminations — a clean exit, or a teardown signal
// (SIGTERM/SIGINT/SIGHUP) — as a calm "Session ended", and reserve the raw
// exit-code notice for genuinely abnormal exits: a non-zero application exit
// code, an out-of-memory/force kill (SIGKILL → 137), or a crash
// (SIGSEGV → 139, SIGABRT → 134).

// 128 + N is the conventional shell encoding for "terminated by signal N".
const SIGNAL_BY_CODE: Record<number, string> = {
  129: 'SIGHUP',
  130: 'SIGINT',
  143: 'SIGTERM',
};

// Signals that represent an expected, orderly shutdown rather than a fault.
const GRACEFUL_SIGNALS = new Set(['SIGHUP', 'SIGINT', 'SIGTERM']);

function normalizeSignal(code: number, signal?: string): string | undefined {
  const trimmed = signal?.trim();
  if (trimmed) {
    const upper = trimmed.toUpperCase();
    return upper.startsWith('SIG') ? upper : `SIG${upper}`;
  }
  return SIGNAL_BY_CODE[code];
}

// Returns the line written into the terminal when a session's process exits.
export function formatExitNotice(code: number, signal?: string): string {
  if (code === 0 && !signal?.trim()) {
    return '[Session ended]';
  }
  const sig = normalizeSignal(code, signal);
  if (sig && GRACEFUL_SIGNALS.has(sig)) {
    return '[Session ended]';
  }
  return `[Process exited with code ${code}]`;
}
