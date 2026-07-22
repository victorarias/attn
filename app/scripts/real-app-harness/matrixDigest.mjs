// Pure helpers shared between run-serial-matrix.mjs's digest/--failed-only
// support and its unit tests. Kept out of run-serial-matrix.mjs itself because
// that file's `main()` runs unconditionally at import time (no
// `import.meta.url === process.argv[1]` guard), so importing it from a test
// would launch the actual matrix.

// Given a parsed last-matrix.json, return the ids of scenarios that failed
// last time.
export function selectFailedScenarios(lastMatrixJson) {
  return (lastMatrixJson?.results || [])
    .filter((result) => result.code !== 0)
    .map((result) => result.id);
}

export function formatResultTable(results) {
  const idWidth = results.reduce((width, result) => Math.max(width, result.id.length), 2);
  return results
    .map((result) => {
      const status = result.code === 0 ? 'PASS' : 'FAIL';
      const seconds = (result.durationMs / 1000).toFixed(1);
      return `${status.padEnd(4)}  ${result.id.padEnd(idWidth)}  ${seconds}s`;
    })
    .join('\n');
}
