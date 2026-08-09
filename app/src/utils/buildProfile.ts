/**
 * Compile-time profile baked into this bundle ("" = production), mirroring the
 * Rust shell's ATTN_BUILD_PROFILE and the daemon's ATTN_PROFILE. A daemon
 * reporting a different profile is refused rather than served the wrong data dir.
 */
export const BUILD_PROFILE: string = (import.meta.env.VITE_ATTN_BUILD_PROFILE ?? '').trim();

export const BUILD_PROFILE_LABEL: string = BUILD_PROFILE === '' ? 'default' : BUILD_PROFILE;

/** Whether the daemon's reported profile matches this build; empty means "default". */
export function daemonProfileMatches(reportedProfile: string | null | undefined): boolean {
  const reported = (reportedProfile ?? '').trim() || 'default';
  return reported === BUILD_PROFILE_LABEL;
}

/** ws://127.0.0.1:29849/ws → http://127.0.0.1:29849/health */
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
 * Fetches /health for the profile-identity subset. Throws on network/HTTP
 * errors so the caller decides whether no answer is a mismatch or transient.
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

/** Mismatch banner text; the caller shows it non-dismissably and stops reconnecting. */
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
