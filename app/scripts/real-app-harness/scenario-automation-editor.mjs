#!/usr/bin/env node

/**
 * Packaged-app proof for Slice 7 PR B's self-service automation YAML editor:
 * the starter template, invalid-YAML rejection (nothing stored), a real
 * create with a hand-written comment, the create-collision refusal, D1
 * (comments survive a save/reload round-trip), the id-change refusal (D4),
 * the stale-revision refusal plus Reload recovery (D5), a comment-only edit
 * that still bumps the revision, a Save that is refused rather than
 * silently resurrecting a definition deleted elsewhere, and a panel
 * toggle-off that survives a later unrelated edit — the last three pin the
 * three multi-writer races fixed in 649a7e52 and 09cfb0b6 (spec_yaml missing
 * from the revision-bump condition; DeleteAutomationDefinition leaving
 * revision untouched; SetAutomationEnabled writing only the enabled column
 * and leaving the stored spec saying otherwise) — all driven through the
 * real rendered editor and panel via the automation_editor_* and
 * automations_* UI-automation bridge verbs, cross-checked against the
 * daemon's own state via the bundled `attn` CLI (`automation show`/`list`),
 * not just the DOM.
 *
 * One definition id is reused across most legs (`automation-editor-<suffix>`)
 * so the id-change and stale-revision legs exercise the SAME live definition
 * D1 just proved comment-preservation on, rather than fresh throwaway ids —
 * matching how a real user edits one automation repeatedly in one sitting.
 * The same id carries through the comment-only-revision-bump leg and finally
 * into the delete-elsewhere leg, which ends the scenario with it soft-
 * deleted on purpose. A second id (`automation-editor-renamed-<suffix>`)
 * exists only as the (refused) target of the id-change leg; it must never
 * actually be created.
 *
 * No fake-agent probe is needed: this scenario never triggers a run. Every
 * definition it applies is manual-trigger, and all but leg10's are also
 * enabled: false, so `launch.executable` is never invoked and can stay unset
 * like the starter template's. leg10 must start from an enabled definition in
 * order to disable it from the panel; being manual-trigger, it still never
 * fires, and the leg deletes it before finishing.
 *
 * Run serially (packaged-app scenarios are single-tenant):
 *   ATTN_HARNESS_PROFILE=<name> node scripts/real-app-harness/scenario-automation-editor.mjs
 */

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { parseCommonArgs, printCommonHelp, launchFreshAppAndConnect } from './common.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, resolveHarnessResources } from './harnessProfile.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
import { readFrontendProtocolVersion } from './presentDaemon.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const PANEL_TIMEOUT_MS = 30_000;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function profileEnv(profile, extra = {}) {
  const env = { ...process.env, ATTN_PROFILE: profile, ...extra };
  for (const key of ['ATTN_SOCKET_PATH', 'ATTN_DB_PATH', 'ATTN_CONFIG_PATH', 'ATTN_PLUGIN_DIR']) {
    delete env[key];
  }
  return env;
}

function run(binary, args, env, options = {}) {
  return execFileSync(binary, args, {
    encoding: 'utf8',
    env,
    stdio: options.stdio || ['ignore', 'pipe', 'pipe'],
    timeout: options.timeout || 30_000,
  });
}

function runJSON(binary, args, env) {
  return JSON.parse(run(binary, args, env));
}

// `attn automation show <id>` prints the canonical rendered YAML alone (no
// JSON wrapper — see cmd/attn/automation.go), unlike the pre-unification CLI
// which printed the raw store row (Enabled/Revision/SpecYAML/SpecJSON). Get
// the definition summary (id/enabled/revision) from `automation list`
// instead, filtered by id — the two together are the CLI-observable
// replacement for the old single `show` call.
function showSpecYAML(binary, id, env) {
  return run(binary, ['automation', 'show', id], env);
}

function findListRow(binary, id, env) {
  const list = runJSON(binary, ['automation', 'list'], env) || [];
  return list.find((row) => row.id === id) || null;
}

