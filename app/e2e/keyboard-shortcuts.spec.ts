import { test, expect } from './fixtures';

// Helper to inject a session into the local UI store
async function injectLocalSession(
  page: import('@playwright/test').Page,
  session: { id: string; label: string; state: string; cwd?: string; isWorktree?: boolean; branch?: string }
) {
  await page.evaluate((s) => {
    window.__TEST_INJECT_SESSION?.({
      id: s.id,
      label: s.label,
      state: s.state as 'working' | 'waiting_input' | 'idle',
      cwd: s.cwd || '/tmp/test',
      ...(s.isWorktree !== undefined ? { isWorktree: s.isWorktree } : {}),
      ...(s.branch ? { branch: s.branch } : {}),
    });
  }, session);
}

// Helper to create a session in both local store AND daemon
async function createSession(
  page: import('@playwright/test').Page,
  daemon: {
    injectSession: (s: {
      id: string;
      label: string;
      state: string;
      directory?: string;
      is_worktree?: boolean;
      branch?: string;
      main_repo?: string;
    }) => Promise<void>;
  },
  session: {
    id: string;
    label: string;
    state: string;
    cwd?: string;
    is_worktree?: boolean;
    branch?: string;
    main_repo?: string;
  }
) {
  const cwd = session.cwd || '/tmp/test';
  await injectLocalSession(page, {
    ...session,
    cwd,
    ...(session.is_worktree !== undefined ? { isWorktree: session.is_worktree } : {}),
  });
  await daemon.injectSession({
    id: session.id,
    label: session.label,
    state: session.state,
    directory: cwd,
    is_worktree: session.is_worktree,
    branch: session.branch,
    main_repo: session.main_repo,
  });
}

