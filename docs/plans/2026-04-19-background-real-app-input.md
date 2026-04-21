---
name: Background real-app input (no focus steal)
description: Prove out a focus-free input path for the packaged-app harness by porting the Codex/Understudy AX-based approach, with a synthetic focus-probe scenario and one converted smoke test.
type: plan
status: Done
---

# Background real-app input (no focus steal)

**Status**: Done
**Owner**: Victor
**Date**: 2026-04-19

## Why

The packaged-app harness uses two input paths today:

1. **Focus-free** — `UiAutomationClient` → Tauri TCP socket (`app/src-tauri/src/ui_automation.rs`) → `useUiAutomationBridge.ts` dispatches shortcuts, focuses panes, runs `type_pane_via_ui`, reads pane text. Everything it does stays inside the renderer; nothing touches macOS focus.
2. **Focus-stealing** — `InputDriver.swift` + `macosDriver.mjs` (`app/scripts/real-app-harness/`). Calls `NSRunningApplication.activate(options: [.activateIgnoringOtherApps])` before every action, then fires `CGEvent.post(tap: .cghidEventTap)`. That's the exact pattern Understudy ships and that OpenAI's Codex CUA inherits — it works against any app but yanks the user's frontmost app away.

The focus-stealer is load-bearing only where the bridge can't reach:
- app activation + deep-link open in `smoke.mjs:29` and `scenario-tr502-remote-relaunch-splits.mjs:111`
- window-chrome keyboard paths (e.g. AppKit menu equivalents) in `repro-older-pane-writability.mjs:35`

Goal: close that gap so developers can keep using the machine while real-app scenarios run — specifically the `pnpm --dir app run real-app:serial-matrix` loop called out in `AGENTS.md`.

## What the Codex/Understudy stack actually does

Confirmed from `understudy-ai/understudy/packages/gui/src/native-helper.ts:1-813` and the codex-cua-mcp README (`Parassharmaa/codex-cua-mcp`):

- Short-lived Swift helper compiled from inline source at install time, invoked per action via env vars.
- `CGWindowListCopyWindowInfo` + ranking for window discovery, `ScreenCaptureKit` for screenshots (the public Codex build uses `SCContentFilter(desktopIndependentWindow:)` to capture a specific window-id, which works even when occluded).
- Two actuation surfaces:
  - **CGEvent via `cghidEventTap`** — the Understudy open-source path. Requires `app.activate([.activateIgnoringOtherApps])` first; that's the focus steal.
  - **AX actions** — Codex's shipping path. `get_app_state` returns an AX tree with numbered element indices; tool contract *prefers* `click(app, element_index=…)` over `click(app, x, y)`. Indexed click routes through `AXUIElementPerformAction(kAXPressAction)`; text input through `AXUIElementSetAttributeValue(kAXValueAttribute, …)`. These hit the target process's AX server directly — no HID event tap, no focus switch, no cursor move.
- Why Codex refuses `Terminal.app` / `iTerm2.app`: their AX trees are stubs, so the tool would fall back to CGEvent — unsafe there.

Implication for attn: our Tauri webview's AX surface is also weak (WebKit exposes a minimal AX tree for the web content). So the AX path is plausible for **window-chrome and menu-level** actions but not for typing into the webview — which is fine, because we already have `type_pane_via_ui` for the webview side.

## Scope

**In scope**
- Add an AX-based sibling to `InputDriver.swift` that performs `activate`, `key` (window-level shortcuts), and `menu_item` without activating the app.
- Replace the `MacOSDriver.activateApp()` CGEvent path with an AX-only variant when the caller opts in.
- Build a new synthetic scenario that measures focus preservation directly.
- Convert `smoke.mjs` as the smallest consumer, as a forcing function.

**Out of scope**
- Changing `useUiAutomationBridge.ts` or the Tauri TCP socket. Those already don't steal focus.
- Touching `scenario-tr502` or `repro-older-pane-writability` until the smoke conversion is green.
- Screenshot-based grounding. We don't need LLM grounding — the targets are fixed (attn windows, fixed menu items).

## Design

### New Swift entry points

Add to `app/scripts/real-app-harness/InputDriver.swift`:

```swift
// Non-activating app resolution — no frontmost change.
func attnApp(bundleId: String) throws -> (pid: pid_t, ax: AXUIElement) {
    guard let running = NSRunningApplication.runningApplications(withBundleIdentifier: bundleId).first else {
        throw DriverError.appNotRunning(bundleId)
    }
    return (running.processIdentifier, AXUIElementCreateApplication(running.processIdentifier))
}

// activate_background: NSRunningApplication.activate(.activateAllWindows) is out.
// Use the app's AX focused-window attribute to set focus without cross-app activation.
// (Fallback: no-op — attn is already running and the target pane is already selected.)
func axActivateBackground(bundleId: String) throws { /* verify pid, done */ }

// Menu-item dispatch: walk AXMenuBar → AXMenuBarItem → AXMenuItem by title, press it.
func axPressMenuItem(bundleId: String, path: [String]) throws { /* AXUIElementPerformAction(kAXPressAction) */ }
```

