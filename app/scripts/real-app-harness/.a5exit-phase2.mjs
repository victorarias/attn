#!/usr/bin/env node
// A5 exit proof, phase 2: `attn app dev` is watching; edit the view, watch the
// tile remount on the new version with the edit visible.
import { readFileSync, writeFileSync } from 'fs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const delay = (ms) => new Promise((r) => setTimeout(r, ms));
const VIEW = '/tmp/a5exit-proof/src/views/Sessions.tsx';
const SESSION = '24ddeb2a-f453-4667-8bac-7cf6f6c907bc';
const WORKSPACE = `workspace-${SESSION}`;

async function main() {
  const client = new UiAutomationClient();
  await client.waitForManifest(20_000);
  await client.waitForFrontendResponsive(20_000, 'get_state');

  await delay(3000); // let the recording show the tile as it is
  const before = readFileSync(VIEW, 'utf8');
  writeFileSync(VIEW, before.replace('· seq ${doc.body.seq}', '· seq ${doc.body.seq} · v2'));
  console.log('edited view (added · v2)');

  for (let i = 0; i < 20; i++) {
    await delay(500);
    const st = await client.request('app_view_get_state', { workspaceId: WORKSPACE });
    const host = st.hosts?.find((h) => h.view === 'exit-proof/sessions');
    if (host && !host.placeholder && host.text.includes('· v2')) {
      console.log('remounted with the edit visible');
      await delay(4000);
      return;
    }
  }
  throw new Error('view did not remount with the edit');
}

main().catch((e) => { console.error(e); process.exitCode = 1; });
