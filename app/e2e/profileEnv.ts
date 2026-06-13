import { execFileSync } from 'child_process';
import * as fs from 'fs';
import * as path from 'path';
import { fileURLToPath } from 'url';

const E2E_DIR = path.dirname(fileURLToPath(import.meta.url));

// Resolve the attn binary the e2e harness drives. ATTN_E2E_BIN wins; otherwise
// the repo-root ./attn built by `go build -o ./attn ./cmd/attn`.
export function resolveAttnBinaryPath(): string {
  const candidates = [
    process.env.ATTN_E2E_BIN,
    path.resolve(E2E_DIR, '../../attn'),
  ].filter((candidate): candidate is string => Boolean(candidate));

  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) {
      return candidate;
    }
  }

  throw new Error(
    `attn binary not found. Tried: ${candidates.join(', ')}. ` +
      `Set ATTN_E2E_BIN or build binary with 'go build -o ./attn ./cmd/attn'.`
  );
}

export interface E2EPorts {
  profile: string;
  daemonPort: string;
  vitePort: string;
}

// e2ePorts derives the throwaway-daemon + Vite ports for the active ATTN_PROFILE
// from the single profile authority (`attn profile resolve`).
//
// The default profile keeps the historical fixed ports (19849 / 1421) so
// existing local and CI runs are byte-for-byte unchanged and need no attn
// binary at config-load time. A named profile gets disjoint per-profile bands
// ([30000,30999] daemon / [31000,31999] Vite) so multiple agents can run e2e
// concurrently without colliding — and because the per-run teardown kill is
// scoped to ATTN_WS_PORT=<daemonPort>, one agent never kills a peer's daemon.
export function e2ePorts(): E2EPorts {
  const profile = (process.env.ATTN_PROFILE ?? '').trim();
  if (profile === '') {
    return { profile: '', daemonPort: '19849', vitePort: '1421' };
  }
  const attn = resolveAttnBinaryPath();
  const out = execFileSync(attn, ['profile', 'resolve', '--json'], {
    encoding: 'utf8',
  });
  const resolved = JSON.parse(out) as {
    profile: string;
    e2eDaemonPort: string;
    e2eVitePort: string;
  };
  return {
    profile: resolved.profile,
    daemonPort: resolved.e2eDaemonPort,
    vitePort: resolved.e2eVitePort,
  };
}
