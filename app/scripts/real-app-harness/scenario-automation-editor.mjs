#!/usr/bin/env node

/**
 * Packaged-app proof for Slice 7 PR B's self-service automation YAML editor:
 * the starter template, invalid-YAML rejection (nothing stored), a real
 * create, the create-collision refusal, the v2 spec-canonical save/reload
 * contract, the id-change refusal (D4), the stale-revision refusal plus
 * Reload recovery (D5), a comment-only edit that is a no-op revision-wise
 * (immediately followed by a real semantic edit that does bump), a Save
 * that is refused rather than silently resurrecting a definition deleted
 * elsewhere, and a panel toggle-off that survives a later unrelated edit —
 * all driven through the real rendered editor and panel via the
 * automation_editor_* and automations_* UI-automation bridge verbs,
 * cross-checked against the daemon's own state via the bundled `attn` CLI
 * (`automation show`/`list`), not just the DOM.
 *
 * v2 spec-canonical contract (PR #629, "make enabled column-only and drop
 * spec_yaml storage"): the stored source of truth is spec_json alone: the
 * `spec_yaml` DB column is gone. Every YAML read (`attn automation show`,
 * the editor's Save-then-reopen) is re-derived canonically from spec_json via
 * automation.MarshalDefinitionYAML — 4-space indent, no comments, no
 * original formatting. This is intentional, not a bug: hand-written
 * comments are interchange-only and do not round-trip. `enabled` and
 * `policy` are likewise outside the spec entirely (column-only /
 * trigger-implied) and a YAML carrying either key is rejected outright
 * (errEnabledManagedOutsideSpec / errPolicyRemoved in
 * internal/automation/automation.go). Revision bumps exactly when spec_json
 * changes (store.UpsertAutomationDefinition): a comment-only edit changes
 * only the buffer, not the canonical spec, so re-saving it is a no-op for
 * revision purposes; a real content edit (e.g. `name:`) always bumps by
 * exactly one.
 *
 * One definition id is reused across most legs (`automation-editor-<suffix>`)
 * so the id-change and stale-revision legs exercise the SAME live definition
 * leg3 just created, rather than fresh throwaway ids — matching how a real
 * user edits one automation repeatedly in one sitting. The same id carries
 * through the no-op/bump revision leg and finally into the delete-elsewhere
 * leg, which ends the scenario with it soft-deleted on purpose. A second id
 * (`automation-editor-renamed-<suffix>`) exists only as the (refused) target
 * of the id-change leg; it must never actually be created.
 *
 * No fake-agent probe is needed: this scenario never triggers a run. Every
 * definition it applies is manual-trigger, so `launch.executable` is never
 * invoked and can stay unset like the starter template's, regardless of
 * enabled state. `enabled` is column-only post-PR5/#629 (spec YAML/JSON no
 * longer carries it at all) and every apply of a brand-new id always inserts
 * it enabled; leg9 relies on exactly that default to get an enabled
 * definition to disable from the panel, and deletes it before finishing.
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

// A soft-deleted (or never-existed) id makes `automation show` exit 1 with
// "automation: daemon error: ..." on stderr, rather than printing anything
// JSON-parseable — there is no more null/empty-document result to check.
function showFailsWithDaemonError(binary, id, env) {
  try {
    run(binary, ['automation', 'show', id], env);
    return false;
  } catch (error) {
    const stderr = typeof error.stderr === 'string' ? error.stderr : String(error.stderr || error.message || error);
    return stderr.includes('automation: daemon error');
  }
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

// `enabled` is not a spec field post-PR5 (it is the enabled COLUMN's sole
// authority; a YAML carrying `enabled:` is rejected outright — see
// errEnabledManagedOutsideSpec in internal/automation/automation.go), so this
// template never emits it. Every apply of a brand-new id is inserted enabled
// regardless (store.UpsertAutomationDefinition); that is harmless here since
// every definition this scenario applies is manual-trigger and never fires on
// its own. leg9 relies on that same default-enabled behavior to get a
// definition it can disable from the panel, and deletes it at the end of the
// leg.
function editorDefinitionYAML({ id, locationPath, prompt, comment }) {
  const commentLine = comment ? `${comment}\n` : '';
  return `${commentLine}api_version: ${API_VERSION}
id: ${id}
name: Slice 7 packaged editor proof
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
// Must not carry `enabled:` — ParseDefinitionYAML probes for that key BEFORE
// api_version validation (errEnabledManagedOutsideSpec in
// internal/automation/automation.go), so leaving it in would make this
// fixture fail for the wrong reason and never reach the api_version
// complaint this leg pins.
function invalidDefinitionYAML({ id, locationPath }) {
  return `api_version: not-a-real-api-version
id: ${id}
name: Slice 7 packaged editor proof (invalid)
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

    // Leg 3: a real create, with a hand-written leading comment, then a
    // reload through both the CLI and the real editor — pinning the v2
    // spec-canonical contract (PR #629): the re-read YAML is a canonical
    // rendering of the saved content (same id/name/prompt), and the
    // hand-written comment is gone — comments are interchange-only and do
    // not round-trip since spec_yaml storage was dropped. Also folds in what
    // used to be a separate leg (D1, pre-#629) proving the editor's own
    // reopen path gets the same canonical rendering, not just the CLI.
    let leg3Revision = null;
    let leg3SpecYAML = null;
    const promptV1 = 'Slice 7 packaged editor proof: initial create.';
    await runner.step('leg3_create_then_reload_is_canonical', async () => {
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
      runner.assert(
        shownYAML.includes(`id: ${primaryID}`) && shownYAML.includes('name: Slice 7 packaged editor proof') && shownYAML.includes(promptV1),
        'the re-read YAML is the canonical rendering of the saved spec — same id/name/prompt content',
        shownYAML,
      );
      runner.assert(
        !shownYAML.includes(harnessMarkerComment) && !shownYAML.includes('#'),
        'v2: the hand-written comment does not survive the round-trip — comments are interchange-only, not stored (PR #629 dropped spec_yaml storage)',
        shownYAML,
      );
      leg3Revision = shownRow.revision;
      leg3SpecYAML = shownYAML;

      // Reopen through the real editor too — same canonical-rendering
      // guarantee on the WS load path, not just the CLI.
      const opened = await client.request('automation_editor_open', { definitionId: primaryID });
      runner.assert(opened.mode === 'edit', 'opening an existing definition starts in edit mode', opened);
      runner.assert(opened.definitionId === primaryID, 'edit mode reports the definition id being edited', opened);
      runner.assert(opened.revision === leg3Revision, "edit mode reports the definition's current revision", opened);
      runner.assert(opened.reloadOffered === true, 'Reload is offered once a persisted definition is being edited', opened);
      runner.assert(
        opened.text.includes(promptV1) && !opened.text.includes(harnessMarkerComment),
        'the editor buffer reopens with the same canonical rendering — content preserved, hand-written comment absent',
        opened,
      );

      // Close back out before the next leg. The editor replaces the panel
      // body entirely while open (see leg9's not-covered note), so leaving
      // it open here would make leg4's `automation_editor_open {}` for New
      // an undrivable flow — the New button isn't reachable while an editor
      // is already up.
      const afterReopenCancel = await client.request('automation_editor_click', { button: 'cancel' });
      runner.assert(afterReopenCancel.present === false, 'Cancel closes the reopened editor back to the list', afterReopenCancel);
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

    // Leg 5 (D4): changing the id in the buffer and saving is refused — an id
    // change must go through a separate create, not an in-place edit.
    await runner.step('leg5_id_change_refusal', async () => {
      // leg4's cancel closed the editor entirely (it left the collision
      // attempt's create buffer, not primaryID's edit buffer) — open the
      // shared definition for edit fresh before attempting the rename.
      const reopened = await client.request('automation_editor_open', { definitionId: primaryID });
      runner.assert(reopened.mode === 'edit', 'opening the shared definition for edit starts in edit mode', reopened);
      runner.assert(reopened.revision === leg3Revision, "the editor's revision matches the definition's current revision", { reopened, leg3Revision });

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

    // Leg 6 (D5): a Save against a stale revision — the definition changed
    // out from under the open editor via the CLI — is refused, and Reload
    // recovers by pulling the current content into the buffer, after which a
    // normal Save succeeds.
    await runner.step('leg6_stale_revision_refusal_and_reload', async () => {
      // Reset the buffer's id back to primaryID (leg 5 left it on renamedID)
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

      const finalRow = findListRow(binary, primaryID, daemonEnv);
      const finalYAML = showSpecYAML(binary, primaryID, daemonEnv);
      runner.assert(finalRow.revision === outOfBandRevision + 1, 'the recovered save bumps the revision once more', finalRow);
      runner.assert(finalYAML.includes(localPromptA), 'the recovered save persists the locally re-applied content', finalYAML);
    });

    // Leg 7: v2 revision semantics (store.UpsertAutomationDefinition —
    // revision bumps exactly when spec_json changes). Part A: a comment-only
    // edit changes only the buffer's interchange formatting, not the
    // canonical spec, so re-saving the exact same content the editor loaded
    // (modulo a leading comment, which is dropped on every read anyway per
    // leg3) reapplies an IDENTICAL spec_json — a no-op, revision unchanged.
    // Part B: immediately after, a real semantic edit (changing `name:`)
    // DOES change spec_json and bumps revision by exactly one — proving
    // part A's unchanged revision is because nothing meaningful changed, not
    // because saves stopped bumping revision at all.
    await runner.step('leg7_comment_only_save_is_noop_then_semantic_edit_bumps', async () => {
      const rowBefore = findListRow(binary, primaryID, daemonEnv);
      const yamlBefore = showSpecYAML(binary, primaryID, daemonEnv);

      const opened = await client.request('automation_editor_open', { definitionId: primaryID });
      runner.assert(opened.mode === 'edit', 'opening the shared definition for edit starts in edit mode', opened);
      runner.assert(opened.definitionId === primaryID, 'edit mode reports the definition id being edited', opened);
      runner.assert(
        opened.revision === rowBefore.revision,
        "the editor's revision matches the CLI's independently-read revision",
        { opened, rowBefore },
      );
      runner.assert(
        opened.text === yamlBefore,
        'the editor buffer loads exactly the stored (canonically-rendered) YAML — the baseline this leg edits from',
        { opened, yamlBefore },
      );

      const leg7Comment = `# harness-marker-leg7: ${suffix}`;
      const commentOnlyYAML = `${leg7Comment}\n${opened.text}`;
      await client.request('automation_editor_set_text', { text: commentOnlyYAML });

      const afterValidate = await client.request('automation_editor_click', { button: 'validate' });
      runner.assert(afterValidate.validation.state === 'ok', 'the comment-only buffer is still schema-valid', afterValidate);

      const afterSave = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterSave.present === false, 'the comment-only save succeeds and closes the editor', afterSave);

      const rowAfterCommentOnly = findListRow(binary, primaryID, daemonEnv);
      const yamlAfterCommentOnly = showSpecYAML(binary, primaryID, daemonEnv);
      runner.assert(
        rowAfterCommentOnly.revision === rowBefore.revision,
        'v2: a comment-only save is a no-op for revision — spec_json is unchanged, and revision guards spec content only',
        { before: rowBefore.revision, after: rowAfterCommentOnly.revision },
      );
      runner.assert(
        !yamlAfterCommentOnly.includes(leg7Comment),
        "the comment does not persist either way — it never reached spec_json, so the canonical re-render can't echo it back",
        yamlAfterCommentOnly,
      );
      runner.assert(
        yamlAfterCommentOnly === yamlBefore,
        'the canonical rendering is byte-identical before/after the comment-only save — nothing meaningful moved',
        { before: yamlBefore, after: yamlAfterCommentOnly },
      );

      // Part B: a real semantic edit — change `name:` — must bump revision by
      // exactly one, proving part A's unchanged revision was content-specific.
      const reopened = await client.request('automation_editor_open', { definitionId: primaryID });
      const semanticYAML = reopened.text.replace('name: Slice 7 packaged editor proof', 'name: Slice 7 packaged editor proof (renamed)');
      runner.assert(semanticYAML !== reopened.text, 'sanity: the name replacement actually changed the buffer', { reopened });
      await client.request('automation_editor_set_text', { text: semanticYAML });
      const afterSemanticSave = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterSemanticSave.present === false, 'the semantic edit saves cleanly', afterSemanticSave);

      const rowAfterSemantic = findListRow(binary, primaryID, daemonEnv);
      runner.assert(
        rowAfterSemantic.revision === rowBefore.revision + 1,
        'v2: a real semantic edit (name change) bumps revision by exactly one — spec_json content actually changed',
        { before: rowBefore.revision, after: rowAfterSemantic.revision },
      );
      runner.assert(rowAfterSemantic.name === 'Slice 7 packaged editor proof (renamed)', 'the renamed name is what got stored', rowAfterSemantic);
    });

    // Leg 8 (defect B regression proof): a definition deleted out of band
    // while an editor has it open must not be resurrected by that editor's
    // Save. Before the daemon fix, DeleteAutomationDefinition left revision
    // untouched, so the stale editor's expected_revision still matched the
    // soft-deleted row, the guard passed, and the upsert cleared
    // deleted_at — reported to the user as a successful save. Mirrors leg6's
    // out-of-band-mutation shape, but via `automation delete` instead of
    // `automation apply`.
    await runner.step('leg8_delete_elsewhere_then_save_refused', async () => {
      const rowBefore = findListRow(binary, primaryID, daemonEnv);
      runner.assert(
        rowBefore && rowBefore.id === primaryID,
        'sanity: the shared definition is still live before this leg deletes it',
        rowBefore,
      );

      const opened = await client.request('automation_editor_open', { definitionId: primaryID });
      runner.assert(opened.mode === 'edit', 'opening the shared definition for edit starts in edit mode', opened);
      runner.assert(opened.definitionId === primaryID, 'edit mode reports the definition id being edited', opened);
      runner.assert(
        opened.revision === rowBefore.revision,
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
      runner.assert(
        showFailsWithDaemonError(binary, primaryID, daemonEnv),
        'the CLI also reports the definition as gone (a daemon error, like any soft-deleted row) after the refused save',
        primaryID,
      );
    });

    // Leg 9: `enabled` has exactly one authority post-#629 — the column,
    // set only by SetAutomationEnabled — and that function never touches
    // spec_json or revision (TestSetAutomationEnabledNeverTouchesSpecOrRevision,
    // internal/store/automations_test.go). This leg pins both directions of
    // that split: (1) the panel toggle does what it says (enabled flips) and
    // does NOT bump revision, in either direction; (2) a later spec save does
    // not silently flip enabled back on. It also keeps pinning that neither
    // the stored nor the buffered YAML ever carries an `enabled:` key, so a
    // regression that reintroduces one fails loudly (a parse error on apply)
    // rather than silently disagreeing with the column again.
    //
    // Every out-of-band step here goes through the REAL panel toggle verb
    // (automations_toggle_enabled), not a CLI shortcut, because the toggle is
    // the surface under test.
    await runner.step('leg9_panel_toggle_off_survives_a_later_edit', async () => {
      const leg9ID = `automation-editor-toggle-${suffix}`;
      const createYAML = editorDefinitionYAML({
        id: leg9ID,
        locationPath: fixturePath,
        prompt: 'Definition that the operator disables from the panel and then edits.',
        comment: null,
      });

      // leg8 deliberately ends with the editor still open on its refused save.
      const cleared = await client.request('automation_editor_click', { button: 'cancel' });
      runner.assert(cleared.present === false, "leg8's editor is closed before this leg opens its own", cleared);

      await client.request('automation_editor_open', {});
      await client.request('automation_editor_set_text', { text: createYAML });
      const afterCreate = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterCreate.present === false, 'the definition is created and the editor closes', afterCreate);

      const createdRow = findListRow(binary, leg9ID, daemonEnv);
      runner.assert(createdRow && createdRow.enabled === true, 'sanity: a brand-new definition starts enabled by default', createdRow);

      // The panel toggle.
      await toggleEnabledAndWait(client, leg9ID, false);

      const disabledRow = findListRow(binary, leg9ID, daemonEnv);
      const disabledYAML = showSpecYAML(binary, leg9ID, daemonEnv);
      runner.assert(disabledRow.enabled === false, 'the panel toggle disables the definition', disabledRow);
      runner.assert(
        disabledRow.revision === createdRow.revision,
        'v2: the toggle does NOT bump revision — enabled is column-only and never touches spec_json',
        { before: createdRow.revision, after: disabledRow.revision },
      );
      // The stored YAML never carries `enabled` at all now (column-only, #629).
      runner.assert(
        !disabledYAML.includes('enabled:'),
        'the stored YAML carries no enabled key at all',
        disabledYAML,
      );

      // Now the operator opens it and makes an unrelated edit.
      const opened = await client.request('automation_editor_open', { definitionId: leg9ID });
      runner.assert(
        !opened.text.includes('enabled:'),
        'the editor buffer carries no enabled key either — disabled state is not something a save can echo back wrong',
        opened,
      );
      runner.assert(opened.revision === disabledRow.revision, 'the editor holds the post-toggle revision', { opened, disabledRow });

      await client.request('automation_editor_set_text', { text: `# unrelated edit ${suffix}\n${opened.text}` });
      const afterEdit = await client.request('automation_editor_click', { button: 'save' });
      runner.assert(afterEdit.present === false, 'the unrelated edit saves cleanly', afterEdit);

      // The assertion this whole leg exists for.
      const afterSaveRow = findListRow(binary, leg9ID, daemonEnv);
      runner.assert(
        afterSaveRow.enabled === false,
        'editing a disabled automation does NOT silently re-enable it — a save never touches the enabled column',
        afterSaveRow,
      );

      // And back on, through the panel again, so the leg proves the toggle is
      // symmetric rather than only pinning the disable direction — and that
      // re-enabling also does not bump revision.
      await toggleEnabledAndWait(client, leg9ID, true);
      const reEnabledRow = findListRow(binary, leg9ID, daemonEnv);
      const reEnabledYAML = showSpecYAML(binary, leg9ID, daemonEnv);
      runner.assert(
        reEnabledRow.enabled === true && !reEnabledYAML.includes('enabled:'),
        'toggling back on writes through the same way — the disable direction is not a special case, and the spec still carries no enabled key',
        reEnabledRow,
      );
      runner.assert(
        reEnabledRow.revision === disabledRow.revision,
        'v2: re-enabling also does not bump revision — the toggle never touches spec_json in either direction',
        { disabled: disabledRow.revision, reEnabled: reEnabledRow.revision },
      );

      // Not covered here: a toggle landing while THIS editor is open.
      // AutomationsPanel renders the editor as a full replacement of the
      // panel body (see its EditorTarget doc comment), so the toggle control
      // is not in the DOM at all while the editor is up — there is no way to
      // drive that race through the real UI, and faking it here would be
      // testing the harness rather than the product. The guard itself is
      // pinned where it is reachable: the revision assertions above, plus
      // TestSetAutomationEnabledNeverTouchesSpecOrRevision (store).

      run(binary, ['automation', 'delete', leg9ID], daemonEnv);
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
