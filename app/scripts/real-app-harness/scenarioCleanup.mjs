export async function cleanupSessionViaAppClose(client, observer, sessionId, timeoutMs = 30_000) {
  if (!sessionId) {
    return;
  }

  try {
    await client.request('close_session', { sessionId }, { timeoutMs: Math.min(20_000, timeoutMs) });
  } catch {
    // Fall through to observer-based verification/fallback.
  }

  try {
    await observer.waitFor(
      () => !observer.getSession(sessionId) && !observer.getWorkspace(sessionId),
      `session ${sessionId} to disappear after close_session`,
      timeoutMs,
    );
    return;
  } catch {
    // Fall back to direct daemon cleanup if app-close semantics did not complete.
  }

  observer.send({ cmd: 'kill_session', id: sessionId });
  await observer.waitFor(() => {
    const session = observer.getSession(sessionId);
    return !session || session.state !== 'working' ? true : null;
  }, `cleanup kill_session ${sessionId}`, 20_000).catch(() => {});
  observer.unregisterSession(sessionId);
  await observer.waitFor(() => !observer.getSession(sessionId), `cleanup unregister ${sessionId}`, 20_000).catch(() => {});
}
