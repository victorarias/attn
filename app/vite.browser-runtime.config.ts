import { defineConfig } from "vite";
import { resolve } from "path";

export default defineConfig({
  publicDir: false,
  define: {
    "process.env.NODE_ENV": JSON.stringify("production"),
  },
  build: {
    emptyOutDir: false,
    lib: {
      entry: resolve(__dirname, "src/browser/runtime.ts"),
      formats: ["iife"],
      name: "AttnBrowserRuntime",
      fileName: () => "browser-runtime.js",
    },
    outDir: resolve(__dirname, "src-tauri/generated"),
    minify: true,
    rollupOptions: {
      output: {
        inlineDynamicImports: true,
      },
    },
  },
});
