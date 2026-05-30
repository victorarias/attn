import { test, expect } from './fixtures';

type WorkspaceSessionFixture = {
  id: string;
  label: string;
  paneId: string;
  cwd: string;
};

async function injectWorkspace(
  page: import('@playwright/test').Page,
  workspaceId: string,
  sessions: WorkspaceSessionFixture[],
  activePaneId = sessions[0]?.paneId || ''
) {
  await page.evaluate(({ workspaceId, sessions, activePaneId }) => {
    for (const session of sessions) {
      window.__TEST_INJECT_SESSION?.({
        id: session.id,
        label: session.label,
        state: 'working',
        cwd: session.cwd,
        agent: 'shell',
        workspaceId,
      });
    }

    const workspace = {
      agents: sessions.map((session) => ({
        id: session.paneId,
        runtimeId: session.id,
        sessionId: session.id,
        title: session.label,
      })),
      layoutTree: sessions.length === 1
        ? { type: 'pane', paneId: sessions[0].paneId }
        : {
            type: 'split',
            splitId: `${workspaceId}-root`,
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: sessions[0].paneId },
              { type: 'pane', paneId: sessions[1].paneId },
            ],
          },
    };

    for (const session of sessions) {
      window.__TEST_SET_SESSION_WORKSPACE?.(session.id, workspace, activePaneId);
    }
  }, { workspaceId, sessions, activePaneId });
}

test.describe('Workspace Sessions', () => {
  test('switches workspaces and Cmd+number jumps to the first session', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await injectWorkspace(page, 'workspace-a', [
      { id: 'a1', label: 'alpha-one', paneId: 'pane-a1', cwd: '/tmp/workspace-a' },
      { id: 'a2', label: 'alpha-two', paneId: 'pane-a2', cwd: '/tmp/workspace-a' },
    ], 'pane-a2');
    await injectWorkspace(page, 'workspace-b', [
      { id: 'b1', label: 'beta-one', paneId: 'pane-b1', cwd: '/tmp/workspace-b' },
      { id: 'b2', label: 'beta-two', paneId: 'pane-b2', cwd: '/tmp/workspace-b' },
    ], 'pane-b2');

    await expect(page.locator('[data-testid="sidebar-workspace-workspace-a"]')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('[data-testid="sidebar-workspace-workspace-b"]')).toBeVisible();

    await page.locator('[data-testid="session-a1"]').click();
    await expect(page.locator('[data-session-terminal-workspace="workspace-a"]')).toBeVisible({ timeout: 2000 });

    await page.locator('[data-testid="sidebar-session-a2"]').click();
    await expect(page.locator('[data-testid="sidebar-workspace-workspace-a"]')).toHaveClass(/selected/);
    await expect(page.locator('[data-testid="sidebar-session-a2"]')).toHaveClass(/selected/);
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-a1"]')).toBeVisible();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-a2"]')).toBeVisible();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-b1"]')).toHaveCount(0);

    await page.locator('[data-testid="sidebar-workspace-workspace-b"] .workspace-group-header').click();
    await expect(page.locator('[data-testid="sidebar-workspace-workspace-b"]')).toHaveClass(/selected/);
    await expect(page.locator('[data-testid="sidebar-session-b1"]')).toHaveClass(/selected/);
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-b1"]')).toBeVisible();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-b2"]')).toBeVisible();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-a1"]')).toHaveCount(0);

    await page.keyboard.press('Meta+1');
    await expect(page.locator('[data-testid="sidebar-workspace-workspace-a"]')).toHaveClass(/selected/);
    await expect(page.locator('[data-testid="sidebar-session-a1"]')).toHaveClass(/selected/);
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-a1"]')).toBeVisible();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-a2"]')).toBeVisible();

    await page.keyboard.press('Meta+2');
    await expect(page.locator('[data-testid="sidebar-workspace-workspace-b"]')).toHaveClass(/selected/);
    await expect(page.locator('[data-testid="sidebar-session-b1"]')).toHaveClass(/selected/);
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-b1"]')).toBeVisible();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-b2"]')).toBeVisible();
  });

  test('Cmd+Shift+N opens the new-workspace picker while Cmd+N opens new-session picker', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await injectWorkspace(page, 'workspace-shortcuts', [
      { id: 'shortcut-a', label: 'shortcut-a', paneId: 'pane-shortcut-a', cwd: '/tmp/workspace-shortcuts' },
    ]);

    await page.locator('[data-testid="session-shortcut-a"]').click();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-shortcut-a"]')).toBeVisible({ timeout: 5000 });

    await page.keyboard.press('Meta+n');
    await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });
    await expect(page.locator('.picker-title')).toHaveText('New Session Location');
    await page.keyboard.press('Escape');
    await expect(page.locator('.location-picker-overlay')).toHaveCount(0, { timeout: 2000 });

    await page.keyboard.press('Meta+Shift+n');
    await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });
    await expect(page.locator('.picker-title')).toHaveText('New Workspace Location');
  });
});