async function poll(fn, description, timeoutMs = 30_000) {
  const started = Date.now();
  let last = null;
  while (Date.now() - started < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(150);
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

async function waitForDaemonReady(binary, daemonEnv) {
  await poll(() => {
    try {
      runJSON(binary, ['automation', 'list'], daemonEnv);
      return { ready: true };
    } catch {
      return null;
    }
  }, 'profile daemon');
}

// Click the panel's real enable/disable toggle and wait for the daemon to
// come back. Two waits are needed and neither is optional:
//
//   - The row must be RENDERED before the click. A definition created through
//     the editor reaches the panel by broadcast, not synchronously, so
//     clicking straight after a save hits an element that does not exist yet.
//   - automations_toggle_enabled clicks and settles a few UI frames without
//     awaiting the daemon round-trip, so the panel is the thing to poll for
//     the new state; once it renders the flip, the store has already
//     committed it and a following CLI read is no longer a race.
//
// Editor saves need neither: they are request/result and are daemon-confirmed
// by the time they return. Same shape as scenario-automation-surface.mjs's
// toggle legs, which is where this pattern is house convention.
async function toggleEnabledAndWait(client, definitionId, wantEnabled) {
  await poll(async () => {
    const current = await client.request('automations_get_state');
    return findDefinitionRow(current, definitionId) ? current : null;
  }, `definition ${definitionId} to appear in the panel before toggling it`, PANEL_TIMEOUT_MS);

  await client.request('automations_toggle_enabled', { definitionId });

  return poll(async () => {
    const current = await client.request('automations_get_state');
    const row = findDefinitionRow(current, definitionId);
    return row && row.enabled === wantEnabled ? current : null;
  }, `definition ${definitionId} to render ${wantEnabled ? 'enabled' : 'disabled'} after the toggle broadcast`, PANEL_TIMEOUT_MS);
}

// --- automation definition YAML ----------------------------------------

const API_VERSION = 'attn.dev/automations/v1alpha1';

// Defaults to enabled: false — this scenario never triggers a run, and
// leaving these definitions disabled means the finally block does not need to
// disable-before-teardown like scenario-automation-lifecycle.mjs (an enabled
// directory-location definition re-validates its path on every future daemon
// tick, which would spam errors once the fixture dir is gone). leg10 is the
// one caller that opts into enabled: true, because it has to start from an
// enabled definition to disable it from the panel; it deletes that definition
// at the end of the leg for the same reason.
function editorDefinitionYAML({ id, locationPath, prompt, comment, enabled = false }) {
  const commentLine = comment ? `${comment}\n` : '';
  return `${commentLine}api_version: ${API_VERSION}
id: ${id}
name: Slice 7 packaged editor proof
enabled: ${enabled ? 'true' : 'false'}
trigger:
  type: manual
prompt: |
  ${prompt}
launch:
  driver: codex
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
`;
}

// Deliberately missing/wrong api_version — the cheapest reliable way to fail
// ValidateDefinition without depending on any other field's exact wording.
function invalidDefinitionYAML({ id, locationPath }) {
  return `api_version: not-a-real-api-version
id: ${id}
name: Slice 7 packaged editor proof (invalid)
enabled: false
trigger:
  type: manual
prompt: |
  This definition is deliberately invalid; it must never be stored.
launch:
  driver: codex
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
`;
}

function findDefinitionRow(state, definitionId) {
  return (state?.definitions || []).find((row) => row.id === definitionId) || null;
}

async function captureFailureEvidence(runner, client) {
  try {
    const state = await client.request('automations_get_state');
    runner.writeJson('failure-automations-state.json', state);
  } catch (error) {
    runner.log('failure_evidence_state_error', { error: error instanceof Error ? error.message : String(error) });
  }
  try {
    const editorState = await client.request('automation_editor_get_state');
    runner.writeJson('failure-automation-editor-state.json', editorState);
  } catch (error) {
    runner.log('failure_evidence_editor_state_error', { error: error instanceof Error ? error.message : String(error) });
  }
  try {
    await captureFrontWindowScreenshot(path.join(runner.runDir, 'failure.png'), { client });
  } catch (error) {
    runner.log('failure_evidence_screenshot_error', { error: error instanceof Error ? error.message : String(error) });
  }
}

// A best-effort, non-fatal, descriptively-named inline screenshot — matches
// scenario-notebook-editor-undo.mjs's convention rather than a standalone
// screenshot leg, since by the time a separate late step ran the relevant DOM
// state would already have moved on.
async function captureEvidenceScreenshot(runner, client, name) {
  try {
    await captureFrontWindowScreenshot(path.join(runner.runDir, name), { client });
  } catch (error) {
    runner.log('evidence_screenshot_error', { name, error: error instanceof Error ? error.message : String(error) });
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-automation-editor.mjs');
    return;
  }
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('automation editor scenario requires a named non-production profile');
  const resources = resolveHarnessResources(profile);
  const binary = path.join(resources.appPath, 'Contents', 'MacOS', 'attn');
  const runner = createScenarioRunner(options, {
    scenarioId: 'AUTOMATION-EDITOR',
    tier: 'tier2-local',
    prefix: 'automation-editor',
    metadata: { profile },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const suffix = Date.now().toString(36);
  const primaryID = `automation-editor-${suffix}`;
  const renamedID = `automation-editor-renamed-${suffix}`;
  const harnessMarkerComment = `# harness-marker: ${suffix}`;

  let daemonEnv = null;
  let fixturePath = null;
  let appBuild = null;
  let protocolVersion = null;

  try {
    await runner.step('setup_fixtures', async () => {
      fixturePath = fs.realpathSync(fs.mkdtempSync(path.join(runner.sessionDir, 'automation-editor-')));
      protocolVersion = readFrontendProtocolVersion();
    });

    await runner.step('restart_isolated_daemon', async () => {
      daemonEnv = profileEnv(profile);
      try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {}
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);
    });

    await runner.step('launch_packaged_app', async () => {
      await launchFreshAppAndConnect(client, observer);
      const state = await client.request('get_state');
      appBuild = state.appBuild ?? null;
    });

    // Leg 1: opening New shows the starter template at revision 0, and — the
    // new product behavior team-lead flagged — Reload is NOT offered while
    // creating (loadedId is null until the first successful Save).
    await runner.step('leg1_starter_template', async () => {
      await client.request('automations_open_panel');
      const opened = await client.request('automation_editor_open', {});
      runner.assert(opened.present === true, 'editor is present after opening New', opened);
      runner.assert(opened.mode === 'create', 'opening New starts in create mode', opened);
      runner.assert(opened.definitionId === null, 'create mode has no definitionId', opened);
      runner.assert(opened.revision === 0, 'create mode starts at revision 0', opened);
      runner.assert(opened.status === 'ready', 'starter template load reaches ready', opened);
      runner.assert(opened.text.includes('id: my-automation'), 'starter template text includes the placeholder id', opened);
      runner.assert(opened.text.includes(`api_version: ${API_VERSION}`), 'starter template text includes the current api_version', opened);
      runner.assert(
        opened.reloadOffered === false,
        'Reload is not offered while creating — no persisted definition exists yet to reload from',
        opened,
      );
    });

    // Leg 2: an invalid definition is rejected by both Validate and Save, and
    // nothing is ever stored — a leg that can fail if either the daemon
    // guard or the editor's error surfacing regresses.
    await runner.step('leg2_invalid_rejected_nothing_stored', async () => {
      const listBefore = runJSON(binary, ['automation', 'list'], daemonEnv) || [];
      runner.assert(!listBefore.some((row) => row.id === primaryID), 'sanity: primary id does not exist yet', listBefore);

      const invalidYAML = invalidDefinitionYAML({ id: primaryID, locationPath: fixturePath });
      const afterSetText = await client.request('automation_editor_set_text', { text: invalidYAML });
      runner.assert(afterSetText.text === invalidYAML, 'set_text replaces the buffer with the invalid YAML exactly', afterSetText);

      const afterValidate = await client.request('automation_editor_click', { button: 'validate' });
      runner.assert(afterValidate.validation.state === 'error', 'Validate reports an error for the invalid definition', afterValidate);
      runner.assert(
        afterValidate.validation.message.toLowerCase().includes('api_version'),
        'the validation error names the offending field (api_version)',
        afterValidate,
      );
      await captureEvidenceScreenshot(runner, client, 'leg2-validation-error.png');

      const afterSave = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterSave.present === true, 'the editor stays open after a refused Save', afterSave);
      runner.assert(afterSave.saveError !== '', 'Save surfaces a saveError for the invalid definition', afterSave);

      const listAfter = runJSON(binary, ['automation', 'list'], daemonEnv) || [];
      runner.assert(
        listAfter.length === listBefore.length && !listAfter.some((row) => row.id === primaryID),
        'nothing is stored after the refused create',
        { listBefore, listAfter },
      );
    });

    // Leg 3: a real create, with a hand-written leading comment — the
    // fixture leg2's D1 assertion (comment survival) depends on.
    let leg3Revision = null;
    let leg3SpecYAML = null;
    const promptV1 = 'Slice 7 packaged editor proof: initial create.';
    await runner.step('leg3_create_with_comment', async () => {
      const validYAML = editorDefinitionYAML({ id: primaryID, locationPath: fixturePath, prompt: promptV1, comment: harnessMarkerComment });
      const afterSetText = await client.request('automation_editor_set_text', { text: validYAML });
      runner.assert(afterSetText.text === validYAML, 'set_text replaces the buffer with the valid YAML exactly', afterSetText);

      const afterValidate = await client.request('automation_editor_click', { button: 'validate' });
      runner.assert(afterValidate.validation.state === 'ok', 'Validate accepts the valid definition', afterValidate);

      const afterSave = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterSave.present === false, 'a successful create closes the editor back to the list', afterSave);

      await poll(async () => {
        const current = await client.request('automations_get_state');
        return findDefinitionRow(current, primaryID) ? current : null;
      }, `created definition ${primaryID} to appear in the panel`, PANEL_TIMEOUT_MS);
      await captureEvidenceScreenshot(runner, client, 'leg3-after-save.png');

      const shownRow = findListRow(binary, primaryID, daemonEnv);
      const shownYAML = showSpecYAML(binary, primaryID, daemonEnv);
      runner.assert(shownRow && shownRow.id === primaryID, 'the CLI shows the created definition by id', shownRow);
      runner.assert(shownRow.revision === 1, 'a fresh create lands at revision 1', shownRow);
      runner.assert(shownYAML.includes(harnessMarkerComment), 'the stored YAML includes the hand-written comment', shownYAML);
      leg3Revision = shownRow.revision;
      leg3SpecYAML = shownYAML;
    });

    // Leg 4 (new, per team-lead): creating a SECOND definition that reuses
    // the id from leg 3 is refused — the id belongs to a live definition
    // already. The original definition must be untouched by the attempt.
    await runner.step('leg4_create_collision_refused', async () => {
      const opened = await client.request('automation_editor_open', {});
      runner.assert(opened.mode === 'create', 'opening New again starts a fresh create buffer', opened);
      runner.assert(opened.reloadOffered === false, 'Reload is still not offered while creating', opened);

      const collisionYAML = editorDefinitionYAML({
        id: primaryID,
        locationPath: fixturePath,
        prompt: 'Attempted collision create — must be refused.',
        comment: null,
      });
      await client.request('automation_editor_set_text', { text: collisionYAML });

      const afterValidate = await client.request('automation_editor_click', { button: 'validate' });
      runner.assert(afterValidate.validation.state === 'ok', 'the collision definition is schema-valid on its own — the guard is id-collision, not shape', afterValidate);

      const afterSave = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterSave.present === true, 'the editor stays open after a refused collision create', afterSave);
      runner.assert(
        afterSave.saveError.includes('already exists') && afterSave.saveError.includes(primaryID),
        'Save refuses the collision with an "already exists" error naming the id',
        afterSave,
      );

      const afterCollisionRow = findListRow(binary, primaryID, daemonEnv);
      const afterCollisionYAML = showSpecYAML(binary, primaryID, daemonEnv);
      runner.assert(
        afterCollisionRow.revision === leg3Revision && afterCollisionYAML === leg3SpecYAML,
        "the original definition's revision and content are unchanged by the refused collision",
        {
          before: { revision: leg3Revision, specYaml: leg3SpecYAML },
          after: { revision: afterCollisionRow.revision, specYaml: afterCollisionYAML },
        },
      );

      const afterCancel = await client.request('automation_editor_click', { button: 'cancel' });
      runner.assert(afterCancel.present === false, 'Cancel closes the collision attempt back to the list', afterCancel);
    });

    // Leg 5 (D1, load-bearing): opening the definition leg3 created for edit
    // shows the hand-written comment intact — a save/reload round-trip that
    // does not preserve comments is exactly the regression this scenario
    // exists to catch.
    await runner.step('leg5_comments_survive_roundtrip', async () => {
      const opened = await client.request('automation_editor_open', { definitionId: primaryID });
      runner.assert(opened.mode === 'edit', 'opening an existing definition starts in edit mode', opened);
      runner.assert(opened.definitionId === primaryID, 'edit mode reports the definition id being edited', opened);
      runner.assert(opened.revision === leg3Revision, 'edit mode reports the definition\'s current revision', opened);
      runner.assert(opened.reloadOffered === true, 'Reload is offered once a persisted definition is being edited', opened);
      runner.assert(
        opened.text.includes(harnessMarkerComment),
        'D1: the hand-written comment survives the save/reload round-trip into the editor buffer',
        opened,
      );
    });

    // Leg 6 (D4): changing the id in the buffer and saving is refused — an id
    // change must go through a separate create, not an in-place edit.
    await runner.step('leg6_id_change_refusal', async () => {
      const renameYAML = editorDefinitionYAML({
        id: renamedID,
        locationPath: fixturePath,
        prompt: 'Attempted rename via id change — must be refused.',
        comment: null,
      });
      await client.request('automation_editor_set_text', { text: renameYAML });

      const afterSave = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterSave.present === true, 'the editor stays open after a refused id-change save', afterSave);
      runner.assert(afterSave.mode === 'edit', 'the editor remains in edit mode for the original definition', afterSave);
      runner.assert(afterSave.definitionId === primaryID, 'the editor is still keyed on the original definition id', afterSave);
      runner.assert(
        afterSave.saveError.includes('does not match') && afterSave.saveError.includes(renamedID) && afterSave.saveError.includes(primaryID),
        'Save refuses the id change, naming both the buffer id and the definition being edited',
        afterSave,
      );

      const listAfter = runJSON(binary, ['automation', 'list'], daemonEnv) || [];
      runner.assert(
        !listAfter.some((row) => row.id === renamedID) && listAfter.filter((row) => row.id === primaryID).length === 1,
        'no definition was created under the renamed id; the original still exists exactly once',
        listAfter,
      );
    });

    // Leg 7 (D5): a Save against a stale revision — the definition changed
    // out from under the open editor via the CLI — is refused, and Reload
    // recovers by pulling the current content into the buffer, after which a
    // normal Save succeeds.
    await runner.step('leg7_stale_revision_refusal_and_reload', async () => {
      // Reset the buffer's id back to primaryID (leg 6 left it on renamedID)
      // before doing anything else, so this leg's Save attempts exercise the
      // stale-revision guard rather than re-triggering the id-change guard.
      const localPromptA = 'Local buffer edit before the out-of-band mutation lands.';
      const resetYAML = editorDefinitionYAML({ id: primaryID, locationPath: fixturePath, prompt: localPromptA, comment: null });
      await client.request('automation_editor_set_text', { text: resetYAML });

      const outOfBandPrompt = 'Mutated out of band via the bundled CLI while the editor was open.';
      const outOfBandFile = path.join(runner.sessionDir, 'out-of-band.yml');
      fs.writeFileSync(outOfBandFile, editorDefinitionYAML({ id: primaryID, locationPath: fixturePath, prompt: outOfBandPrompt, comment: null }));
      run(binary, ['automation', 'apply', '--file', outOfBandFile], daemonEnv);

      const outOfBandRow = findListRow(binary, primaryID, daemonEnv);
      runner.assert(outOfBandRow.revision > leg3Revision, 'the out-of-band CLI apply bumps the revision past what the open editor has', outOfBandRow);
      const outOfBandRevision = outOfBandRow.revision;

      const afterStaleSave = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterStaleSave.present === true, 'the editor stays open after a refused stale-revision save', afterStaleSave);
      runner.assert(
        afterStaleSave.saveError.toLowerCase().includes('changed elsewhere') || afterStaleSave.saveError.toLowerCase().includes('reload'),
        'Save refuses the stale-revision write with a "changed elsewhere / reload" error',
        afterStaleSave,
      );

      const afterReload = await client.request('automation_editor_click', { button: 'reload' });
      runner.assert(afterReload.reloadError === '', 'Reload succeeds without error', afterReload);
      runner.assert(afterReload.revision === outOfBandRevision, 'Reload pulls in the current (out-of-band) revision', afterReload);
      runner.assert(afterReload.text.includes(outOfBandPrompt), 'Reload pulls in the current (out-of-band) content', afterReload);

      // Re-apply the SAME local edit on top of the now-current buffer; this
      // Save should succeed since the buffer is no longer stale.
      const reappliedYAML = editorDefinitionYAML({ id: primaryID, locationPath: fixturePath, prompt: localPromptA, comment: null });
      await client.request('automation_editor_set_text', { text: reappliedYAML });
      const afterFinalSave = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterFinalSave.present === false, 'the recovered Save succeeds and closes the editor', afterFinalSave);

      const finalShown = runJSON(binary, ['automation', 'show', primaryID], daemonEnv);
      runner.assert(finalShown.Revision === outOfBandRevision + 1, 'the recovered save bumps the revision once more', finalShown);
      runner.assert(finalShown.SpecYAML.includes(localPromptA), 'the recovered save persists the locally re-applied content', finalShown);
    });

    // Leg 8 (defect A regression proof): a comment-only edit — the exact
    // shape of change this editor exists to make — still bumps the
    // revision. Before the store fix, the bump condition compared only
    // spec_json, so a save that changed spec_yaml alone (comments live only
    // there, never re-derived into spec_json) left revision untouched and
    // the stale-save guard blind to it. The new buffer is built by
    // prepending exactly ONE comment line to the exact bytes the editor
    // loaded, so everything after that line is untouched by construction —
    // if spec_json also moved, this leg would prove nothing.
    await runner.step('leg8_comment_only_edit_bumps_revision', async () => {
      const shownBefore = runJSON(binary, ['automation', 'show', primaryID], daemonEnv);

      const opened = await client.request('automation_editor_open', { definitionId: primaryID });
      runner.assert(opened.mode === 'edit', 'opening the shared definition for edit starts in edit mode', opened);
      runner.assert(opened.definitionId === primaryID, 'edit mode reports the definition id being edited', opened);
      runner.assert(
        opened.revision === shownBefore.Revision,
        "the editor's revision matches the CLI's independently-read revision",
        { opened, shownBefore },
      );
      runner.assert(
        opened.text === shownBefore.SpecYAML,
        'the editor buffer loads exactly the stored spec_yaml bytes — the baseline this leg edits from',
        { opened, shownBefore },
      );

      const leg8Comment = `# harness-marker-leg8: ${suffix}`;
      const commentOnlyYAML = `${leg8Comment}\n${opened.text}`;
      await client.request('automation_editor_set_text', { text: commentOnlyYAML });

      const afterValidate = await client.request('automation_editor_click', { button: 'validate' });
      runner.assert(afterValidate.validation.state === 'ok', 'the comment-only buffer is still schema-valid', afterValidate);

      const afterSave = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterSave.present === false, 'the comment-only save succeeds and closes the editor', afterSave);

      const shownAfter = runJSON(binary, ['automation', 'show', primaryID], daemonEnv);
      runner.assert(
        shownAfter.Revision === shownBefore.Revision + 1,
        'D-A: a comment-only edit still bumps the revision — the stale-save guard is no longer blind to comment-only changes',
        { before: shownBefore.Revision, after: shownAfter.Revision },
      );
      runner.assert(
        shownAfter.SpecYAML === commentOnlyYAML,
        'the stored spec_yaml is exactly the comment-prepended buffer — nothing else moved',
        { expected: commentOnlyYAML, actual: shownAfter.SpecYAML },
      );
      // The assertion that actually matters for D-A: prove this was a pure
      // comment edit by checking the CANONICAL spec, not just the YAML text.
      // A future refactor that starts folding comments into spec_json would
      // otherwise leave this leg passing for the wrong reason.
      runner.assert(
        shownAfter.SpecJSON === shownBefore.SpecJSON,
        'the canonical spec_json is byte-identical before/after — this was a pure comment edit, not a disguised content change',
        { before: shownBefore.SpecJSON, after: shownAfter.SpecJSON },
      );
    });

    // Leg 9 (defect B regression proof): a definition deleted out of band
    // while an editor has it open must not be resurrected by that editor's
    // Save. Before the daemon fix, DeleteAutomationDefinition left revision
    // untouched, so the stale editor's expected_revision still matched the
    // soft-deleted row, the guard passed, and the upsert cleared
    // deleted_at — reported to the user as a successful save. Mirrors leg7's
    // out-of-band-mutation shape, but via `automation delete` instead of
    // `automation apply`.
    await runner.step('leg9_delete_elsewhere_then_save_refused', async () => {
      const shownBefore = runJSON(binary, ['automation', 'show', primaryID], daemonEnv);
      runner.assert(
        shownBefore && shownBefore.ID === primaryID,
        'sanity: the shared definition is still live before this leg deletes it',
        shownBefore,
      );

      const opened = await client.request('automation_editor_open', { definitionId: primaryID });
      runner.assert(opened.mode === 'edit', 'opening the shared definition for edit starts in edit mode', opened);
      runner.assert(opened.definitionId === primaryID, 'edit mode reports the definition id being edited', opened);
      runner.assert(
        opened.revision === shownBefore.Revision,
        "the editor holds the definition's current revision before the out-of-band delete",
        opened,
      );

      run(binary, ['automation', 'delete', primaryID], daemonEnv);
      const listAfterDelete = runJSON(binary, ['automation', 'list'], daemonEnv) || [];
      runner.assert(
        !listAfterDelete.some((row) => row.id === primaryID),
        'sanity: the out-of-band CLI delete removes the definition from the live list while the editor is still open on it',
        listAfterDelete,
      );

      const staleEditPrompt = 'Edited after the definition was deleted elsewhere — this save must be refused.';
      const staleEditYAML = editorDefinitionYAML({ id: primaryID, locationPath: fixturePath, prompt: staleEditPrompt, comment: null });
      await client.request('automation_editor_set_text', { text: staleEditYAML });

      const afterValidate = await client.request('automation_editor_click', { button: 'validate' });
      runner.assert(
        afterValidate.validation.state === 'ok',
        'the buffer is schema-valid on its own — the guard is the out-of-band delete, not shape',
        afterValidate,
      );

      const afterSave = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(
        afterSave.present === true,
        "the editor stays open after a refused save — the user's in-progress edit is not destroyed",
        afterSave,
      );
      runner.assert(afterSave.mode === 'edit', 'the editor remains in edit mode for the deleted definition', afterSave);
      runner.assert(afterSave.definitionId === primaryID, 'the editor is still keyed on the same definition id', afterSave);
      runner.assert(
        afterSave.saveError.includes('deleted elsewhere') &&
          afterSave.saveError.includes(primaryID) &&
          afterSave.saveError.includes('New'),
        'Save refuses the edit of a deleted definition, naming the deletion and pointing the user at New',
        afterSave,
      );

      // The part that actually matters: the daemon's own state, not just the
      // UI string — a refusal message sitting in front of a resurrected row
      // would be exactly the failure this leg exists to catch.
      const listAfterRefusedSave = runJSON(binary, ['automation', 'list'], daemonEnv) || [];
      runner.assert(
        !listAfterRefusedSave.some((row) => row.id === primaryID),
        'the definition is still gone after the refused save — it was not resurrected',
        listAfterRefusedSave,
      );
      const shownAfterRefusedSave = runJSON(binary, ['automation', 'show', primaryID], daemonEnv);
      runner.assert(
        shownAfterRefusedSave === null,
        'the CLI also reports the definition as gone (filtered like any soft-deleted row) after the refused save',
        shownAfterRefusedSave,
      );
    });

    // The reviewer-reported split brain: `enabled` was written by the panel
    // toggle (column only) AND by a save (from the YAML), with nothing keeping
    // them in step. Disable, then edit, and the stale `enabled: true` in the
    // stored YAML came back — for a cron trigger, unattended sessions firing
    // again after the operator turned them off, reported as a clean save.
    //
    // Every out-of-band step here goes through the REAL panel toggle verb
    // (automations_toggle_enabled), not a CLI shortcut, because the toggle is
    // the surface that was writing only half the state.
    await runner.step('leg10_panel_toggle_off_survives_a_later_edit', async () => {
      const leg10ID = `automation-editor-toggle-${suffix}`;
      const leg10Comment = `# harness-marker-leg10: ${suffix}`;
      const createYAML = editorDefinitionYAML({
        id: leg10ID,
        locationPath: fixturePath,
        prompt: 'Definition that the operator disables from the panel and then edits.',
        comment: leg10Comment,
        enabled: true,
      });

      // leg9 deliberately ends with the editor still open on its refused save.
      const cleared = await client.request('automation_editor_click', { button: 'cancel' });
      runner.assert(cleared.present === false, "leg9's editor is closed before this leg opens its own", cleared);

      await client.request('automation_editor_open', {});
      await client.request('automation_editor_set_text', { text: createYAML });
      const afterCreate = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterCreate.present === false, 'the enabled definition is created and the editor closes', afterCreate);

      const created = runJSON(binary, ['automation', 'show', leg10ID], daemonEnv);
      runner.assert(created && created.Enabled === true, 'sanity: the definition starts enabled', created);

      // The panel toggle — the exact surface the reviewer named.
      await toggleEnabledAndWait(client, leg10ID, false);

      const disabled = runJSON(binary, ['automation', 'show', leg10ID], daemonEnv);
      runner.assert(disabled.Enabled === false, 'the panel toggle disables the definition', disabled);
      runner.assert(
        disabled.Revision === created.Revision + 1,
        'the toggle bumps the revision — otherwise the stale-save guard cannot see it at all',
        { before: created.Revision, after: disabled.Revision },
      );
      // Write-through, not a read-time overlay: the stored definition itself
      // now says disabled, so the CLI, the editor, and the column agree.
      runner.assert(
        disabled.SpecYAML.includes('enabled: false') && !disabled.SpecYAML.includes('enabled: true'),
        'the toggle wrote through into the stored YAML — no stale `enabled: true` left behind to be saved back',
        disabled.SpecYAML,
      );
      runner.assert(
        disabled.SpecYAML.includes(leg10Comment),
        "the toggle preserved the author's comment — it rewrote one scalar, not the document",
        disabled.SpecYAML,
      );
      runner.assert(
        JSON.parse(disabled.SpecJSON).enabled === false,
        'the canonical spec_json agrees too — one authority, not two',
        disabled.SpecJSON,
      );

      // Now the operator opens it and makes an unrelated edit.
      const opened = await client.request('automation_editor_open', { definitionId: leg10ID });
      runner.assert(
        opened.text.includes('enabled: false'),
        'the editor opens on the automation\'s REAL current state, so the operator is not editing a lie',
        opened,
      );
      runner.assert(opened.revision === disabled.Revision, 'the editor holds the post-toggle revision', { opened, disabled });

      await client.request('automation_editor_set_text', { text: `# unrelated edit ${suffix}\n${opened.text}` });
      const afterEdit = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterEdit.present === false, 'the unrelated edit saves cleanly', afterEdit);

      // The assertion this whole leg exists for.
      const afterSave = runJSON(binary, ['automation', 'show', leg10ID], daemonEnv);
      runner.assert(
        afterSave.Enabled === false,
        'D-F: editing a disabled automation does NOT silently re-enable it — the toggle is not reversed by a save',
        afterSave,
      );

      // And back on, through the panel again, so the leg proves the toggle is
      // symmetric rather than only pinning the disable direction.
      await toggleEnabledAndWait(client, leg10ID, true);
      const reEnabled = runJSON(binary, ['automation', 'show', leg10ID], daemonEnv);
      runner.assert(
        reEnabled.Enabled === true && reEnabled.SpecYAML.includes('enabled: true'),
        'toggling back on writes through the same way — the disable direction is not a special case',
        reEnabled,
      );
      runner.assert(
        reEnabled.SpecYAML.includes(leg10Comment),
        "the author's comment survives a full off-and-on cycle, not just one rewrite",
        reEnabled.SpecYAML,
      );

      // Not covered here: a toggle landing while THIS editor is open, which
      // would exercise the stale-save guard against the toggle's revision
      // bump. AutomationsPanel renders the editor as a full replacement of the
      // panel body (see its EditorTarget doc comment), so the toggle control
      // is not in the DOM at all while the editor is up — there is no way to
      // drive that race through the real UI, and faking it here would be
      // testing the harness rather than the product. The guard itself is
      // pinned where it is reachable: the revision assertion above, plus
      // TestSetAutomationEnabledWritesThroughToStoredSpec (store) and
      // TestAutomationDefinitionGetWSAfterToggleShowsDisabledWithoutReenabling
      // (daemon).

      run(binary, ['automation', 'delete', leg10ID], daemonEnv);
    });

    runner.finishSuccess({ profile, primaryID, renamedID, leg3Revision, appBuild, protocolVersion });
  } catch (error) {
    await captureFailureEvidence(runner, client).catch(() => {});
    runner.finishFailure(error, { profile, primaryID, renamedID, appBuild, protocolVersion });
    throw error;
  } finally {
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
    if (daemonEnv) { try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {} }
    if (fixturePath) { try { fs.rmSync(fixturePath, { recursive: true, force: true }); } catch {} }
    // Leave a healthy plain profile daemon behind for whatever runs next.
    try { run(binary, ['daemon', 'ensure'], profileEnv(profile)); } catch {}
    runner.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
