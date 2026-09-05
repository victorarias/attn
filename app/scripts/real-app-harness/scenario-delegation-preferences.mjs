#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, profileCliEnv, socketPathForProfile } from './harnessProfile.mjs';
import { appDaemonInTree, delay } from './platform.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';
import { captureWebKitPids, readLiveDaemonPid, readProcessTable, snapshot, readAppFootprint } from './perfMeasure.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const profile = currentHarnessProfile();
if (!profile) throw new Error('Delegation preferences verification requires a named profile');
const runner = createScenarioRunner(options, { scenarioId: 'DelegationPreferences', tier: 'local', prefix: 'delegation-preferences', allowRealAgents: false });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const root = '[data-testid="delegation-settings"]';
const runAttn = args => execFileSync(appDaemonInTree(options.appPath), args, { encoding: 'utf8', env: profileCliEnv(profile, { ATTN_SOCKET_PATH: socketPathForProfile(profile) }) });
const roles = () => JSON.parse(runAttn(['delegate', 'roles', '--json']));
const click = selector => client.request('dom_click', { selector });
const type = (selector, text) => client.request('dom_type', { selector, text });
const select = (selector, value) => client.request('dom_select', { selector, value });
const text = async () => (await client.request('dom_text', { selector: root })).text;
const hold = () => process.env.ATTN_HARNESS_RECORD === '1' ? delay(1200) : Promise.resolve();
async function until(check, description) {
  for (let i = 0; i < 100; i++) { const found = await check(); if (found) return found; await delay(100); }
  throw new Error(`Timed out: ${description}`);
}
async function save() {
  await click('[data-testid="delegation-save"]');
  await until(async () => !(await text()).includes('Unsaved changes') && !(await text()).includes('Saving…'), 'preferences saved');
}
async function screenshot(name) { await captureScreenshotData(path.join(runner.runDir, name), { client }); }
function preferencesRequest(cmd, preferences) {
  const request_id = crypto.randomUUID();
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => { observer.ws?.off('message', receive); reject(new Error(`${cmd} timed out`)); }, 10000);
    const receive = raw => {
      const value = JSON.parse(String(raw));
      if (value.request_id !== request_id) return;
      clearTimeout(timeout); observer.ws.off('message', receive);
      if (value.success) resolve(value); else reject(new Error(value.error));
    };
    observer.ws.on('message', receive);
    observer.ws.send(JSON.stringify({ cmd, request_id, preferences }));
  });
}
let source, worker, baseline;
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
try {
  const webkitBaseline = await captureWebKitPids();
  await launchFreshAppAndConnect(client, observer);
  const initial = await preferencesRequest('delegation_preferences_get');
  baseline = initial.preferences;
  if (baseline.revision !== 0) {
    await preferencesRequest('delegation_preferences_save', { ...baseline, enabled: false, roles: initial.templates, fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } });
  }
  await runner.step('disabled_by_default', async () => {
    await client.request('dismiss_whats_new');
    await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
    await client.request('settings_select_section', { sectionId: 'delegation' });
    await until(async () => (await text()).includes('Your existing setup stays yours.'), 'disabled preferences');
    runner.assert(roles().roles.length === 0, 'roles lookup is empty before opt-in');
    await screenshot('01-disabled.png'); await hold();
  });
  await runner.step('configure_starter_roles', async () => {
    await click(`${root} .delegation-switch input`);
    await until(async () => (await text()).includes('Start with one model'), 'starter setup');
    await select('.delegation-starter .delegation-fields > label:nth-child(1) select', 'codex');
    await click('[data-testid="delegation-apply-starter"]');
    await save();
    const found = roles();
    runner.assert(found.roles.length === 5 && found.roles.every(r => r.choices[0].selection.harness === 'codex'), 'one starter selection fills five roles');
    await screenshot('02-roles.png'); await hold();
  });
  await runner.step('edit_role_and_add_effort_alternative', async () => {
    await click('[aria-label="Edit Build"]');
    await type('.delegation-behavior label:nth-of-type(1) textarea', 'Verify the change and preserve {{literal}} in the fixture.');
    await type('.delegation-choice-body .delegation-picker input[list]', 'medium');
    await click('[data-testid="delegation-add-choice"]');
    await type('.delegation-choice-body > label input', 'Difficult verification');
    await type('.delegation-choice-body > label textarea', 'Verification is difficult or the requirements are ambiguous.');
    await type('.delegation-choice-body .delegation-picker input[list]', 'high');
    await save();
    const build = roles().roles.find(r => r.id === 'build');
    runner.assert(build.choices.length === 2 && build.choices[1].selection.effort === 'high', 'alternative retains its native effort');
    runner.assert(build.instructions.includes('{{literal}}'), 'user guidance stays literal');
    await screenshot('03-role-editor.png'); await hold();
    await client.request('dom_scroll_into_view', { selector: '.delegation-choice-body' });
    await screenshot('03-model-choices.png'); await hold();
  });
  await runner.step('custom_role_can_be_deleted_and_restored', async () => {
    await click('.delegation-tabs button:first-child');
    await click(`${root} .delegation-content > .delegation-row.between button`);
    await type('.delegation-role-heading input', 'Debug');
    await select('.delegation-choice-body .delegation-fields > label:first-child select', 'copilot');
    await save();
    runner.assert(roles().roles.some(r => r.name === 'Debug' && r.choices[0].selection.harness === 'copilot'), 'custom role keeps Copilot default');
    await click('.delegation-content > .delegation-row.between .danger');
    await click(`${root} > [role="status"] button`);
    await save();
    runner.assert(roles().roles.some(r => r.name === 'Debug'), 'Undo restores a deleted role');
    await screenshot('04-custom-role.png'); await hold();
  });
  const before = roles();
  await runner.step('request_override_reaches_visible_session', async () => {
    await click('[data-testid="settings-close"]');
    fs.mkdirSync(runner.sessionDir, { recursive: true });
    source = await createSessionAndWaitForInitialPane({ client, observer, cwd: runner.sessionDir, label: 'Delegation source', agent: 'shell', sessionWaitMs: 30000 });
    const output = runAttn(['delegate', '--source-session', source, '--no-worktree', '--role', 'build', '--preferences-revision', String(before.revision), '--effort', 'high', '--brief', 'Delegation settings verification. Wait for direction.', '--name', 'Build check']);
    const result = JSON.parse(output.slice(output.indexOf('{')));
    worker = result.session_id;
    await observer.waitFor(() => observer.sessionsById.has(worker), 'visible delegated session');
    runner.assert(roles().roles.find(r => r.id === 'build').choices[0].selection.effort === 'medium', 'request effort does not mutate the role default');
    runner.writeText('delegation-result.json', JSON.stringify(result, null, 2));
    await hold();
  });
  await runner.step('disable_hides_roles_and_reenable_restores_them', async () => {
    await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
    await client.request('settings_select_section', { sectionId: 'delegation' });
    await click(`${root} .delegation-switch input`);
    await until(() => roles().roles.length === 0, 'roles hidden');
    await hold();
    await click(`${root} .delegation-switch input`);
    await until(() => roles().roles.length === 6, 'saved roles restored');
    runner.writeJson('roles.json', roles());
    await screenshot('05-restored.png'); await hold();
  });
  await runner.step('idle_preferences_page', async () => {
    for (const sessionId of [worker, source].filter(Boolean)) await client.request('close_session', { sessionId });
    await delay(3000);
    const appPid = client.readManifest().pid;
    const daemonPid = readLiveDaemonPid(profile);
    const before = await snapshot(appPid, daemonPid, webkitBaseline);
    const pids = new Set(Object.values(before.byClass).flatMap(value => value.pids.map(value => value.pid)));
    const samples = [];
    for (let i = 0; i < 10; i++) {
      await delay(1000);
      samples.push((await readProcessTable()).filter(process => pids.has(process.pid)));
    }
    const after = await snapshot(appPid, daemonPid, webkitBaseline);
    runner.writeJson('idle.json', { before, after, samples, footprint: await readAppFootprint(after) });
  });
  console.log(JSON.stringify(await runner.finishSuccess({ source, worker }), null, 2));
} catch (error) {
  await screenshot('failure.png').catch(() => {});
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
} finally {
  if (baseline && observer.connected) {
    try {
      const current = await preferencesRequest('delegation_preferences_get');
      await preferencesRequest('delegation_preferences_save', { ...baseline, revision: current.preferences.revision });
    } catch (error) { console.error(`Restore preferences: ${error.message}`); process.exitCode = 1; }
  }
  for (const sessionId of [worker, source].filter(Boolean)) { await client.request('close_session', { sessionId }).catch(() => {}); }
  await observer.close().catch(() => {});
  await client.quitApp().catch(() => {});
}
