import { defineConfig } from '@playwright/test';
import { e2ePorts } from './e2e/profileEnv';

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
  use: {
    baseURL: `http://localhost:${TEST_VITE_PORT}`,
    trace: 'on-first-retry',
  },
  webServer: {
    command: `npx vite --port ${TEST_VITE_PORT}`,
    url: `http://localhost:${TEST_VITE_PORT}`,
    reuseExistingServer: false, // Always start fresh to ensure correct env vars
    timeout: 30000,
    env: {
      VITE_DAEMON_PORT: TEST_DAEMON_PORT,
      VITE_MOCK_PTY: process.env.VITE_MOCK_PTY ?? '1',
      VITE_FORCE_REAL_PTY: process.env.VITE_FORCE_REAL_PTY ?? '0',
    },
  },
});
