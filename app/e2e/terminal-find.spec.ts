import { test, expect } from './fixtures';

declare global {
  interface Window {
    __TEST_GET_SESSION_PANE_VISIBLE_TEXT?: (sessionId: string) => string;
  }
}

async function openTerminalSession(
  page: import('@playwright/test').Page,
  daemon: { start: () => Promise<void>; injectSession: (session: { id: string; label: string; state: string; directory?: string; workspace_id?: string }) => Promise<void> },
  sessionId: string,
) {
  await daemon.start();
  await page.goto('/');
  await page.waitForSelector('.dashboard');
  const workspaceId = `workspace-${sessionId}`;
  await page.evaluate(({ id, workspace }) => {
    window.__TEST_INJECT_SESSION?.({
      id,
      label: 'Terminal Find',
      state: 'working',
      cwd: '/tmp/test/terminal-find',
      workspaceId: workspace,
    });
  }, { id: sessionId, workspace: workspaceId });
  await daemon.injectSession({
    id: sessionId,
    label: 'Terminal Find',
    state: 'working',
    directory: '/tmp/test/terminal-find',
    workspace_id: workspaceId,
  });
  await page.locator(`[data-testid="session-${sessionId}"]`).click();
  const terminal = page.locator(`[data-pane-session-id="${sessionId}"][data-pane-kind="agent"] .terminal-container`);
  await expect(terminal).toBeVisible({ timeout: 5000 });
  // The container becomes visible before the Ghostty wasm terminal finishes
  // initializing, and PTY data delivered before the pane handle registers is
  // dropped by design (a real attach replays it; the e2e mock has no replay).
  // Wait for the pane's connect_terminal signal before writing output.
  await expect
    .poll(
      async () => page.evaluate(
        (id) => (window.__TEST_GET_SESSION_INPUT_EVENTS?.(id) ?? [])
          .some((event) => event.event === 'connect_terminal'),
        sessionId,
      ),
      { timeout: 5000 },
    )
    .toBe(true);
  return terminal;
}

async function writeTerminalOutput(
  page: import('@playwright/test').Page,
  sessionId: string,
  output: string,
) {
  await page.evaluate(({ id, data }) => {
    window.__TEST_EMIT_PTY_DATA?.(id, data);
  }, { id: sessionId, data: output });
}

test.describe('Ghostty terminal find', () => {
  test('cmd+f finds matches across scrollback and navigates to them', async ({ page, daemon }) => {
    const terminal = await openTerminalSession(page, daemon, 's-find');
    // 80 rows: the needle appears early (scrolls out of view) and near the end.
    const lines = Array.from({ length: 80 }, (_, index) => {
      if (index === 4) return 'first FIND_NEEDLE here';
      if (index === 75) return 'second FIND_NEEDLE here';
      return `line-${String(index).padStart(3, '0')}`;
    }).join('\r\n');
    await writeTerminalOutput(page, 's-find', `[2J[H${lines}`);

    await expect
      .poll(
        async () => page.evaluate(() => window.__TEST_GET_SESSION_PANE_TEXT?.('s-find') ?? ''),
        { timeout: 5000 },
      )
      .toContain('second FIND_NEEDLE');

    await terminal.click({ position: { x: 100, y: 100 } });
    await page.keyboard.press('Meta+f');
    const findBar = page.locator('[data-testid="ghostty-find-bar"]');
    await expect(findBar).toBeVisible();
    const findInput = page.locator('[data-testid="ghostty-find-input"]');
    await expect(findInput).toBeFocused();

    await findInput.pressSequentially('find_needle', { delay: 10 });
    const count = page.locator('[data-testid="ghostty-find-count"]');
    await expect(count).toHaveText('2/2', { timeout: 5000 });

    // Enter walks upward to the first (older) match and scrolls it into view.
    await page.keyboard.press('Enter');
    await expect(count).toHaveText('1/2');
    await expect
      .poll(
        async () => page.evaluate(() => window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('s-find') ?? ''),
        { timeout: 3000 },
      )
      .toContain('first FIND_NEEDLE');

    // Wraps around back to the newest match.
    await page.keyboard.press('Enter');
    await expect(count).toHaveText('2/2');
    await expect
      .poll(
        async () => page.evaluate(() => window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('s-find') ?? ''),
        { timeout: 3000 },
      )
      .toContain('second FIND_NEEDLE');
  });

  test('find input keystrokes never reach the PTY and escape returns focus', async ({ page, daemon }) => {
    const terminal = await openTerminalSession(page, daemon, 's-find-isolated');
    await writeTerminalOutput(page, 's-find-isolated', '[2J[Hisolation target');

    await terminal.click({ position: { x: 100, y: 8 } });
    await page.keyboard.press('Meta+f');
    const findInput = page.locator('[data-testid="ghostty-find-input"]');
    await expect(findInput).toBeFocused();
    await findInput.pressSequentially('secret', { delay: 10 });

    const leaked = await page.evaluate(() => (
      (window.__TEST_GET_SESSION_INPUT_EVENTS?.('s-find-isolated') ?? [])
        .filter((event) => event.event === 'send_to_pty')
        .map((event) => event.data ?? '')
        .join('')
    ));
    expect(leaked).not.toContain('s');

    await page.keyboard.press('Escape');
    await expect(page.locator('[data-testid="ghostty-find-bar"]')).toBeHidden();
    await expect(terminal).toBeFocused();
  });

  test('case toggle narrows matches', async ({ page, daemon }) => {
    await openTerminalSession(page, daemon, 's-find-case');
    await writeTerminalOutput(page, 's-find-case', '[2J[HCase case CASE');

    await expect
      .poll(
        async () => page.evaluate(() => window.__TEST_GET_SESSION_PANE_TEXT?.('s-find-case') ?? ''),
        { timeout: 5000 },
      )
      .toContain('Case case CASE');

    await page.keyboard.press('Meta+f');
    const findInput = page.locator('[data-testid="ghostty-find-input"]');
    await findInput.pressSequentially('case', { delay: 10 });
    const count = page.locator('[data-testid="ghostty-find-count"]');
    await expect(count).toHaveText('3/3', { timeout: 5000 });

    await page.getByRole('button', { name: 'Match case' }).click();
    await expect(count).toHaveText('1/1', { timeout: 5000 });
  });
});
