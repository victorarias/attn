import { test, expect } from './fixtures';

const realPtyEnabled = process.env.VITE_FORCE_REAL_PTY === '1';

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
    await expect(page.locator('.utility-terminal-panel')).toBeVisible({ timeout: 5000 });

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

    await page.locator('.utility-terminal-wrapper.active .terminal-container').click();
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

    await page.locator('.utility-terminal-wrapper.active .terminal-container').click();
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

    await page.keyboard.press('Meta+d');
    await expect(page.locator('.dashboard')).toBeVisible({ timeout: 5000 });

    await page.locator('[data-testid="session-s-real-pty-d"]').click();
    await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('.utility-terminal-panel')).toBeVisible({ timeout: 5000 });

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
});
