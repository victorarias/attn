import { test, expect } from './fixtures';

const OSC = ']133;';
const BEL = '';

// A captured fish 4.x interactive lifecycle, reduced to its marker skeleton:
// prompt-start (A), input-start (B), pre-exec with the percent-encoded
// command line (C), and command-end with the exit code (D).
//   row 0: prompt> echo hello
//   row 1: hello
//   row 2: world
//   row 3: prompt>            (next prompt)
const BLOCK_STREAM = '[2J[H'
  + `${OSC}A;click_events=1${BEL}prompt> ${OSC}B${BEL}echo hello\r\n`
  + `${OSC}C;cmdline_url=echo%20hello${BEL}hello\r\nworld\r\n`
  + `${OSC}D;0${BEL}`
  + `${OSC}A;click_events=1${BEL}prompt> `;

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
      label: 'Terminal Blocks',
      state: 'working',
      cwd: '/tmp/test/terminal-blocks',
      workspaceId: workspace,
    });
  }, { id: sessionId, workspace: workspaceId });
  await daemon.injectSession({
    id: sessionId,
    label: 'Terminal Blocks',
    state: 'working',
    directory: '/tmp/test/terminal-blocks',
    workspace_id: workspaceId,
  });
  await page.locator(`[data-testid="session-${sessionId}"]`).click();
  const terminal = page.locator(`[data-pane-session-id="${sessionId}"][data-pane-kind="agent"] .terminal-container`);
  await expect(terminal).toBeVisible({ timeout: 5000 });
  return terminal;
}

async function writeBlockStream(
  page: import('@playwright/test').Page,
  terminal: import('@playwright/test').Locator,
  sessionId: string,
) {
  await page.evaluate(({ id, data }) => {
    window.__TEST_EMIT_PTY_DATA?.(id, data);
  }, { id: sessionId, data: BLOCK_STREAM });
  await expect
    .poll(
      async () => page.evaluate((id) => window.__TEST_GET_SESSION_PANE_TEXT?.(id) ?? '', sessionId),
      { timeout: 5000 },
    )
    .toContain('world');
  // Pixel centers of buffer rows 0 (command line) and 1 (first output row).
  return terminal.evaluate((element, id) => {
    const canvas = element.querySelector('canvas');
    if (!canvas) throw new Error('Terminal canvas not found');
    const paneSize = (window as Window & {
      __TEST_GET_SESSION_PANE_SIZE?: (sessionId: string) => { rows: number } | null;
    }).__TEST_GET_SESSION_PANE_SIZE?.(id);
    if (!paneSize?.rows) throw new Error('Terminal pane size not available');
    const terminalRect = element.getBoundingClientRect();
    const canvasRect = canvas.getBoundingClientRect();
    const rowHeight = canvasRect.height / paneSize.rows;
    const canvasTop = canvasRect.top - terminalRect.top;
    return {
      commandRowY: canvasTop + rowHeight * 0.5,
      outputRowY: canvasTop + rowHeight * 1.5,
    };
  }, sessionId);
}

