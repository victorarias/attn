/// <reference types="vitest" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "path";

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

// https://vite.dev/config/
export default defineConfig(async () => ({
  plugins: [react(), appSdkDevChunks],
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
