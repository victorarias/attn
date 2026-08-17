#!/usr/bin/env node
// Undock a tile from a workspace over the WebSocket, the way the app does.
import WebSocket from 'ws';
import { harnessClientHello, defaultDaemonPortForProfile } from './harnessProfile.mjs';

const workspaceId = process.argv[2];
const tileId = process.argv[3];

const ws = new WebSocket(`ws://127.0.0.1:${defaultDaemonPortForProfile()}/ws`);
ws.on('open', () => {
  ws.send(JSON.stringify(harnessClientHello('a5exit-undock')));
  ws.send(JSON.stringify({
    cmd: 'workspace_layout_undock_tile',
    workspace_id: workspaceId,
    tile_id: tileId,
  }));
});
ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  if (msg.event === 'workspace_layout_action_result' || msg.event === 'error') {
    console.log(JSON.stringify(msg));
    ws.close();
    process.exit(0);
  }
});
setTimeout(() => { console.error('timeout'); process.exit(1); }, 8000);