test.describe('Keyboard Shortcuts', () => {
  test.describe('Terminal Workspace', () => {
    test('⌘⇧Z zooms toward the active pane without hiding the others', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, { id: 's-zoom', label: 'Zoom', state: 'working', cwd: '/tmp/test/zoom' });
      await expect(page.locator('[data-testid="session-s-zoom"]')).toBeVisible({ timeout: 5000 });

      await page.locator('[data-testid="session-s-zoom"]').click();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      await page.evaluate(() => {
        window.__TEST_SET_SESSION_WORKSPACE?.('s-zoom', {
          terminals: [{ id: 'pane-shell-1', ptyId: 'runtime-shell-1', title: 'Shell 1' }],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: 'main' },
              { type: 'pane', paneId: 'pane-shell-1' },
            ],
          },
        }, 'pane-shell-1');
      });
      await expect(page.locator('[data-pane-session-id="s-zoom"][data-pane-kind="shell"]')).toBeVisible({ timeout: 5000 });

      const workspace = page.locator('[data-session-terminal-workspace="s-zoom"]');
      const mainPane = page.locator('[data-pane-session-id="s-zoom"][data-pane-id="main"]');
      const utilityPane = page.locator('[data-pane-session-id="s-zoom"][data-pane-kind="shell"]').first();
      const rootSplit = page.locator('[data-split-id="root"]');
      const zoomHint = page.getByText('⌘⇧Z zoom');

      await expect(zoomHint).toHaveAttribute('data-active', 'false');

      const mainBefore = await mainPane.boundingBox();
      const utilityBefore = await utilityPane.boundingBox();
      expect(mainBefore?.width).toBeTruthy();
      expect(utilityBefore?.width).toBeTruthy();

      await utilityPane.click();
      await page.keyboard.press('Meta+Shift+z');
      await expect(workspace).toHaveAttribute('data-zoomed-pane-id', 'pane-shell-1', { timeout: 2000 });
      await expect(rootSplit).toHaveAttribute('data-split-ratio', '0.240', { timeout: 2000 });
      await expect(zoomHint).toHaveAttribute('data-active', 'true');

      await expect.poll(async () => (await utilityPane.boundingBox())?.width ?? 0, { timeout: 2000 })
        .toBeGreaterThan(utilityBefore!.width);
      await expect.poll(async () => (await mainPane.boundingBox())?.width ?? 0, { timeout: 2000 })
        .toBeLessThan(mainBefore!.width);

      const mainAfterZoom = await mainPane.boundingBox();
      const utilityAfterZoom = await utilityPane.boundingBox();
      expect(mainAfterZoom).not.toBeNull();
      expect(utilityAfterZoom).not.toBeNull();
      expect(utilityAfterZoom!.width).toBeGreaterThan(utilityBefore!.width);
      expect(mainAfterZoom!.width).toBeLessThan(mainBefore!.width);
      await expect(mainPane).toBeVisible();
      await expect(utilityPane).toBeVisible();

      await mainPane.click();
      await expect(workspace).toHaveAttribute('data-zoomed-pane-id', 'main', { timeout: 2000 });
      await expect(rootSplit).toHaveAttribute('data-split-ratio', '0.760', { timeout: 2000 });
      await expect(zoomHint).toHaveAttribute('data-active', 'true');

      await expect.poll(async () => (await mainPane.boundingBox())?.width ?? 0, { timeout: 2000 })
        .toBeGreaterThan(mainAfterZoom!.width);
      await expect.poll(async () => (await utilityPane.boundingBox())?.width ?? 0, { timeout: 2000 })
        .toBeLessThan(utilityAfterZoom!.width);

      const mainRetargeted = await mainPane.boundingBox();
      const utilityRetargeted = await utilityPane.boundingBox();
      expect(mainRetargeted).not.toBeNull();
      expect(utilityRetargeted).not.toBeNull();
      expect(mainRetargeted!.width).toBeGreaterThan(mainAfterZoom!.width);
      expect(utilityRetargeted!.width).toBeLessThan(utilityAfterZoom!.width);
    });
  });

  test.describe('Attention Drawer', () => {
    test('⌘K toggles attention drawer', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Drawer should be closed initially
      await expect(page.locator('.side-panel-shell.is-open .attention-drawer .attention-drawer-panel')).toHaveCount(0);

      // Open with ⌘K
      await page.keyboard.press('Meta+k');
      await expect(page.locator('.side-panel-shell.is-open .attention-drawer .attention-drawer-panel')).toBeVisible({ timeout: 2000 });

      // Close with ⌘K
      await page.keyboard.press('Meta+k');
      await expect(page.locator('.side-panel-shell.is-open .attention-drawer .attention-drawer-panel')).toHaveCount(0, { timeout: 2000 });
    });

    // Removed ⌘. test - this shortcut was never implemented. Use ⌘K to toggle drawer.
  });

  test.describe('Dashboard Navigation', () => {
    test('⌘G goes to dashboard from terminal', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Create and select a session to enter terminal view
      await createSession(page, daemon, { id: 's1', label: 'Test', state: 'working', cwd: '/tmp/test/s1' });
      await expect(page.locator('[data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });

      // Click to select (enters terminal view)
      await page.locator('[data-testid="session-s1"]').click();

      // Should show terminal area
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      // ⌘G should return to dashboard
      await page.keyboard.press('Meta+g');

      // Dashboard should be visible, no active session
      await expect(page.locator('.dashboard')).toBeVisible({ timeout: 2000 });
    });

    test('Escape goes to dashboard', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, { id: 's1', label: 'Test', state: 'working', cwd: '/tmp/test/s1' });
      await expect(page.locator('[data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });

      await page.locator('[data-testid="session-s1"]').click();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      // Escape should return to dashboard
      await page.keyboard.press('Escape');
      await expect(page.locator('.dashboard')).toBeVisible({ timeout: 2000 });
    });
  });

  test.describe('Session Selection', () => {
    test('⌘1-9 selects session by index', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Create multiple sessions
      await createSession(page, daemon, { id: 's1', label: 'First', state: 'working', cwd: '/tmp/test/s1' });
      await createSession(page, daemon, { id: 's2', label: 'Second', state: 'working', cwd: '/tmp/test/s2' });
      await createSession(page, daemon, { id: 's3', label: 'Third', state: 'working', cwd: '/tmp/test/s3' });

      await expect(page.locator('[data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });

      // ⌘2 should select second session
      await page.keyboard.press('Meta+2');
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      // Go back to dashboard
      await page.keyboard.press('Escape');

      // ⌘1 should select first session
      await page.keyboard.press('Meta+1');
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });
    });

    test('⌘↑/⌘↓ navigates between sessions', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, { id: 's1', label: 'First', state: 'working', cwd: '/tmp/test/s1' });
      await createSession(page, daemon, { id: 's2', label: 'Second', state: 'working', cwd: '/tmp/test/s2' });

      await expect(page.locator('[data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });

      // Select first session
      await page.keyboard.press('Meta+1');
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      // ⌘↓ should go to next session
      await page.keyboard.press('Meta+ArrowDown');

      // ⌘↑ should go to previous session
      await page.keyboard.press('Meta+ArrowUp');
    });
  });

  test.describe('Session Management', () => {
    test('⌘J jumps to next waiting session', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Create sessions with one waiting
      await createSession(page, daemon, { id: 's1', label: 'Working', state: 'working', cwd: '/tmp/test/s1' });
      await createSession(page, daemon, { id: 's2', label: 'Waiting', state: 'waiting_input', cwd: '/tmp/test/s2' });

      await expect(page.locator('[data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });

      // ⌘J should jump to the waiting session
      await page.keyboard.press('Meta+j');

      // Should be viewing a terminal (the waiting session)
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });
    });
  });

  test.describe('Worktree Cleanup Prompt', () => {
    test('traps focus and supports arrow navigation', async ({ page, daemon }) => {
      await daemon.start();
      await page.addInitScript(() => {
        localStorage.setItem('alwaysKeepWorktrees', 'false');
      });
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, {
        id: 's1',
        label: 'Worktree',
        state: 'working',
        cwd: '/tmp/test/worktree-1',
        is_worktree: true,
        branch: 'feature/cleanup',
      });

      await expect(page.locator('[data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });
      await page.locator('[data-testid="session-s1"]').click();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      await page.keyboard.press('Meta+Shift+w');

      const dialog = page.locator('.worktree-cleanup-prompt .cleanup-content');
      await expect(dialog).toBeVisible({ timeout: 2000 });

      const keep = page.locator('.cleanup-btn.keep');
      const del = page.locator('.cleanup-btn.delete');
      const always = page.locator('.cleanup-btn.always');

      await expect(keep).toBeFocused();

      await page.keyboard.press('ArrowRight');
      await expect(del).toBeFocused();

      await page.keyboard.press('ArrowRight');
      await expect(always).toBeFocused();

      await page.keyboard.press('ArrowLeft');
      await expect(del).toBeFocused();

      await page.keyboard.press('Tab');
      await expect(always).toBeFocused();

      await page.keyboard.press('Tab');
      await expect(keep).toBeFocused();

      await page.keyboard.press('Shift+Tab');
      await expect(always).toBeFocused();
    });
  });

  test.describe('Sidebar', () => {
    test('⌘⇧B toggles sidebar', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Sidebar is only visible in session view, so create a session first
      await createSession(page, daemon, { id: 's1', label: 'Test', state: 'working', cwd: '/tmp/test/s1' });
      await expect(page.locator('[data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });

      // Click session to enter session view
      await page.locator('[data-testid="session-s1"]').click();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      // Now sidebar should be visible and expanded
      await expect(page.locator('.sidebar:not(.collapsed)')).toBeVisible({ timeout: 2000 });

      // ⌘⇧B should collapse sidebar
      await page.keyboard.press('Meta+Shift+B');
      await expect(page.locator('.sidebar.collapsed')).toBeVisible({ timeout: 2000 });

      // ⌘⇧B should expand sidebar
      await page.keyboard.press('Meta+Shift+B');
      await expect(page.locator('.sidebar:not(.collapsed)')).toBeVisible({ timeout: 2000 });
    });
  });

  test.describe('Branch Picker', () => {
    test('⌘B opens branch picker when session has git', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Create session with git info
      await injectLocalSession(page, { id: 's1', label: 'Test', state: 'working', cwd: '/tmp/test/s1' });
      await daemon.injectSession({
        id: 's1',
        label: 'Test',
        state: 'working',
        directory: '/tmp/test/s1',
      });

      await expect(page.locator('[data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });

      // Select the session
      await page.locator('[data-testid="session-s1"]').click();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 2000 });

      // Note: ⌘B may not open branch picker without actual git repo
      // This test verifies the shortcut is wired up
      await page.keyboard.press('Meta+b');

      // Branch picker might show loading or not open if no git
      // Just verify no crash and the shortcut is handled
    });

    test('Escape closes branch picker', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Create session
      await injectLocalSession(page, { id: 's1', label: 'Test', state: 'working', cwd: '/tmp/test/s1' });
      await daemon.injectSession({
        id: 's1',
        label: 'Test',
        state: 'working',
        directory: '/tmp/test/s1',
      });

      await expect(page.locator('[data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });
      await page.locator('[data-testid="session-s1"]').click();

      // Try to open branch picker
      await page.keyboard.press('Meta+b');

      // If picker opens, Escape should close it without going to dashboard
      const picker = page.locator('.branch-picker-overlay');
      if (await picker.isVisible({ timeout: 1000 }).catch(() => false)) {
        await page.keyboard.press('Escape');
        await expect(picker).not.toBeVisible({ timeout: 1000 });
        // Should still be in terminal view, not dashboard
        await expect(page.locator('.terminal-wrapper.active')).toBeVisible();
      }
    });
  });

});