New CLI subcommands:
- `activate_background --bundle-id com.attn.manager` — replaces `activate` for callers that don't need true OS-level frontmost.
- `menu --bundle-id com.attn.manager --path "File>New Session"` — replaces `key --key n --modifiers command` style calls for menu-mapped shortcuts.

Leave the existing `activate`, `text`, `key`, `click` subcommands alone. They stay as the last-resort HID path.

### `MacOSDriver` additions

In `app/scripts/real-app-harness/macosDriver.mjs`:

```js
// Default to background mode; opt out only when the caller explicitly wants a focus pull.
async activateBackground() {
  await this.runInputDriver(['activate_background']);
}
async menu(pathSegments) {
  await this.runInputDriver(['menu', '--path', pathSegments.join('>')]);
}
```

### Synthetic focus-probe scenario

New file `app/scripts/real-app-harness/scenario-focus-probe.mjs`:

1. Record the caller's frontmost app via a tiny helper `frontmostBundleId()` (osascript one-liner or Swift helper returning `NSWorkspace.shared.frontmostApplication?.bundleIdentifier`).
2. Launch attn via `launchFreshAppAndConnect`.
3. **Record frontmost again** — expect it to be attn right after launch (baseline).
4. Focus a terminal app (`osascript -e 'tell app "Terminal" to activate'`) to simulate "user is working in another app".
5. Record frontmost — expect `com.apple.Terminal`.
6. Run the same action twice, once per driver mode:
   - **A) Current path** — `driver.activateApp()` then `driver.pressKey('t', {command: true})`.
   - **B) New path** — `driver.activateBackground()` then `driver.menu(['View', 'Utility Terminal'])` *or* a bridge-only `dispatch_shortcut` call.
7. After each, assert via bridge that a new utility pane was created (`get_workspace`), **and** record frontmost.
   - Expected A: frontmost flipped to `com.attn.manager` — regression evidence.
   - Expected B: frontmost stayed `com.apple.Terminal` — success.
8. Write a JSON artifact `{mode, frontmost_before, frontmost_after, pane_created}` to `scenarioRunner`'s run dir for regression tracking.

This is the validation test. It answers "does the new path preserve focus?" in one run, and it'll keep answering forever as a regression probe.

### Conversion candidate: `smoke.mjs`

Smallest existing consumer (86 lines). Current shape:

```js
const driver = new MacOSDriver({ appPath: options.appPath });
await driver.launchApp();
// ...
await driver.activateApp();
await driver.typeText('attn smoke');
await driver.pressEnter();
```

Target shape:

```js
const driver = new MacOSDriver({ appPath: options.appPath });
const client = new UiAutomationClient({ appPath: options.appPath });
await driver.launchApp();                    // still needs `open -a` — that's activation-free
await client.waitForReady();
// No activateApp(). Drive typing via the bridge.
await client.request('write_pane', { sessionId, text: 'attn smoke', submit: true });
```

If `smoke.mjs` still needs a genuine window-level keyboard shortcut for its flow, it uses `driver.menu(...)` (new AX path), not `driver.pressKey(...)`.

Success criterion for conversion: `scenario-focus-probe` run immediately after `smoke` shows `frontmost_after === com.apple.Terminal`.

## Risks / things that can kill the approach

- **AX menu-walk is flaky on Electron/Tauri.** Tauri wraps a native AppKit window, so top-level menus *are* native — AX menu traversal should work. Webview content is a different story but we don't target that.
- **`activate_background` is a semantic no-op** unless we also teach the bridge's `dispatch_shortcut` and `focus_pane` to do more heavy lifting. Likely fine; bridge already handles pane focus inside the webview.
- **Accessibility prompt fatigue.** The existing driver already triggers the prompt; AX read is covered by the same entitlement. No new permission surface.
- **AppKit global shortcuts that don't map to menu items** (e.g. custom ones registered via `NSEvent.addLocalMonitorForEvents`) can't be hit via menu-walk. If `smoke.mjs` needs one of those, we fall back to a third path: a Tauri test-only command that calls the same internal handler the global shortcut would. Strictly better than CGEvent because no focus change.

## Milestones

- **M1 (half day)** — `scenario-focus-probe.mjs` against the *current* driver. Captures the regression, proves the measurement loop works. No new Swift code yet.
- **M2 (half day)** — Add `activate_background` + `menu` subcommands to `InputDriver.swift`; extend `MacOSDriver`. Probe scenario runs both modes, shows the delta.
- **M3 (half day)** — Convert `smoke.mjs`. Run `scenario-focus-probe` + `smoke` back-to-back and confirm focus stays on the caller's terminal for the whole run.
- **M4 (optional)** — Migrate `scenario-tr502` and `repro-older-pane-writability`. Only after M3 holds up across several runs.

## Done when

- `scenario-focus-probe` asserts `frontmost_after === frontmost_before` on the new path and fails fast on the old path.
- `pnpm run real-app:smoke` completes without the caller's frontmost app changing.
- CHANGELOG.md has an entry under a dated section describing "background-safe real-app harness" — user-visible because contributors benefit.
- This plan's status flips to Done in the same PR that lands M3.

## Non-goals (explicit)

- Not adopting Codex's screenshot grounding loop. We have structured bridge state; pixel grounding is the wrong tool here.
- Not replacing Playwright e2e specs. Those already run in a Vite-hosted Chromium and don't pull focus.
