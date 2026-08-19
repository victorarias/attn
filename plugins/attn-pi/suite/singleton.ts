// A process-wide slot for state that must outlive one evaluation of the suite
// module.
//
// pi's `/reload` clears the extension module cache (0.83.0,
// `ResourceLoader.reload()` -> `clearExtensionCache()`), as does resuming a
// session from another directory, and its loader runs jiti with
// `moduleCache: false`. The suite entrypoint is therefore EVALUATED AGAIN in
// the same process: module scope is not the process-wide slot it looks like.
// Anything holding an OS resource — the relay socket — has to be keyed on the
// process, or the re-evaluation dials a second one and leaves the first open
// with nobody reading it. pi keeps its own theme instance across module
// loaders exactly this way (`Symbol.for("...:theme")`).
export function processSingleton<T>(key: string, build: () => T): T {
  const slot = Symbol.for(key);
  const host = globalThis as unknown as Record<symbol, unknown>;
  const existing = host[slot];
  if (existing !== undefined) return existing as T;
  const created = build();
  host[slot] = created;
  return created;
}
