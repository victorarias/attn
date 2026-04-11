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
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
