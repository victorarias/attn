#!/usr/bin/env node

import { UiAutomationClient } from './uiAutomationClient.mjs';
import { manifestPathForNativeProfile } from './nativeHarnessProfile.mjs';
import { getProcessWindowId } from './nativeWindowCapture.mjs';

function printHelp() {
  console.log(`Usage: node scripts/real-app-harness/native-ui-automation-cli.mjs <action> [json-payload]

The native client must already be running through \`make dev-native\`.

Actions:
  ping
  get_state
  tail_events                  JSON payload: {"since_id":0}
  get_window_bounds
  create_workspace             JSON payload: {"id":"...","title":"...","directory":"/tmp"}
  spawn_session                JSON payload: {"id":"...","workspace_id":"...","cwd":"/tmp","agent":"pi","executable":"/bin/sh"}
  destroy_workspace            JSON payload: {"id":"..."}
  kill_runtime                 JSON payload: {"runtime_id":"..."}
  select_workspace             JSON payload: {"workspace_id":"..."}
  focus_pane                   JSON payload: {"workspace_id":"...","pane_id":"..."}
  split_pane                   JSON payload: {"workspaceId":"...","targetPaneId":"...","direction":"vertical"}
  mute_session                 JSON payload: {"session_id":"..."}
  close_pane                   JSON payload: {"workspace_id":"...","pane_id":"..."}
  write_pane                   JSON payload: {"workspaceId":"...","paneId":"...","text":"\\r","submit":false}
  type_pane_via_ui             JSON payload: {"workspaceId":"...","paneId":"...","text":"echo ready"} (requires prior focus_pane)
  read_pane_text               JSON payload: {"workspaceId":"...","paneId":"..."}
  capture_structured_snapshot  JSON payload: {"includePaneText":true}
  capture_render_health
  screenshot                   JSON payload: {"path":"/tmp/attn-native.png"}
`);
}

async function main() {
  const args = [...process.argv.slice(2)];
  if (args[0] === '--') args.shift();
  if (args[0] === '--help' || args[0] === '-h' || !args[0]) {
    printHelp();
    return;
  }

  const action = args.shift();
  const payload = args[0] ? JSON.parse(args[0]) : {};
  const explicitProfile = (process.env.ATTN_PROFILE || '').trim();
  const client = new UiAutomationClient({
    manifestPath: manifestPathForNativeProfile(explicitProfile || 'dev'),
  });
  await client.waitForManifest(20_000);
  await client.waitForReady(20_000);

  const requestPayload = action === 'screenshot'
    ? {
        ...payload,
        windowId: await getProcessWindowId(client.readManifest().pid),
      }
    : payload;
  const result = await client.request(action, requestPayload);
  console.log(JSON.stringify(result, null, 2));
}

main().catch((error) => {
  const message = error instanceof Error ? error.stack || error.message : String(error);
  console.error(message);
  if (message.includes('could not create image')) {
    console.error(
      'Hint: grant Screen Recording permission to the signed attn-native-dev.app bundle, then retry the native screenshot.',
    );
  }
  process.exitCode = 1;
});
