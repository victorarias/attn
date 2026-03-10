import { test, expect } from './fixtures';

const realPtyEnabled = process.env.VITE_FORCE_REAL_PTY === '1';

async function injectLocalSession(
  page: import('@playwright/test').Page,
  session: { id: string; label: string; state: string; cwd?: string; agent?: 'codex' | 'claude' }
) {
  await page.evaluate((s) => {
    window.__TEST_INJECT_SESSION?.({
      id: s.id,
      label: s.label,
      state: s.state as 'working' | 'waiting_input' | 'idle',
      cwd: s.cwd || '/tmp/test',
      agent: s.agent,
    });
  }, session);
}

async function createSession(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (s: { id: string; label: string; state: string; directory?: string; agent?: 'codex' | 'claude' }) => Promise<void> },
  session: { id: string; label: string; state: string; cwd?: string; agent?: 'codex' | 'claude' }
) {
  const cwd = session.cwd || '/tmp/test';
  await injectLocalSession(page, { ...session, cwd });
  await daemon.injectSession({
    id: session.id,
    label: session.label,
    state: session.state,
    directory: cwd,
    agent: session.agent,
  });
}

async function dispatchShortcut(
  page: import('@playwright/test').Page,
  key: string,
  modifiers: { meta?: boolean; shift?: boolean; ctrl?: boolean; alt?: boolean } = {}
) {
  await page.evaluate(
    ({ key, modifiers }) => {
      const event = new KeyboardEvent('keydown', {
        key,
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

async function selectSessionByTestId(
  page: import('@playwright/test').Page,
  sessionID: string
) {
  await page.evaluate((id) => {
    const element = document.querySelector(`[data-testid="sidebar-session-${id}"]`);
    if (!element) {
      throw new Error(`Session row not found: ${id}`);
    }
    element.dispatchEvent(
      new MouseEvent('click', { bubbles: true, cancelable: true, view: window })
    );
  }, sessionID);
}

async function readUtilityScrollback(wsUrl: string, ptyID: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(wsUrl);
    const timeout = setTimeout(() => {
      ws.close();
      reject(new Error('attach_session timeout'));
    }, 5000);

    ws.onopen = () => {
      ws.send(JSON.stringify({ cmd: 'attach_session', id: ptyID }));
    };

    ws.onmessage = (event) => {
      const msg = JSON.parse(event.data.toString()) as {
        event?: string;
        id?: string;
        success?: boolean;
        scrollback?: string;
      };
      if (msg.event !== 'attach_result' || msg.id !== ptyID) {
        return;
      }
      clearTimeout(timeout);
      ws.close();
      if (!msg.success) {
        reject(new Error('attach_session failed'));
        return;
      }
      const raw = msg.scrollback ? Buffer.from(msg.scrollback, 'base64').toString('utf8') : '';
      resolve(raw);
    };

    ws.onerror = (err) => {
      clearTimeout(timeout);
      reject(err);
    };
  });
}

async function sendWorkspaceSplitPane(
  wsUrl: string,
  sessionID: string,
  targetPaneID: string,
  direction: 'vertical' | 'horizontal'
): Promise<void> {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(wsUrl);
    const timeout = setTimeout(() => {
      ws.close();
      reject(new Error('workspace_split_pane timeout'));
    }, 5000);

    ws.onopen = () => {
      ws.send(JSON.stringify({
        cmd: 'workspace_split_pane',
        session_id: sessionID,
        target_pane_id: targetPaneID,
        direction,
      }));
    };

    ws.onmessage = (event) => {
      const msg = JSON.parse(event.data.toString()) as {
        event?: string;
        action?: string;
        session_id?: string;
        pane_id?: string;
        success?: boolean;
        error?: string;
      };
      if (
        msg.event !== 'workspace_action_result' ||
        msg.action !== 'workspace_split_pane' ||
        msg.session_id !== sessionID ||
        msg.pane_id !== targetPaneID
      ) {
        return;
      }

      clearTimeout(timeout);
      ws.close();
      if (msg.success) {
        resolve();
        return;
      }
      reject(new Error(msg.error || 'workspace_split_pane failed'));
    };

    ws.onerror = (err) => {
      clearTimeout(timeout);
      reject(err);
    };
  });
}

async function getSessionInputEvents(
  page: import('@playwright/test').Page,
  sessionID: string
): Promise<Array<{ event: string; data?: string }>> {
  return page.evaluate(
    (id) => (window as unknown as {
      __TEST_GET_SESSION_INPUT_EVENTS?: (sessionId: string) => Array<{ event: string; data?: string }>;
    }).__TEST_GET_SESSION_INPUT_EVENTS?.(id) ?? [],
    sessionID
  );
}

async function getMainTerminalText(
  page: import('@playwright/test').Page,
  sessionID: string
): Promise<string> {
  return page.evaluate(
    (id) => (window as unknown as {
      __TEST_GET_MAIN_TERMINAL_TEXT?: (sessionId: string) => string;
    }).__TEST_GET_MAIN_TERMINAL_TEXT?.(id) ?? '',
    sessionID
  );
}

test.describe('Utility Terminal Real PTY', () => {
  test('cmd+t terminal accepts keyboard input and reaches shell', async ({ page, daemon }) => {
    test.skip(!realPtyEnabled, 'Requires real PTY (VITE_FORCE_REAL_PTY=1)');

    const { wsUrl } = await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await createSession(page, daemon, {
      id: 's-real-pty',
      label: 'RealPTY',
      state: 'working',
      cwd: '/Users/victor.arias/projects/victor/attn',
    });
    await page.locator('[data-testid="session-s-real-pty"]').click();
    await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 5000 });

    await page.keyboard.press('Meta+t');
    await expect
      .poll(
        async () =>
          page.evaluate(
            (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
            's-real-pty'
          ),
        { timeout: 5000 }
      )
      .not.toBeNull();
    const utilityPtyID = await page.evaluate(
      (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
      's-real-pty'
    );
    expect(utilityPtyID).not.toBeNull();
    if (!utilityPtyID) {
      throw new Error('Utility PTY ID was not captured');
    }
    await expect(page.locator('.session-terminal-workspace')).toBeVisible({ timeout: 5000 });

    // Important regression check: Cmd+T should focus the utility terminal
    // immediately so typing appears without requiring an extra click.
    await page.keyboard.type('__PW_UTILITY_FOCUS__');
    await expect
      .poll(
        async () => {
          try {
            const scrollback = await readUtilityScrollback(wsUrl, utilityPtyID);
            return scrollback.includes('__PW_UTILITY_FOCUS__');
          } catch {
            return false;
          }
        },
        { timeout: 8000, intervals: [300, 500, 800, 1200, 1800] }
      )
      .toBe(true);

    await page.keyboard.press('Control+c');
    await page.keyboard.type('echo __PW_REALPTY_OK__');
    await page.keyboard.press('Enter');

    await expect
      .poll(
        async () => {
          try {
            const scrollback = await readUtilityScrollback(wsUrl, utilityPtyID);
            return scrollback.includes('__PW_REALPTY_OK__');
          } catch {
            return false;
          }
        },
        { timeout: 12000, intervals: [500, 1000, 1500, 2000] }
      )
      .toBe(true);
  });

  test('utility terminal keeps keyboard interactivity after switching sessions', async ({ page, daemon }) => {
    test.skip(!realPtyEnabled, 'Requires real PTY (VITE_FORCE_REAL_PTY=1)');

    const { wsUrl } = await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await createSession(page, daemon, {
      id: 's-real-pty-a',
      label: 'RealPTY-A',
      state: 'working',
      cwd: '/Users/victor.arias/projects/victor/attn',
    });
    await createSession(page, daemon, {
      id: 's-real-pty-b',
      label: 'RealPTY-B',
      state: 'working',
      cwd: '/Users/victor.arias/projects/victor/attn',
    });

    await selectSessionByTestId(page, 's-real-pty-a');
    await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 5000 });

    await page.keyboard.press('Meta+t');
    const utilityPtyID = await expect
      .poll(
        async () =>
          page.evaluate(
            (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
            's-real-pty-a'
          ),
        { timeout: 5000 }
      )
      .not.toBeNull()
      .then(async () =>
        page.evaluate(
          (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
          's-real-pty-a'
        )
      );

    if (!utilityPtyID) {
      throw new Error('Utility PTY ID was not captured');
    }

    await page.locator('.workspace-pane.utility-pane.active .terminal-container').click();
    await page.keyboard.type('echo __PW_SWITCH_A__');
    await page.keyboard.press('Enter');
    await expect
      .poll(
        async () => {
          try {
            const scrollback = await readUtilityScrollback(wsUrl, utilityPtyID);
            return scrollback.includes('__PW_SWITCH_A__');
          } catch {
            return false;
          }
        },
        { timeout: 10000, intervals: [300, 500, 800, 1200, 1800] }
      )
      .toBe(true);

    await selectSessionByTestId(page, 's-real-pty-b');
    await expect(page.locator('[data-testid="sidebar-session-s-real-pty-b"]')).toHaveClass(/selected/);

    await selectSessionByTestId(page, 's-real-pty-a');
    await expect(page.locator('[data-testid="sidebar-session-s-real-pty-a"]')).toHaveClass(/selected/);

    // Wait past App's delayed focus handoff; if focus is incorrectly stolen by
    // the main terminal, this write won't reach the utility PTY.
    await page.waitForTimeout(120);
    await page.keyboard.type('echo __PW_SWITCH_BACK__');
    await page.keyboard.press('Enter');

    await expect
      .poll(
        async () => {
          try {
            const scrollback = await readUtilityScrollback(wsUrl, utilityPtyID);
            return scrollback.includes('__PW_SWITCH_BACK__');
          } catch {
            return false;
          }
        },
        { timeout: 10000, intervals: [300, 500, 800, 1200, 1800] }
      )
      .toBe(true);
  });

  test('utility terminal keeps keyboard interactivity after dashboard roundtrip', async ({ page, daemon }) => {
    test.skip(!realPtyEnabled, 'Requires real PTY (VITE_FORCE_REAL_PTY=1)');

    const { wsUrl } = await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await createSession(page, daemon, {
      id: 's-real-pty-d',
      label: 'RealPTY-Dashboard',
      state: 'working',
      cwd: '/Users/victor.arias/projects/victor/attn',
    });

    await page.locator('[data-testid="session-s-real-pty-d"]').click();
    await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 5000 });

    await page.keyboard.press('Meta+t');
    await expect
      .poll(
        async () =>
          page.evaluate(
            (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
            's-real-pty-d'
          ),
        { timeout: 5000 }
      )
      .not.toBeNull();
    const utilityPtyID = await page.evaluate(
      (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
      's-real-pty-d'
    );
    if (!utilityPtyID) {
      throw new Error('Utility PTY ID was not captured');
    }

    await page.locator('.workspace-pane.utility-pane.active .terminal-container').click();
    await page.keyboard.type('echo __PW_DASH_BEFORE__');
    await page.keyboard.press('Enter');
    await expect
      .poll(
        async () => {
          try {
            const scrollback = await readUtilityScrollback(wsUrl, utilityPtyID);
            return scrollback.includes('__PW_DASH_BEFORE__');
          } catch {
            return false;
          }
        },
        { timeout: 10000, intervals: [300, 500, 800, 1200, 1800] }
      )
      .toBe(true);

    await sendWorkspaceSplitPane(wsUrl, 's-real-pty-main', 'main', 'vertical');
    await expect(page.locator('.dashboard')).toBeVisible({ timeout: 5000 });

    await page.locator('[data-testid="session-s-real-pty-d"]').click();
    await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('.session-terminal-workspace')).toBeVisible({ timeout: 5000 });

    // Output shown before dashboard switch should be restored in the
    // remounted utility xterm buffer without typing anything new.
    await expect
      .poll(
        async () =>
          page.evaluate(
            () =>
              ((window as unknown as { __TEST_GET_ACTIVE_UTILITY_TEXT?: () => string }).__TEST_GET_ACTIVE_UTILITY_TEXT?.() || '').includes(
                '__PW_DASH_BEFORE__'
              )
          ),
        { timeout: 10000, intervals: [300, 500, 800, 1200, 1800] }
      )
      .toBe(true);

    // Wait past delayed focus handoff in App; typing should still hit utility PTY.
    await page.waitForTimeout(120);
    await page.keyboard.type('echo __PW_DASH_AFTER__');
    await page.keyboard.press('Enter');

    await expect
      .poll(
        async () => {
          try {
            const scrollback = await readUtilityScrollback(wsUrl, utilityPtyID);
            return scrollback.includes('__PW_DASH_AFTER__');
          } catch {
            return false;
          }
        },
        { timeout: 10000, intervals: [300, 500, 800, 1200, 1800] }
      )
      .toBe(true);
  });

  test('older split panes remain writable after creating additional panes', async ({ page, daemon }) => {
    test.skip(!realPtyEnabled, 'Requires real PTY (VITE_FORCE_REAL_PTY=1)');

    const { wsUrl } = await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await createSession(page, daemon, {
      id: 's-real-pty-three',
      label: 'RealPTY-Three',
      state: 'working',
      cwd: '/Users/victor.arias/projects/victor/attn',
    });
    await page.locator('[data-testid="session-s-real-pty-three"]').click();
    await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 5000 });

    await page.keyboard.press('Meta+t');
    const shell1PtyID = await expect
      .poll(
        async () =>
          page.evaluate(
            (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
            's-real-pty-three'
          ),
        { timeout: 5000 }
      )
      .not.toBeNull()
      .then(async () =>
        page.evaluate(
          (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
          's-real-pty-three'
        )
      );
    if (!shell1PtyID) {
      throw new Error('Shell 1 PTY ID was not captured');
    }

    await page.keyboard.press('Meta+t');
    const shell2PtyID = await expect
      .poll(
        async () =>
          page.evaluate(
            (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
            's-real-pty-three'
          ),
        { timeout: 5000 }
      )
      .not.toBe(shell1PtyID)
      .then(async () =>
        page.evaluate(
          (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
          's-real-pty-three'
        )
      );
    if (!shell2PtyID || shell2PtyID === shell1PtyID) {
      throw new Error('Shell 2 PTY ID was not captured');
    }

    await page.locator('.workspace-pane.utility-pane', { hasText: 'Shell 1' }).click();
    await expect
      .poll(
        async () =>
          page.evaluate(
            (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
            's-real-pty-three'
          ),
        { timeout: 5000 }
      )
      .toBe(shell1PtyID);
    await page.keyboard.type('echo __PW_SHELL1_BACK__');
    await page.keyboard.press('Enter');
    await expect
      .poll(
        async () => {
          try {
            const scrollback = await readUtilityScrollback(wsUrl, shell1PtyID);
            return scrollback.includes('__PW_SHELL1_BACK__');
          } catch {
            return false;
          }
        },
        { timeout: 10000, intervals: [300, 500, 800, 1200, 1800] }
      )
      .toBe(true);

    await page.locator('.workspace-pane.utility-pane', { hasText: 'Shell 2' }).click();
    await expect
      .poll(
        async () =>
          page.evaluate(
            (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
            's-real-pty-three'
          ),
        { timeout: 5000 }
      )
      .toBe(shell2PtyID);
    await page.keyboard.type('echo __PW_SHELL2_BACK__');
    await page.keyboard.press('Enter');
    await expect
      .poll(
        async () => {
          try {
            const scrollback = await readUtilityScrollback(wsUrl, shell2PtyID);
            return scrollback.includes('__PW_SHELL2_BACK__');
          } catch {
            return false;
          }
        },
        { timeout: 10000, intervals: [300, 500, 800, 1200, 1800] }
      )
      .toBe(true);
  });

  test('main session keeps keyboard interactivity after returning from a split', async ({ page, daemon }) => {
    test.skip(!realPtyEnabled, 'Requires real PTY (VITE_FORCE_REAL_PTY=1)');

    const { wsUrl } = await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await createSession(page, daemon, {
      id: 's-real-pty-main',
      label: 'main',
      agent: 'claude',
      state: 'working',
      cwd: '/Users/victor.arias/projects/victor/attn',
    });
    await page.locator('[data-testid="session-s-real-pty-main"]').click();
    await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 5000 });
    await page.locator('.workspace-pane.main-pane').click();
    await page.waitForTimeout(120);

    const initialInputEvents = await getSessionInputEvents(page, 's-real-pty-main');
    await page.keyboard.type('__PW_MAIN_BEFORE_SPLIT__');
    await expect
      .poll(
        async () => (await getSessionInputEvents(page, 's-real-pty-main')).length,
        { timeout: 5000, intervals: [300, 500, 800, 1200] }
      )
      .toBeGreaterThan(initialInputEvents.length);
    console.log('[main-before-text]', JSON.stringify(await getMainTerminalText(page, 's-real-pty-main')));

    await sendWorkspaceSplitPane(wsUrl, 's-real-pty-main', 'main', 'vertical');
    await expect
      .poll(
        async () =>
          page.evaluate(
            (sessionID) => window.__TEST_GET_ACTIVE_UTILITY_PTY?.(sessionID) ?? null,
            's-real-pty-main'
          ),
        { timeout: 5000 }
      )
      .not.toBeNull();

    const afterSplitInputEvents = await getSessionInputEvents(page, 's-real-pty-main');

    await page.locator('.workspace-pane.main-pane').click();
    await page.waitForTimeout(120);
    await page.keyboard.type('__PW_MAIN_BACK__');
    console.log('[main-after-text]', JSON.stringify(await getMainTerminalText(page, 's-real-pty-main')));

    await expect
      .poll(
        async () => {
          const events = await getSessionInputEvents(page, 's-real-pty-main');
          console.log('[main-split-debug]', JSON.stringify(events));
          return events.length;
        },
        { timeout: 5000, intervals: [300, 500, 800, 1200] }
      )
      .toBeGreaterThan(afterSplitInputEvents.length);
  });
});
