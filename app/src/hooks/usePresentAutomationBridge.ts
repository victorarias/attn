import { useEffect } from 'react';
import { emit, listen } from '@tauri-apps/api/event';
import { isTauri } from '@tauri-apps/api/core';
import { getCurrentWindow } from '@tauri-apps/api/window';

// Mirrors the constants in useUiAutomationBridge.ts. The Rust side
// (ui_automation.rs) broadcasts these same event names to EVERY webview
// window, so both bridges must agree on the wire strings even though the two
// hooks live in separate modules and are mounted in separate windows.
const UI_AUTOMATION_REQUEST_EVENT = 'attn://ui-automation/request';
const UI_AUTOMATION_RESPONSE_EVENT = 'attn://ui-automation/response';
const UI_AUTOMATION_READY_EVENT = 'attn://ui-automation/ready';

const PRESENT_WINDOW_ACTION_PREFIX = 'present_window_';

// Routing predicate shared (by value, not import — see useUiAutomationBridge's
// guard) across both bridge listeners. The Rust automation server broadcasts
// every request to ALL webview windows and resolves on the FIRST response
// with a matching request_id, so exactly one of the two listeners must answer
// any given request. Routing is by action-name prefix; there is no
// window/label field on the protocol request.
export function isPresentWindowAction(action: string): boolean {
  return action.startsWith(PRESENT_WINDOW_ACTION_PREFIX);
}

interface AutomationRequest {
  request_id: string;
  action: string;
  payload?: Record<string, unknown> | null;
}

interface AutomationResponse {
  request_id: string;
  ok: boolean;
  result?: unknown;
  error?: string;
}

function nextAnimationFrame(): Promise<void> {
  return new Promise((resolve) => requestAnimationFrame(() => resolve()));
}

// The submit dialog mounts on a React state update triggered by the header
// submit button's onClick, which is not synchronous with the click dispatch
// in present_window_submit below — poll a few animation frames for it,
// capped at ~1s.
async function waitForSubmitDialog(timeoutMs = 1_000): Promise<HTMLElement> {
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    const dialog = document.querySelector<HTMLElement>('.present-root-submit-dialog');
    if (dialog) return dialog;
    await nextAnimationFrame();
  }
  throw new Error('present_window_submit: submit dialog did not appear');
}

async function handlePresentWindowAction(action: string): Promise<unknown> {
  switch (action) {
    case 'present_window_is_visible': {
      return { visible: await getCurrentWindow().isVisible() };
    }
    case 'present_window_submit': {
      // Drive the real DOM submit flow end to end: click the round-header
      // submit button, wait for the confirm dialog, then click its confirm
      // button (the second/last button in .present-root-submit-actions — the
      // first is Cancel). Zero draft comments is a valid submit; nothing here
      // requires any drafts to exist.
      const submitButton = document.querySelector<HTMLElement>('.present-root-submit-button');
      if (!submitButton) {
        throw new Error('present_window_submit: submit button not found');
      }
      submitButton.click();

      const dialog = await waitForSubmitDialog();
      const actions = dialog.querySelector<HTMLElement>('.present-root-submit-actions');
      const buttons = actions ? Array.from(actions.querySelectorAll<HTMLButtonElement>('button')) : [];
      const confirmButton = buttons[buttons.length - 1];
      if (!confirmButton) {
        throw new Error('present_window_submit: confirm button not found in submit dialog');
      }
      confirmButton.click();
      return { submitted: true };
    }
    default:
      throw new Error(`Unknown present-window automation action: ${action}`);
  }
}

/**
 * The present window's half of the UI automation bridge. Mirrors the
 * transport half of useUiAutomationBridge (event names, the
 * `__ATTN_AUTOMATION_ENABLED` gate, the `isTauri()` check) but answers its
 * own small action set (`present_window_is_visible`, `present_window_submit`)
 * instead of the main window's giant handler.
 *
 * Routing: the Rust automation server (ui_automation.rs) broadcasts every
 * request to ALL webview windows and resolves on the first matching
 * response. This hook only answers `present_window_*` actions (see
 * isPresentWindowAction) and returns without emitting a response for
 * anything else, so the main window's bridge is free to answer everything
 * else without a race.
 */
export function usePresentAutomationBridge(): void {
  useEffect(() => {
    const automationEnabled =
      typeof window !== 'undefined' &&
      (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED === true;
    if (!isTauri() || !automationEnabled) {
      return;
    }

    void emit(UI_AUTOMATION_READY_EVENT, { ready: true });
    const unlistenPromise = listen<AutomationRequest>(UI_AUTOMATION_REQUEST_EVENT, async (event) => {
      const request = event.payload;
      if (!isPresentWindowAction(request.action)) {
        // Not ours — the main window's bridge answers this one.
        return;
      }

      let response: AutomationResponse;
      try {
        const result = await handlePresentWindowAction(request.action);
        response = { request_id: request.request_id, ok: true, result };
      } catch (error) {
        response = {
          request_id: request.request_id,
          ok: false,
          error: error instanceof Error ? error.message : String(error),
        };
      }
      await emit(UI_AUTOMATION_RESPONSE_EVENT, response);
    });

    return () => {
      void unlistenPromise.then((unlisten) => unlisten());
    };
  }, []);
}
