import { defineConfig } from '@playwright/test';
import { E2E_CLIENT_TOKEN, e2ePorts } from './e2e/profileEnv';

// Ports for the active ATTN_PROFILE. Default profile keeps the historical
// 19849 (daemon) / 1421 (Vite); a named profile gets disjoint per-profile bands
// so multiple agents can run e2e in parallel (see e2e/profileEnv.ts).
const { daemonPort: TEST_DAEMON_PORT, vitePort: TEST_VITE_PORT } = e2ePorts();

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false, // Run tests serially due to shared daemon
  retries: 0,
  workers: 1,
  reporter: 'list',
  // One floor for every assertion, because a passing assertion returns the
  // moment its condition holds — the budget is only ever spent on the failure
  // path, so a tight one buys nothing and a generous one costs nothing. The
  // number is a tripwire: the slowest whole test here runs 2.9s locally and CI
  // runs 3-4x slower, so no healthy assertion reaches 15s, and it stays under
  // the 30s test timeout so a blown assertion reports itself rather than
  // surfacing as a timed-out test. `retries: 0` means a budget a healthy CI run
  // can touch is a red build, never a silent retry.
  expect: { timeout: 15_000 },
  use: {
    baseURL: `http://localhost:${TEST_VITE_PORT}`,
    trace: 'on-first-retry',
  },
  webServer: {
    // The wasm module is downloaded, not committed, so a fresh checkout — CI
    // included — has to fetch it before vite can resolve the import.
    command: `pnpm run verify:ghostty-vt && npx vite --port ${TEST_VITE_PORT}`,
    url: `http://localhost:${TEST_VITE_PORT}`,
    reuseExistingServer: false, // Always start fresh to ensure correct env vars
    timeout: 30000,
    env: {
      VITE_DAEMON_PORT: TEST_DAEMON_PORT,
      VITE_CLIENT_TOKEN: E2E_CLIENT_TOKEN,
      VITE_MOCK_PTY: process.env.VITE_MOCK_PTY ?? '1',
      VITE_FORCE_REAL_PTY: process.env.VITE_FORCE_REAL_PTY ?? '0',
    },
  },
});
