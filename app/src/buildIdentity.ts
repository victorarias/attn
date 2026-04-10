function readBuildEnv(value: string | undefined): string | null {
  if (typeof value !== 'string') {
    return null;
  }
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : null;
}

export interface AppBuildIdentity {
  version: string | null;
  sourceFingerprint: string | null;
  gitCommit: string | null;
  buildTime: string | null;
}

export const APP_BUILD_IDENTITY: AppBuildIdentity = {
  version: readBuildEnv(import.meta.env.VITE_ATTN_BUILD_VERSION),
  sourceFingerprint: readBuildEnv(import.meta.env.VITE_ATTN_SOURCE_FINGERPRINT),
  gitCommit: readBuildEnv(import.meta.env.VITE_ATTN_GIT_COMMIT),
  buildTime: readBuildEnv(import.meta.env.VITE_ATTN_BUILD_TIME),
};
