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

// The app SDK's chunks. A view is bundled with `@victorarias/attn-app` external,
// so its import is resolved in the browser by index.html's import map — which
// needs a URL that is the same in every build. These are entries with hashing
// turned off for exactly that reason, and the map's targets are pinned against
// this list by src/appSdk/importMap.test.mjs.
const APP_SDK_CHUNKS: Record<string, string> = {
  "attn-app-sdk": "/src/appSdk/index.ts",
  "attn-app-sdk-jsx": "/src/appSdk/jsxRuntime.ts",
  "attn-app-sdk-jsx-dev": "/src/appSdk/jsxDevRuntime.ts",
};

// In `vite dev` nothing is built, so the same URLs have to exist anyway or a
// docked view fails to link in development only. Serving a one-line re-export of
// the source module keeps the resolution identical without a second import map.
const appSdkDevChunks = {
  name: "attn-app-sdk-dev-chunks",
  apply: "serve" as const,
  configureServer(server: { middlewares: { use: (fn: any) => void } }) {
    server.middlewares.use((req: any, res: any, next: () => void) => {
      const name = (req.url ?? "").split("?")[0].replace(/^\//, "").replace(/\.js$/, "");
      const source = APP_SDK_CHUNKS[name];
      if (!source) {
        next();
        return;
      }
      res.setHeader("Content-Type", "text/javascript; charset=utf-8");
      res.end(`export * from ${JSON.stringify(source)}\n`);
    });
  },
};

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
  plugins: [react(), appSdkDevChunks],
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
        ...Object.fromEntries(
          Object.entries(APP_SDK_CHUNKS).map(([name, source]) => [
            name,
            resolve(__dirname, source.replace(/^\//, "")),
          ]),
        ),
      },
      // Vite drops an entry chunk's own exports by default, because its entries
      // are HTML pages that have none. These entries exist *for* their exports —
      // the import map resolves a view's bare specifier to one — so the
      // signature has to survive. "allow-extension" keeps it while still letting
      // rollup fold shared modules (React among them) into these chunks, which
      // is what keeps one React instance in the built app.
      preserveEntrySignatures: "allow-extension",
      output: {
        entryFileNames: (chunk: { name: string }) =>
          chunk.name in APP_SDK_CHUNKS ? "[name].js" : "assets/[name]-[hash].js",
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
