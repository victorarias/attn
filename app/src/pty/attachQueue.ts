// Per-key async serialization for PTY attach requests.
//
// Attaches to the same session must not overlap: the pending-action key, the
// attach context, and the queued-output buffer in the daemon socket are all
// per-session singletons, so a second in-flight attach corrupts the first
// (its promise never settles and its result/context pair up wrong). Session
// creation alone issues two back-to-back attaches (fresh_spawn, then the pane
// mount's same_app_remount), so overlap is routine. enqueuePerKey runs tasks
// for the same key strictly one after another — a failed or timed-out task
// (all attach paths are time-bounded) never blocks the next one.
export function enqueuePerKey<T>(
  chains: Map<string, Promise<unknown>>,
  key: string,
  task: () => Promise<T>,
): Promise<T> {
  const prior = chains.get(key) ?? Promise.resolve();
  const next = prior.catch(() => {}).then(task);
  chains.set(key, next);
  void next.catch(() => {}).finally(() => {
    if (chains.get(key) === next) {
      chains.delete(key);
    }
  });
  return next;
}
