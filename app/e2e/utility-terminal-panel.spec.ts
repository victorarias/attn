import { test, expect } from './fixtures';

// Helper to inject a session into the local UI store
async function injectLocalSession(
  page: import('@playwright/test').Page,
  session: { id: string; label: string; state: string; cwd?: string }
) {
  await page.evaluate((s) => {
    window.__TEST_INJECT_SESSION?.({
      id: s.id,
      label: s.label,
      state: s.state as 'working' | 'waiting_input' | 'idle',
      cwd: s.cwd || '/tmp/test',
    });
  }, session);
}

// Helper to create a session in both local store AND daemon
async function createSession(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (s: { id: string; label: string; state: string; directory?: string }) => Promise<void> },
  session: { id: string; label: string; state: string; cwd?: string }
) {
  const cwd = session.cwd || '/tmp/test';
  await injectLocalSession(page, { ...session, cwd });
  await daemon.injectSession({
    id: session.id,
    label: session.label,
    state: session.state,
    directory: cwd,
  });
}

// Helper to add a mock terminal to a session's panel
async function addMockTerminal(
  page: import('@playwright/test').Page,
  sessionId: string,
  terminalId: string,
  title: string
) {
  await page.evaluate(
    ({ sessionId, terminalId, title }) => {
      window.__TEST_ADD_UTILITY_TERMINAL?.(sessionId, terminalId, title);
    },
    { sessionId, terminalId, title }
  );
}

// Helper to open terminal panel
async function openTerminalPanel(page: import('@playwright/test').Page, sessionId: string) {
  await page.evaluate((id) => {
    window.__TEST_OPEN_TERMINAL_PANEL?.(id);
  }, sessionId);
}

// Helper to dispatch keyboard shortcut with exact key value
// Playwright's keyboard.press doesn't send shifted characters correctly
async function dispatchShortcut(
  page: import('@playwright/test').Page,
  key: string,
  modifiers: { meta?: boolean; shift?: boolean; ctrl?: boolean; alt?: boolean } = {}
) {
  await page.evaluate(
    ({ key, modifiers }) => {
      const event = new KeyboardEvent('keydown', {
        key,
        code: '', // not needed for our matching
        metaKey: modifiers.meta || false,
        shiftKey: modifiers.shift || false,
        ctrlKey: modifiers.ctrl || false,
        altKey: modifiers.alt || false,
        bubbles: true,
        cancelable: true,
      });
      document.dispatchEvent(event);
    },
    { key, modifiers }
  );
}

test.describe('Utility Terminal Panel', () => {
  test.describe('Panel Visibility', () => {
    test('panel is hidden when closed', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, { id: 's1', label: 'Test', state: 'working' });
      await page.locator('[data-testid="session-s1"]').click();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      // Panel should not be visible initially
      await expect(page.locator('.utility-terminal-panel')).not.toBeVisible();
    });

    test('panel is visible when opened with terminal', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, { id: 's1', label: 'Test', state: 'working' });
      await page.locator('[data-testid="session-s1"]').click();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      // Add a mock terminal to open the panel
      await addMockTerminal(page, 's1', 'term-1', 'Shell 1');

      // Panel should be visible
      await expect(page.locator('.utility-terminal-panel')).toBeVisible();
      await expect(page.locator('.terminal-tab')).toHaveCount(1);
    });
  });

  test.describe('Keyboard Shortcuts', () => {
    test('Shift+` collapses terminal panel', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, { id: 's1', label: 'Test', state: 'working' });
      await page.locator('[data-testid="session-s1"]').click();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      // Add a mock terminal
      await addMockTerminal(page, 's1', 'term-1', 'Shell 1');
      await expect(page.locator('.utility-terminal-panel')).toBeVisible();

      // Shift+` produces ~ - dispatch with exact key value
      await dispatchShortcut(page, '~', { shift: true });
      await expect(page.locator('.utility-terminal-panel')).not.toBeVisible({ timeout: 2000 });
    });

    test('⌘⇧W closes current terminal tab', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, { id: 's1', label: 'Test', state: 'working' });
      await page.locator('[data-testid="session-s1"]').click();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      // Add two mock terminals
      await addMockTerminal(page, 's1', 'term-1', 'Shell 1');
      await addMockTerminal(page, 's1', 'term-2', 'Shell 2');
      await expect(page.locator('.terminal-tab')).toHaveCount(2);

      // ⌘⇧W should close the current tab
      await page.keyboard.press('Meta+Shift+w');
      await expect(page.locator('.terminal-tab')).toHaveCount(1, { timeout: 2000 });
    });

    test('closing last tab collapses the panel', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, { id: 's1', label: 'Test', state: 'working' });
      await page.locator('[data-testid="session-s1"]').click();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      // Add one mock terminal
      await addMockTerminal(page, 's1', 'term-1', 'Shell 1');
      await expect(page.locator('.utility-terminal-panel')).toBeVisible();
      await expect(page.locator('.terminal-tab')).toHaveCount(1);

      // Close the only tab
      await page.keyboard.press('Meta+Shift+w');

      // Panel should be closed
      await expect(page.locator('.utility-terminal-panel')).not.toBeVisible({ timeout: 2000 });
    });
  });

  test.describe('Session Isolation', () => {
    test('terminal panel is separate per session', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Create two sessions
      await createSession(page, daemon, { id: 's1', label: 'Session 1', state: 'working' });
      await createSession(page, daemon, { id: 's2', label: 'Session 2', state: 'working' });
      await expect(page.locator('[data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });
      await expect(page.locator('[data-testid="session-s2"]')).toBeVisible({ timeout: 5000 });

      // Select session 1 and add terminals
      await page.locator('[data-testid="session-s1"]').click();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });
      await addMockTerminal(page, 's1', 'term-1a', 'Shell 1');
      await addMockTerminal(page, 's1', 'term-1b', 'Shell 2');
      await expect(page.locator('.utility-terminal-panel')).toBeVisible();
      await expect(page.locator('.terminal-tab')).toHaveCount(2);

      // Switch to session 2 using keyboard shortcut (⌘2) to avoid overlay issues
      await page.keyboard.press('Meta+2');
      // Wait for session switch
      await page.waitForTimeout(100);

      // Panel should not be visible (session 2 hasn't opened it)
      await expect(page.locator('.utility-terminal-panel')).not.toBeVisible({ timeout: 2000 });

      // Add a terminal to session 2
      await addMockTerminal(page, 's2', 'term-2a', 'Shell A');
      await expect(page.locator('.utility-terminal-panel')).toBeVisible();
      await expect(page.locator('.terminal-tab')).toHaveCount(1); // Only 1 tab

      // Switch back to session 1 using keyboard shortcut (⌘1)
      await page.keyboard.press('Meta+1');
      await page.waitForTimeout(100);

      // Should still have 2 tabs
      await expect(page.locator('.utility-terminal-panel')).toBeVisible();
      await expect(page.locator('.terminal-tab')).toHaveCount(2);
    });
  });
});
