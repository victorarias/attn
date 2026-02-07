import { defineConfig } from '@playwright/test';

// Test daemon runs on port 19849 to avoid conflicts with production daemon (9849)
const TEST_DAEMON_PORT = '19849';
// Test Vite server runs on a different port to allow reusing existing dev server
const TEST_VITE_PORT = '1421';

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
