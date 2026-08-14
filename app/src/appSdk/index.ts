// The module a mounted view's `@victorarias/attn-app` import resolves to.
//
// A view is bundled with the SDK marked external, so its import survives into
// the artifact and is resolved in the browser by index.html's import map. That
// map points here — at a chunk of attn's own build — which is what makes an
// app's React the same instance as attn's rather than a second copy shipped in
// the view.
//
// It exists as an entry file, and not as a mapping straight at the package,
// because the import map needs a URL that does not change between builds. See
// vite.config.ts's appSdkChunks plugin and app/src/appSdk/importMap.test.mjs.
export * from "@victorarias/attn-app"
