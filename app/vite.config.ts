/// <reference types="vitest" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { execFileSync } from "child_process";
import { readFileSync } from "fs";
import { homedir } from "os";
import { resolve } from "path";

// The terminal-snapshot wire format this bundle decodes. Computed here, from
// the same script the Makefile feeds into buildinfo.SnapshotFormat, so a
// bundle's worker and app agree by construction rather than by a build script
// remembering to export an env var. A failure to derive it fails the build:
// guessing would silently cost every session its restore.
// See docs/plans/2026-08-16-snapshot-format-skew.md.
const snapshotFormat = execFileSync(
  "bash",
  [resolve(__dirname, "../scripts/snapshot-format.sh")],
  { encoding: "utf8" },
).trim();

// @ts-expect-error process is a nodejs global
const host = process.env.TAURI_DEV_HOST;

// A bundle served outside Tauri — `dev:vite` — has no way to read the profile's
// client-token file, and the daemon refuses a client_hello without it. Read it
// here, where node can, so the browser dev loop needs no setup.
//
// Serve only. A built bundle must never carry this: the same bundle ships to
// every profile and to whoever installs a release, and the token is per-profile
// and secret. Inside the app it is unused anyway — Tauri reads the file itself.
// An explicit VITE_CLIENT_TOKEN (Playwright sets one) wins: this define
// replaces the expression outright, so it has to reproduce what vite's own
// VITE_-prefix injection would have put there.
function clientTokenFromProfile(): string {
  // @ts-expect-error process is a nodejs global
  const env = process.env as Record<string, string | undefined>;
  const explicit = (env.VITE_CLIENT_TOKEN ?? env.ATTN_CLIENT_TOKEN ?? "").trim();
  if (explicit) return explicit;
  const profile = (env.ATTN_PROFILE ?? "").trim();
  const dataDir =
    (env.ATTN_DATA_DIR ?? "").trim() ||
    resolve(homedir(), profile ? `.attn-${profile}` : ".attn");
  try {
    return readFileSync(resolve(dataDir, "client-token"), "utf8").trim();
  } catch {
    return "";
  }
}

// https://vite.dev/config/
export default defineConfig(async ({ command }) => ({
  plugins: [react()],
  define: {
    __ATTN_SNAPSHOT_FORMAT__: JSON.stringify(snapshotFormat),
    ...(command === "serve"
      ? { "import.meta.env.VITE_CLIENT_TOKEN": JSON.stringify(clientTokenFromProfile()) }
      : {}),
  },
  // Multi-page app configuration for test harness
  build: {
    rollupOptions: {
      input: {
        main: resolve(__dirname, "index.html"),
        "test-harness": resolve(__dirname, "test-harness/index.html"),
      },
    },
  },

  // Vite options tailored for Tauri development and only applied in `tauri dev` or `tauri build`
  //
  // 1. prevent Vite from obscuring rust errors
  clearScreen: false,
  // 2. tauri expects a fixed port, fail if that port is not available
  server: {
    port: 1420,
    strictPort: true,
    host: host || false,
    hmr: host
      ? {
          protocol: "ws",
          host,
          port: 1421,
        }
      : undefined,
    watch: {
      // 3. tell Vite to ignore watching `src-tauri`
      ignored: ["**/src-tauri/**"],
    },
  },

  // Vitest configuration
  test: {
    globals: true,
    environment: "happy-dom",
    setupFiles: ["./src/test/setup.ts"],
    include: [
      "src/**/*.test.{ts,tsx}",
      // Plain-JS tests, for guards that must read source files off disk: the app
      // tsconfig has no node types, and vitest stubs CSS imports to empty, so a
      // stylesheet assertion cannot live in a .ts test.
      "src/**/*.test.mjs",
      "scripts/real-app-harness/**/*.test.{ts,mjs}",
    ],
    environmentMatchGlobs: [
      ["scripts/real-app-harness/**/*.test.{ts,mjs}", "node"],
    ],
  },
}));