test.describe('Ghostty terminal command blocks', () => {
  test('clicking a block then cmd+c copies command+output, cmd+shift+c copies the command', async ({ page, context, daemon }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const terminal = await openTerminalSession(page, daemon, 's-block-copy');
    const rows = await writeBlockStream(page, terminal, 's-block-copy');
    await page.evaluate(() => navigator.clipboard.writeText('clipboard-sentinel'));

    // A plain click on the block's output selects the whole block.
    await terminal.click({ position: { x: 30, y: rows.outputRowY } });
    await page.keyboard.press('Meta+c');
    await expect
      .poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toBe('echo hello\nhello\nworld');

    await page.keyboard.press('Meta+Shift+c');
    await expect
      .poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toBe('echo hello');
  });

  test('clicking the command line arms cmd+c with exactly the command text', async ({ page, context, daemon }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const terminal = await openTerminalSession(page, daemon, 's-block-command');
    const rows = await writeBlockStream(page, terminal, 's-block-command');
    await page.evaluate(() => navigator.clipboard.writeText('clipboard-sentinel'));

    // Click inside the command region of the prompt row; the command comes
    // from the pre-exec marker, not from screen scraping, so it excludes the
    // prompt decoration.
    await terminal.click({ position: { x: 100, y: rows.commandRowY } });
    await page.keyboard.press('Meta+c');
    await expect
      .poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toBe('echo hello');
  });

  test('clicking outside any block does not arm cmd+c', async ({ page, context, daemon }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const terminal = await openTerminalSession(page, daemon, 's-block-miss');
    const rows = await writeBlockStream(page, terminal, 's-block-miss');
    await page.evaluate(() => navigator.clipboard.writeText('clipboard-sentinel'));

    // Click well below the block (the empty area under the next prompt).
    const bounds = await terminal.boundingBox();
    expect(bounds).not.toBeNull();
    await terminal.click({ position: { x: 30, y: rows.outputRowY + bounds!.height / 2 } });
    await page.keyboard.press('Meta+c');
    // Clipboard must stay untouched: poll a moment, expect the sentinel.
    await page.waitForTimeout(300);
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('clipboard-sentinel');
  });

  test('right-click on a block opens a context menu that copies command and output', async ({ page, context, daemon }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const terminal = await openTerminalSession(page, daemon, 's-block-menu');
    const rows = await writeBlockStream(page, terminal, 's-block-menu');
    await page.evaluate(() => navigator.clipboard.writeText('clipboard-sentinel'));

    await terminal.click({ button: 'right', position: { x: 30, y: rows.outputRowY } });
    const menu = page.locator('[data-testid="terminal-context-menu"]');
    await expect(menu).toBeVisible();

    await page.locator('[data-testid="terminal-context-menu-copy-output"]').click();
    await expect(menu).not.toBeVisible();
    await expect
      .poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toBe('hello\nworld');

    await terminal.click({ button: 'right', position: { x: 30, y: rows.outputRowY } });
    await page.locator('[data-testid="terminal-context-menu-copy-command"]').click();
    await expect
      .poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toBe('echo hello');

    await terminal.click({ button: 'right', position: { x: 30, y: rows.outputRowY } });
    await page.locator('[data-testid="terminal-context-menu-copy"]').click();
    await expect
      .poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toBe('echo hello\nhello\nworld');
  });

  test('right-click outside any block disables block items and paste sends clipboard to the pty', async ({ page, context, daemon }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const terminal = await openTerminalSession(page, daemon, 's-block-menu-out');
    const rows = await writeBlockStream(page, terminal, 's-block-menu-out');
    await page.evaluate(() => navigator.clipboard.writeText('pasted-text'));

    const bounds = await terminal.boundingBox();
    expect(bounds).not.toBeNull();
    await terminal.click({
      button: 'right',
      position: { x: 30, y: rows.outputRowY + bounds!.height / 2 },
    });
    await expect(page.locator('[data-testid="terminal-context-menu"]')).toBeVisible();
    await expect(page.locator('[data-testid="terminal-context-menu-copy-command"]')).toBeDisabled();
    await expect(page.locator('[data-testid="terminal-context-menu-copy-output"]')).toBeDisabled();
    await expect(page.locator('[data-testid="terminal-context-menu-filter-block"]')).toBeDisabled();

    await page.locator('[data-testid="terminal-context-menu-paste"]').click();
    await expect
      .poll(
        async () => page.evaluate(
          (id) => (window.__TEST_GET_SESSION_INPUT_EVENTS?.(id) ?? [])
            .filter((event) => event.event === 'send_to_pty' && event.data === 'pasted-text').length,
          's-block-menu-out',
        ),
        { timeout: 3000 },
      )
      .toBe(1);
  });

  test('filter block output lists matching lines and highlights matches', async ({ page, context, daemon }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const terminal = await openTerminalSession(page, daemon, 's-block-filter');
    const rows = await writeBlockStream(page, terminal, 's-block-filter');

    await terminal.click({ button: 'right', position: { x: 30, y: rows.outputRowY } });
    await page.locator('[data-testid="terminal-context-menu-filter-block"]').click();

    const filterInput = page.locator('[data-testid="ghostty-filter-input"]');
    await expect(filterInput).toBeVisible();
    await expect(filterInput).toBeFocused();
    await filterInput.fill('wor');

    const results = page.locator('[data-testid="ghostty-filter-results"]');
    await expect(results.locator('.ghostty-filter-line')).toHaveCount(1, { timeout: 3000 });
    await expect(results.locator('.ghostty-filter-line mark')).toHaveText('wor');
    await expect(page.locator('[data-testid="ghostty-filter-count"]')).toHaveText('1 line');

    // A query matching nothing shows the empty state, not stale lines.
    await filterInput.fill('absent-needle');
    await expect(results.locator('.ghostty-filter-line')).toHaveCount(0, { timeout: 3000 });
    await expect(results.locator('.ghostty-filter-empty')).toBeVisible();

    // Esc closes the filter and returns focus to the terminal.
    await filterInput.press('Escape');
    await expect(page.locator('[data-testid="ghostty-filter-panel"]')).not.toBeVisible();
  });

  test('triple click selects and copies the whole row', async ({ page, context, daemon }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const terminal = await openTerminalSession(page, daemon, 's-block-triple');
    const rows = await writeBlockStream(page, terminal, 's-block-triple');
    await page.evaluate(() => navigator.clipboard.writeText('clipboard-sentinel'));

    // Triple click on the prompt row: distinct from the double-click word
    // ('echo') and the block command ('echo hello').
    await terminal.click({ position: { x: 100, y: rows.commandRowY }, clickCount: 3 });
    await expect
      .poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toBe('prompt> echo hello');
  });
});
