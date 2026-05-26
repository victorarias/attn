#!/usr/bin/env node

import { UiAutomationClient } from './uiAutomationClient.mjs';
import { manifestPathForNativeProfile } from './nativeHarnessProfile.mjs';

function printHelp() {
  console.log(`Usage: node scripts/real-app-harness/native-ui-automation-cli.mjs <action> [json-payload]

The native client must already be running through \`make dev-native\`.

Actions:
  ping
  get_state
  list_panes
  select_workspace              JSON payload: {"workspace_id":"workspace-id"}
  navigate                      JSON payload: {"direction":"left"}
  open_new_workspace_dialog
  open_add_pane_dialog          JSON payload: {"direction":"vertical"}
  quick_split                   JSON payload: {"direction":"vertical"}
  get_launcher_state
  set_launcher_path             JSON payload: {"path":"/tmp/workspace"}
  set_launcher_choice           JSON payload: {"choice":"terminal","yolo":false}
  perform_launcher_action       JSON payload: {"action":"move_location_down"}
  submit_launcher_location
  choose_launcher_destination   JSON payload: {"path":"/path/to/worktree"}
  cancel_launcher
  close_selected_content
  close_window
  tail_events                  JSON payload: {"since_id":0}
  get_window_bounds
  set_window_background_mode   JSON payload: {"enabled":true}
  park_window                  JSON payload: {"visible_px":20}
  screenshot                   JSON payload: {"path":"/tmp/attn-native.png"}
  screenshot_window            JSON payload: {"path":"/tmp/attn-native.png"}
  focus_pane                   JSON payload: {"runtime_id":"runtime-id","key_window":false}
  type_terminal                JSON payload: {"runtime_id":"runtime-id","text":"printf ok\\\\r"}
  press_terminal_enter         JSON payload: {"runtime_id":"runtime-id"}
  copy_terminal_selection      JSON payload: {"runtime_id":"runtime-id"}
  paste_terminal_clipboard     JSON payload: {"runtime_id":"runtime-id"}
  read_pane_text               JSON payload: {"runtime_id":"runtime-id"}
  read_terminal_selection      JSON payload: {"runtime_id":"runtime-id"}
  move_terminal_pointer        JSON payload: {"runtime_id":"runtime-id","column":0,"row":0}
  click_terminal_cell          JSON payload: {"runtime_id":"runtime-id","column":0,"row":0}
  drag_terminal_selection      JSON payload: {"runtime_id":"runtime-id","start_column":0,"start_row":0,"end_column":4,"end_row":0}
  get_surface_geometry         JSON payload: {"runtime_id":"runtime-id"}
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

  const result = await client.request(action, payload);
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
