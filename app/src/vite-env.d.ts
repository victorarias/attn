/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_DAEMON_HOST?: string;
  readonly VITE_DAEMON_PORT?: string;
  readonly VITE_DAEMON_WS_PATH?: string;
  readonly VITE_DAEMON_WS_PROTOCOL?: string;
  readonly VITE_DAEMON_WS_URL?: string;
  readonly VITE_INSTALL_CHANNEL?: string;
  readonly VITE_ATTN_BUILD_VERSION?: string;
  readonly VITE_ATTN_SOURCE_FINGERPRINT?: string;
  readonly VITE_ATTN_GIT_COMMIT?: string;
  readonly VITE_ATTN_BUILD_TIME?: string;
  // Only for a bundle running outside Tauri, which cannot read the profile's
  // client-token file. vite.config.ts fills it in from that file.
  readonly VITE_CLIENT_TOKEN?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

// The snapshot wire format this bundle decodes, defined by vite.config.ts.
declare const __ATTN_SNAPSHOT_FORMAT__: string;
