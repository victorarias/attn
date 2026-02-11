/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_INSTALL_CHANNEL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
