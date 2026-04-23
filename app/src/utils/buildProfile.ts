/**
 * Compile-time profile baked into this frontend bundle. Mirrors the
 * ATTN_BUILD_PROFILE constant in the Rust shell and the ATTN_PROFILE
 * env var the daemon sees at runtime.
 *
 * Default ("") means the production build. "dev" means the sibling
 * dev install. The frontend uses this to refuse to operate when the
 * daemon it connects to reports a different profile — a mismatch
 * means either a misconfigured environment or a malicious daemon
 * replacement, and we want to fail loudly rather than silently
 * operate on the wrong data dir.
 */
export const BUILD_PROFILE: string = (import.meta.env.VITE_ATTN_BUILD_PROFILE ?? '').trim();

export const BUILD_PROFILE_LABEL: string = BUILD_PROFILE === '' ? 'default' : BUILD_PROFILE;

/**
 * Checks whether a profile reported by the daemon matches what this
 * build expects. Treats a missing/empty reported profile as "default"
 * for forward compatibility with pre-profile daemons.
 */
export function daemonProfileMatches(reportedProfile: string | null | undefined): boolean {
  const reported = (reportedProfile ?? '').trim() || 'default';
  return reported === BUILD_PROFILE_LABEL;
}

/**
 * Derives the /health URL from a WebSocket URL. Example:
 *   ws://127.0.0.1:29849/ws  →  http://127.0.0.1:29849/health
 */
export function healthURLFromWS(wsUrl: string): string {
  try {
    const u = new URL(wsUrl);
    u.protocol = u.protocol === 'wss:' ? 'https:' : 'http:';
    u.pathname = '/health';
    u.search = '';
    u.hash = '';
    return u.toString();
  } catch {
    return '';
  }
}

export interface DaemonHealthProfile {
  profile?: string;
  data_dir?: string;
  socket_path?: string;
  port?: string;
}

/**
 * Fetches /health and returns the profile-identity subset. Throws on
 * network/HTTP errors so the caller can decide whether to treat the
 * absence of a response as mismatch or transient.
 */
export async function fetchDaemonHealthProfile(wsUrl: string, signal?: AbortSignal): Promise<DaemonHealthProfile> {
  const url = healthURLFromWS(wsUrl);
  if (!url) throw new Error('cannot derive health URL from ws URL');
  const resp = await fetch(url, { signal, cache: 'no-store' });
  if (!resp.ok) throw new Error(`/health returned ${resp.status}`);
  const body = await resp.json();
  return {
    profile: typeof body?.profile === 'string' ? body.profile : undefined,
    data_dir: typeof body?.data_dir === 'string' ? body.data_dir : undefined,
    socket_path: typeof body?.socket_path === 'string' ? body.socket_path : undefined,
    port: typeof body?.port === 'string' ? body.port : undefined,
  };
}

/**
 * Builds a user-facing error message for a profile mismatch. Matches the
 * "refuse to operate" behavior agreed in design — this is shown as a
 * non-dismissable banner, and the caller should not attempt to
 * reconnect.
 */
export function profileMismatchMessage(reported: string | null | undefined): string {
  const reportedLabel = (reported ?? '').trim() || 'default';
  return (
    `Profile mismatch: this app was built for profile "${BUILD_PROFILE_LABEL}" ` +
    `but the daemon reports profile "${reportedLabel}". ` +
    `Refusing to operate on a mismatched daemon. ` +
    `Quit this app and launch the matching one (prod = attn.app, dev = attn-dev.app), ` +
    `or restart the daemon under the correct ATTN_PROFILE.`
  );
}
