#!/usr/bin/env node

/**
 * Packaged-app proof for PR6's structured automation editor: AutomationForm
 * replaced the freeform YAML buffer (AutomationEditor.tsx, formerly driven by
 * this file as scenario-automation-editor.mjs). The UI-automation bridge
 * verbs moved from automation_editor_* to automation_form_* (see
 * useUiAutomationBridge.ts, `case 'automation_form_*'`), reading/driving the
 * mounted form through a published handle (automationFormAutomation.ts)
 * rather than scraping a textarea, and everything below is driven through the
 * real rendered form and panel, cross-checked against the daemon's own state
 * via the bundled `attn` CLI (`automation show`/`list`), not just the DOM.
 *
 * Two failure classes the old YAML editor exercised are UNREPRESENTABLE by
 * this form, by design:
 *
 *   - Raw invalid YAML (bad api_version, garbage syntax): the form never
 *     lets the user type YAML at all. Its own zod schema
 *     (automationFormModel.ts's automationFormSchema, mirroring
 *     ValidateDefinition's rules client-side) blocks submit before anything
 *     reaches the wire. leg2 pins that client-side block instead of a
 *     daemon-rejection round trip.
 *   - An in-place id edit: in edit mode the form renders `id` as static text
 *     (AutomationForm.tsx's `automation-form-id-static`), not an input — a
 *     real user cannot even attempt the D4 id-change this scenario's
 *     predecessor drove through the YAML buffer. The daemon's id_mismatch
 *     guard (internal/automation) still exists and is still worth proving
 *     independently of the UI, so leg5 reaches it via the bridge's
 *     automation_form_set_values escape hatch: set_values calls the form's
 *     setValues() handler directly (AutomationForm.tsx's bridge
 *     registration), bypassing React state/DOM entirely, so it can write
 *     `id` even though no rendered control offers that action. This is a
 *     deliberate UI-bypass to reach a daemon-only guard, not a claim that a
 *     real user can trigger it this way.
 *
 * A second, load-bearing quirk of the same escape hatch: automation_form_-
 * set_values calls handle.setValues() directly, which is a straight setValue()
 * per key — NOT the name input's onChange handler (handleNameChange in
 * AutomationForm.tsx), which is the only place slug-derivation from name to id
 * runs. Every create leg below sets `id` and `idCustomized: true` explicitly
 * in its set_values payload; slug derivation itself is exercised by
 * AutomationForm's own unit tests, not by this scenario.
 *
 * The form's create-mode defaults (makeCreateDefaults in AutomationForm.tsx)
 * already seed agent 'codex', the first codex catalog model
 * ('gpt-5.6-luna'), and that model's default effort — there is no daemon
 * starter-template fetch anymore (contrast the old editor's
 * `automation_editor_open` returning a YAML starter string). Every create leg
 * below deliberately leaves agent/model/effort OUT of its set_values payload
 * so those defaults carry straight through into the saved spec unmodified —
 * the "starter-simple" launch config this scenario proves end to end.
 *
 * The form saves by sending canonical JSON (which is also valid YAML — see
 * automationFormModel.ts's specJSONString/formValuesToSpec) through the same
 * definition_yaml wire field the old editor used; the daemon re-canonicalizes
 * either way. Revision bumps iff stored spec_json actually changes
 * (store.UpsertAutomationDefinition) — unaffected by PR6, and re-pinned here
 * (leg7, leg8, leg10).
 *
 * After every submit, the apply round trip is asynchronous: this scenario
 * polls automation_form_get_state until either `present === false` (success —
 * AutomationsPanel's onSaved closes the overlay) or saveError/errors becomes
 * non-empty (a refusal), rather than trusting the bridge verb's own
 * settleUi() alone to have waited long enough.
 *
 * Every definition this scenario applies is manual-trigger except the one
 * GitHub-trigger leg (leg8), and nothing it creates ever actually fires:
 * launch.executable is never set, matching the old editor scenario's same
 * invariant.
 *
 * Run serially (packaged-app scenarios are single-tenant):
 *   ATTN_HARNESS_PROFILE=<name> node scripts/real-app-harness/scenario-automation-form.mjs
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
const FORM_TIMEOUT_MS = 30_000;

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
// JSON wrapper — see cmd/attn/automation.go). Get the definition summary
// (id/enabled/revision) from `automation list` instead, filtered by id.
function showSpecYAML(binary, id, env) {
  return run(binary, ['automation', 'show', id], env);
}

function findListRow(binary, id, env) {
  const list = runJSON(binary, ['automation', 'list'], env) || [];
  return list.find((row) => row.id === id) || null;
}

// A soft-deleted (or never-existed) id makes `automation show` exit 1 with
// "automation: daemon error: ..." on stderr, rather than printing anything
// JSON-parseable.
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

// Poll automation_form_get_state until `predicate` is satisfied. Every submit
// this scenario drives is asynchronous (the apply round trip resolves on a
// daemon response), so a single settleUi() inside the bridge verb is not
// enough to observe the outcome — see the file-level comment.
async function pollForm(client, predicate, description, timeoutMs = FORM_TIMEOUT_MS) {
  return poll(async () => {
    const state = await client.request('automation_form_get_state');
    return predicate(state) ? state : null;
  }, description, timeoutMs);
}

function findDefinitionRow(state, definitionId) {
  return (state?.definitions || []).find((row) => row.id === definitionId) || null;
}

// Click the panel's real enable/disable toggle and wait for the daemon to
// come back. Same shape (and same rationale) as the predecessor editor
// scenario's toggleEnabledAndWait: the row must be rendered before the click
// (a definition created through the form reaches the panel by broadcast, not
// synchronously), and automations_toggle_enabled itself only settles a few UI
// frames without awaiting the daemon round trip, so the panel is what this
// polls afterward.
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

// --- form values fixtures -----------------------------------------------
//
// Both fixtures deliberately omit agent/model/effort from the returned
// partial — see the file-level comment on why every create leg lets the
// form's own create-mode defaults (codex / gpt-5.6-luna / medium) carry
// through unmodified.

function manualValues({ id, name, prompt, directoryPath }) {
  return {
    name,
    id,
    idCustomized: true,
    trigger: 'manual',
    directoryPath,
    prompt,
  };
}

function githubValues({ id, name, prompt, repositoriesInclude }) {
  return {
    name,
    id,
    idCustomized: true,
    trigger: 'github_review_requested',
    directoryPath: '',
    repositoriesInclude,
    prompt,
  };
}

async function captureFailureEvidence(runner, client) {
  try {
    const state = await client.request('automations_get_state');
    runner.writeJson('failure-automations-state.json', state);
  } catch (error) {
    runner.log('failure_evidence_state_error', { error: error instanceof Error ? error.message : String(error) });
  }
  try {
    const formState = await client.request('automation_form_get_state');
    runner.writeJson('failure-automation-form-state.json', formState);
  } catch (error) {
    runner.log('failure_evidence_form_state_error', { error: error instanceof Error ? error.message : String(error) });
  }
  try {
    await captureFrontWindowScreenshot(path.join(runner.runDir, 'failure.png'), { client });
  } catch (error) {
    runner.log('failure_evidence_screenshot_error', { error: error instanceof Error ? error.message : String(error) });
  }
}

// A best-effort, non-fatal, descriptively-named inline screenshot — matches
// the predecessor scenario's convention rather than a standalone screenshot
// leg.
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
    printCommonHelp('scripts/real-app-harness/scenario-automation-form.mjs');
    return;
  }
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('automation form scenario requires a named non-production profile');
  const resources = resolveHarnessResources(profile);
  const binary = path.join(resources.appPath, 'Contents', 'MacOS', 'attn');
  const runner = createScenarioRunner(options, {
    scenarioId: 'AUTOMATION-FORM',
    tier: 'tier2-local',
    prefix: 'automation-form',
    metadata: { profile },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const suffix = Date.now().toString(36);
  const primaryID = `automation-form-${suffix}`;
  const renamedID = `automation-form-renamed-${suffix}`;
  const primaryName = `Automation form proof ${suffix}`;

  let daemonEnv = null;
  let fixturePath = null;
  let appBuild = null;
  let protocolVersion = null;

  try {
    await runner.step('setup_fixtures', async () => {
      fixturePath = fs.realpathSync(fs.mkdtempSync(path.join(runner.sessionDir, 'automation-form-')));
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

    // Leg 1: opening New shows the form's own create-mode defaults at
    // revision 0 — no daemon starter-YAML fetch happens anymore (contrast
    // the old editor's automation_editor_open, which pulled a starter
    // template string from the daemon).
    await runner.step('leg1_create_mode_defaults', async () => {
      await client.request('automations_open_panel');
      const opened = await client.request('automation_form_open', {});
      runner.assert(opened.present === true, 'the form is present after opening New', opened);
      runner.assert(opened.mode === 'create', 'opening New starts in create mode', opened);
      runner.assert(opened.definitionId === null, 'create mode has no definitionId', opened);
      runner.assert(opened.revision === 0, 'create mode starts at revision 0', opened);
      runner.assert(opened.status === 'ready', 'create mode reaches ready with no daemon fetch', opened);
      runner.assert(opened.values.trigger === 'manual', 'the default trigger is manual', opened);
      runner.assert(opened.values.agent === 'codex', 'the default agent is codex', opened);
      runner.assert(
        opened.values.model === 'gpt-5.6-luna',
        'the default model is the first codex catalog preset',
        opened,
      );
      runner.assert(
        opened.compiledSentence.includes('Run now'),
        'the compiled sentence for a manual trigger mentions Run now',
        opened,
      );
    });

    // Leg 2: the form's own zod validation blocks submit client-side —
    // nothing reaches the daemon. This replaces the old editor's
    // invalid-YAML-rejected leg; a raw-YAML rejection is unrepresentable
    // through this form (see the file-level comment).
    await runner.step('leg2_client_validation_blocks_submit', async () => {
      const listBefore = runJSON(binary, ['automation', 'list'], daemonEnv) || [];
      runner.assert(!listBefore.some((row) => row.id === primaryID), 'sanity: primary id does not exist yet', listBefore);

      await client.request('automation_form_set_values', {
        values: {
          name: primaryName,
          id: primaryID,
          idCustomized: true,
          directoryPath: 'relative/path',
          prompt: '',
        },
      });
      await client.request('automation_form_submit');

      const afterSubmit = await pollForm(
        client,
        (state) => Object.keys(state.errors || {}).length > 0,
        'client-side validation errors after submitting an incomplete manual definition',
      );
      runner.assert('directoryPath' in afterSubmit.errors, 'the directory path error is reported', afterSubmit);
      runner.assert('prompt' in afterSubmit.errors, 'the prompt error is reported', afterSubmit);
      runner.assert(afterSubmit.present === true, 'the form stays open after a client-blocked submit', afterSubmit);
      runner.assert(afterSubmit.saving === false, 'the blocked submit never entered a saving state', afterSubmit);
      runner.assert(
        afterSubmit.saveError === '',
        'no saveError is set — the block happened client-side, before any daemon round trip',
        afterSubmit,
      );

      const listAfter = runJSON(binary, ['automation', 'list'], daemonEnv) || [];
      runner.assert(
        listAfter.length === listBefore.length && !listAfter.some((row) => row.id === primaryID),
        'nothing reached the wire — nothing is stored after the blocked submit',
        { listBefore, listAfter },
      );
    });

    // Leg 3: a real create (using the values already sitting in the buffer
    // from leg2, corrected), then a reload through both the CLI and the
    // real form — pinning the same v2 spec-canonical contract the old
    // editor scenario pinned: the re-read YAML is a canonical rendering of
    // the saved content, not anything hand-formatted.
    let leg3Revision = null;
    let leg3SpecYAML = null;
    const promptV1 = 'Automation form proof: initial create.';
    await runner.step('leg3_create_then_reopen_canonical', async () => {
      await client.request('automation_form_set_values', {
        values: manualValues({ id: primaryID, name: primaryName, prompt: promptV1, directoryPath: fixturePath }),
      });
      await client.request('automation_form_submit');
      await pollForm(client, (state) => state.present === false, 'the create submit to close the form');
      await captureEvidenceScreenshot(runner, client, 'leg3-after-save.png');

      const shownRow = findListRow(binary, primaryID, daemonEnv);
      const shownYAML = showSpecYAML(binary, primaryID, daemonEnv);
      runner.assert(shownRow && shownRow.id === primaryID, 'the CLI shows the created definition by id', shownRow);
      runner.assert(shownRow.revision === 1, 'a fresh create lands at revision 1', shownRow);
      runner.assert(
        shownYAML.includes(`id: ${primaryID}`) && shownYAML.includes(`name: ${primaryName}`) && shownYAML.includes(promptV1),
        'the re-read YAML is the canonical rendering of the saved spec — same id/name/prompt content',
        shownYAML,
      );
      runner.assert(!shownYAML.includes('#'), 'the canonical rendering carries no comments', shownYAML);
      runner.assert(!shownYAML.includes('enabled:'), 'the canonical rendering carries no enabled key — column-only', shownYAML);
      leg3Revision = shownRow.revision;
      leg3SpecYAML = shownYAML;

      const opened = await client.request('automation_form_open', { definitionId: primaryID });
      runner.assert(opened.mode === 'edit', 'opening an existing definition starts in edit mode', opened);
      runner.assert(opened.definitionId === primaryID, 'edit mode reports the definition id being edited', opened);
      runner.assert(opened.revision === leg3Revision, "edit mode reports the definition's current revision", opened);
      runner.assert(opened.values.prompt === promptV1, 'the reopened form carries the saved prompt', opened);
      runner.assert(opened.values.id === primaryID, 'the reopened form carries the saved id', opened);
      runner.assert(opened.enabled === true, 'a fresh create is enabled by default', opened);

      const afterCancel = await client.request('automation_form_click', { button: 'cancel' });
      runner.assert(afterCancel.present === false, 'Cancel closes the reopened form back to the list', afterCancel);
    });

    // Leg 4: creating a SECOND definition that reuses primaryID's id is
    // refused. The typed id_collision error code routes to the id FIELD
    // (AutomationForm.tsx's doSave: `if (code === 'id_collision') setError('id', ...)`),
    // not the saveError banner — that routing is itself the product
    // behavior this leg pins, not an incidental detail.
    await runner.step('leg4_create_collision_refused', async () => {
      await client.request('automation_form_open', {});
      await client.request('automation_form_set_values', {
        values: manualValues({
          id: primaryID,
          name: 'Attempted collision create',
          prompt: 'Attempted collision create — must be refused.',
          directoryPath: fixturePath,
        }),
      });
      await client.request('automation_form_submit');

      const afterSubmit = await pollForm(
        client,
        (state) => Boolean(state.errors && state.errors.id),
        'an id-field error after submitting a colliding id',
      );
      runner.assert(
        afterSubmit.errors.id.includes('already exists'),
        'the id field error names the collision',
        afterSubmit,
      );
      runner.assert(
        afterSubmit.saveError === '',
        'id_collision routes to the field error, not the saveError banner',
        afterSubmit,
      );
      runner.assert(afterSubmit.present === true, 'the form stays open after a refused collision create', afterSubmit);

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

      const afterCancel = await client.request('automation_form_click', { button: 'cancel' });
      runner.assert(afterCancel.present === false, 'Cancel closes the collision attempt back to the list', afterCancel);
    });

    // Leg 5: the id_mismatch guard, reached via the bridge's set_values
    // escape hatch — a real user cannot do this, since the form renders id
    // as static text in edit mode (see the file-level comment). This proves
    // the DAEMON guard independently of the UI's own prevention.
    await runner.step('leg5_id_mismatch_guard_via_forceset', async () => {
      const reopened = await client.request('automation_form_open', { definitionId: primaryID });
      runner.assert(reopened.mode === 'edit', 'opening the shared definition for edit starts in edit mode', reopened);
      runner.assert(reopened.revision === leg3Revision, "the form's revision matches the definition's current revision", { reopened, leg3Revision });

      await client.request('automation_form_set_values', { values: { id: renamedID, idCustomized: true } });
      await client.request('automation_form_submit');

      const afterSubmit = await pollForm(
        client,
        (state) => state.saveErrorCode === 'id_mismatch',
        'the daemon id_mismatch error after a forced id change',
      );
      runner.assert(
        afterSubmit.saveError.includes('does not match'),
        'the saveError names the mismatch',
        afterSubmit,
      );
      runner.assert(afterSubmit.saveError.includes(renamedID) && afterSubmit.saveError.includes(primaryID), 'the saveError names both ids', afterSubmit);
      runner.assert(afterSubmit.present === true, 'the form stays open after the refused id-change save', afterSubmit);
      runner.assert(afterSubmit.mode === 'edit', 'the form remains in edit mode for the original definition', afterSubmit);

      const listAfter = runJSON(binary, ['automation', 'list'], daemonEnv) || [];
      runner.assert(!listAfter.some((row) => row.id === renamedID), 'no definition was created under the renamed id', listAfter);
      runner.assert(
        listAfter.filter((row) => row.id === primaryID).length === 1,
        'the original still exists exactly once',
        listAfter,
      );

      const afterCancel = await client.request('automation_form_click', { button: 'cancel' });
      runner.assert(afterCancel.present === false, 'Cancel closes the mismatch attempt back to the list', afterCancel);
    });

    // Leg 6: a Save against a stale revision — the definition changed out
    // from under the open form via the CLI — is refused with
    // saveErrorCode 'revision_conflict', and Reload recovers by pulling the
    // current content into the form, after which a normal Save succeeds.
    let outOfBandRevision = null;
    const localPromptA = 'Local form edit before the out-of-band mutation lands.';
    const outOfBandPrompt = 'Mutated out of band via the bundled CLI while the form was open.';
    await runner.step('leg6_stale_revision_and_reload', async () => {
      const opened = await client.request('automation_form_open', { definitionId: primaryID });
      runner.assert(opened.mode === 'edit', 'opening the shared definition for edit starts in edit mode', opened);
      runner.assert(opened.revision === leg3Revision, 'the form holds the current revision before the out-of-band apply', opened);

      const outOfBandFile = path.join(runner.sessionDir, 'out-of-band.yml');
      fs.writeFileSync(
        outOfBandFile,
        `api_version: attn.dev/automations/v1alpha1\nid: ${primaryID}\nname: ${primaryName}\ntrigger:\n  type: manual\nprompt: ${JSON.stringify(outOfBandPrompt)}\nlaunch:\n  driver: codex\nlocation:\n  type: directory\n  path: ${JSON.stringify(fixturePath)}\n`,
      );
      run(binary, ['automation', 'apply', '--file', outOfBandFile], daemonEnv);

      const outOfBandRow = findListRow(binary, primaryID, daemonEnv);
      runner.assert(outOfBandRow.revision > leg3Revision, 'the out-of-band CLI apply bumps the revision past what the open form has', outOfBandRow);
      outOfBandRevision = outOfBandRow.revision;

      await client.request('automation_form_set_values', { values: { prompt: localPromptA } });
      await client.request('automation_form_submit');
      const afterStaleSave = await pollForm(
        client,
        (state) => state.saveErrorCode === 'revision_conflict',
        'the daemon revision_conflict error after a stale save',
      );
      runner.assert(afterStaleSave.present === true, 'the form stays open after the refused stale-revision save', afterStaleSave);

      await client.request('automation_form_click', { button: 'reload' });
      const afterReload = await pollForm(
        client,
        (state) => state.revision === outOfBandRevision && state.saveError === '',
        'Reload to pull in the current (out-of-band) revision',
      );
      runner.assert(afterReload.values.prompt === outOfBandPrompt, 'Reload pulls in the current (out-of-band) content', afterReload);

      await client.request('automation_form_set_values', { values: { prompt: localPromptA } });
      await client.request('automation_form_submit');
      await pollForm(client, (state) => state.present === false, 'the recovered save to succeed and close the form');

      const finalRow = findListRow(binary, primaryID, daemonEnv);
      const finalYAML = showSpecYAML(binary, primaryID, daemonEnv);
      runner.assert(finalRow.revision === outOfBandRevision + 1, 'the recovered save bumps the revision once more', finalRow);
      runner.assert(finalYAML.includes(localPromptA), 'the recovered save persists the locally re-applied content', finalYAML);
    });

    // Leg 7: v2 revision semantics (store.UpsertAutomationDefinition —
    // revision bumps exactly when spec_json changes), unaffected by PR6.
    // Part A: resubmitting the exact same loaded values reapplies an
    // IDENTICAL spec_json — a no-op, revision unchanged. Part B: a real
    // semantic edit (renaming) DOES bump revision by exactly one, proving
    // part A's unchanged revision is content-specific, not because saves
    // stopped bumping revision at all.
    await runner.step('leg7_unchanged_resave_is_noop_then_edit_bumps', async () => {
      const rowBefore = findListRow(binary, primaryID, daemonEnv);

      const opened = await client.request('automation_form_open', { definitionId: primaryID });
      runner.assert(opened.revision === rowBefore.revision, "the form's revision matches the CLI's independently-read revision", { opened, rowBefore });

      await client.request('automation_form_submit');
      await pollForm(client, (state) => state.present === false, 'the unchanged resubmit to succeed and close the form');

      const rowAfterNoop = findListRow(binary, primaryID, daemonEnv);
      runner.assert(
        rowAfterNoop.revision === rowBefore.revision,
        'v2: resubmitting unchanged values is a no-op for revision — spec_json is identical',
        { before: rowBefore.revision, after: rowAfterNoop.revision },
      );

      const renamedName = `${primaryName} (renamed)`;
      await client.request('automation_form_open', { definitionId: primaryID });
      await client.request('automation_form_set_values', { values: { name: renamedName } });
      await client.request('automation_form_submit');
      await pollForm(client, (state) => state.present === false, 'the semantic edit to save and close the form');

      const rowAfterSemantic = findListRow(binary, primaryID, daemonEnv);
      runner.assert(
        rowAfterSemantic.revision === rowBefore.revision + 1,
        'v2: a real semantic edit (name change) bumps revision by exactly one',
        { before: rowBefore.revision, after: rowAfterSemantic.revision },
      );
      runner.assert(rowAfterSemantic.name === renamedName, 'the renamed name is what got stored', rowAfterSemantic);
    });

    // Leg 8: a GitHub-trigger create/reload round trip, pinning that the
    // form's own re-canonicalization of a spec it did not itself author
    // (repositories.mode and any other daemon-added keys) parses cleanly
    // through specToFormValues, and that resubmitting the reopened,
    // unchanged form does not manufacture a spurious revision bump merely
    // because the daemon's canonical JSON differs syntactically from what
    // the form emitted.
    const githubID = `automation-form-github-${suffix}`;
    await runner.step('leg8_github_roundtrip_no_spurious_bump', async () => {
      await client.request('automation_form_open', {});
      await client.request('automation_form_set_values', {
        values: githubValues({
          id: githubID,
          name: `Automation form github proof ${suffix}`,
          prompt: 'Review PRs that request this automation as a reviewer.',
          repositoriesInclude: ['github.com/acme/widgets'],
        }),
      });
      await client.request('automation_form_submit');
      await pollForm(client, (state) => state.present === false, 'the github-trigger create to save and close the form');

      const shownYAML = showSpecYAML(binary, githubID, daemonEnv);
      runner.assert(shownYAML.includes('github.com/acme/widgets'), 'the stored YAML carries the included repository', shownYAML);
      const rowAfterCreate = findListRow(binary, githubID, daemonEnv);
      runner.assert(rowAfterCreate.revision === 1, 'the github create lands at revision 1', rowAfterCreate);

      const reopened = await client.request('automation_form_open', { definitionId: githubID });
      runner.assert(reopened.values.trigger === 'github_review_requested', 'the reopened form reports the github trigger', reopened);
      runner.assert(
        JSON.stringify(reopened.values.repositoriesInclude) === JSON.stringify(['github.com/acme/widgets']),
        'the reopened form deep-equals the saved include list — the canonical spec_json (which may carry daemon-added keys like repositories.mode) round-trips through specToFormValues without error',
        reopened,
      );

      await client.request('automation_form_submit');
      await pollForm(client, (state) => state.present === false, 'the unchanged github resubmit to succeed and close the form');

      const rowAfterResave = findListRow(binary, githubID, daemonEnv);
      runner.assert(
        rowAfterResave.revision === 1,
        "the daemon's canonicalization differences between form-emitted and stored JSON must not manufacture a revision bump",
        rowAfterResave,
      );

      run(binary, ['automation', 'delete', githubID], daemonEnv);
    });

    // Leg 9: a definition deleted out of band while a form has it open must
    // not be resurrected by that form's Save.
    const staleEditPrompt = 'Edited after the definition was deleted elsewhere — this save must be refused.';
    await runner.step('leg9_delete_elsewhere_then_save_refused', async () => {
      const rowBefore = findListRow(binary, primaryID, daemonEnv);
      runner.assert(rowBefore && rowBefore.id === primaryID, 'sanity: the shared definition is still live before this leg deletes it', rowBefore);

      const opened = await client.request('automation_form_open', { definitionId: primaryID });
      runner.assert(opened.revision === rowBefore.revision, "the form holds the definition's current revision before the out-of-band delete", opened);

      run(binary, ['automation', 'delete', primaryID], daemonEnv);
      const listAfterDelete = runJSON(binary, ['automation', 'list'], daemonEnv) || [];
      runner.assert(!listAfterDelete.some((row) => row.id === primaryID), 'sanity: the out-of-band CLI delete removes the definition while the form is still open on it', listAfterDelete);

      await client.request('automation_form_set_values', { values: { prompt: staleEditPrompt } });
      await client.request('automation_form_submit');
      const afterSave = await pollForm(
        client,
        (state) => state.saveErrorCode === 'deleted_elsewhere',
        'the daemon deleted_elsewhere error after a save against a deleted definition',
      );
      runner.assert(afterSave.present === true, "the form stays open after a refused save — the user's in-progress edit is not destroyed", afterSave);
      runner.assert(afterSave.saveError.toLowerCase().includes('deleted'), 'the saveError mentions the deletion', afterSave);

      runner.assert(showFailsWithDaemonError(binary, primaryID, daemonEnv), 'the CLI still reports the definition as gone — it was not resurrected', primaryID);

      await client.request('automation_form_click', { button: 'cancel' });
    });

    // Leg 10: `enabled` remains column-only post-#629, untouched by PR6. The
    // panel toggle flips the column without bumping revision in either
    // direction, and a form Save never touches it — same invariant the old
    // editor scenario pinned, now proven for the form's own enabled toggle
    // (AutomationForm.tsx's header switch) as well as the panel's.
    const toggleID = `automation-form-toggle-${suffix}`;
    await runner.step('leg10_panel_toggle_and_form_reflects_column', async () => {
      await client.request('automation_form_open', {});
      await client.request('automation_form_set_values', {
        values: manualValues({
          id: toggleID,
          name: `Automation form toggle proof ${suffix}`,
          prompt: 'Definition that the operator disables from the panel and then edits.',
          directoryPath: fixturePath,
        }),
      });
      await client.request('automation_form_submit');
      await pollForm(client, (state) => state.present === false, 'the toggle-fixture create to save and close the form');

      const createdRow = findListRow(binary, toggleID, daemonEnv);
      runner.assert(createdRow && createdRow.enabled === true, 'sanity: a brand-new definition starts enabled by default', createdRow);

      await toggleEnabledAndWait(client, toggleID, false);

      const disabledRow = findListRow(binary, toggleID, daemonEnv);
      const disabledYAML = showSpecYAML(binary, toggleID, daemonEnv);
      runner.assert(disabledRow.enabled === false, 'the panel toggle disables the definition', disabledRow);
      runner.assert(disabledRow.revision === createdRow.revision, 'the toggle does NOT bump revision', { before: createdRow.revision, after: disabledRow.revision });
      runner.assert(!disabledYAML.includes('enabled:'), 'the stored YAML carries no enabled key at all', disabledYAML);

      const opened = await client.request('automation_form_open', { definitionId: toggleID });
      runner.assert(opened.enabled === false, 'the form header reflects the enabled column on load', opened);

      await client.request('automation_form_submit');
      await pollForm(client, (state) => state.present === false, 'the unrelated resubmit to save and close the form');

      const afterSaveRow = findListRow(binary, toggleID, daemonEnv);
      runner.assert(afterSaveRow.enabled === false, 'a save never touches the enabled column', afterSaveRow);
      runner.assert(afterSaveRow.revision === disabledRow.revision, 'the unrelated resave is itself a no-op revision-wise', { before: disabledRow.revision, after: afterSaveRow.revision });

      await toggleEnabledAndWait(client, toggleID, true);
      const reEnabledRow = findListRow(binary, toggleID, daemonEnv);
      runner.assert(reEnabledRow.enabled === true, 'toggling back on writes through the same way', reEnabledRow);
      runner.assert(reEnabledRow.revision === disabledRow.revision, 're-enabling also does not bump revision', { disabled: disabledRow.revision, reEnabled: reEnabledRow.revision });

      run(binary, ['automation', 'delete', toggleID], daemonEnv);
    });

    // Leg 11: the form's own two-step delete (armed, then confirmed) is the
    // end-to-end proof of the automation_delete WS path through the new UI.
    const deleteID = `automation-form-delete-${suffix}`;
    await runner.step('leg11_form_two_step_delete', async () => {
      await client.request('automation_form_open', {});
      await client.request('automation_form_set_values', {
        values: manualValues({
          id: deleteID,
          name: `Automation form delete proof ${suffix}`,
          prompt: 'Definition created only to be deleted through the form itself.',
          directoryPath: fixturePath,
        }),
      });
      await client.request('automation_form_submit');
      await pollForm(client, (state) => state.present === false, 'the delete-fixture create to save and close the form');

      await client.request('automation_form_open', { definitionId: deleteID });
      const afterArm = await client.request('automation_form_click', { button: 'delete' });
      runner.assert(afterArm.deleteArmed === true, 'the first delete click arms the confirmation, does not delete', afterArm);
      runner.assert(findListRow(binary, deleteID, daemonEnv) !== null, 'arming delete does not delete — the definition still exists', deleteID);

      await client.request('automation_form_click', { button: 'delete' });
      await pollForm(client, (state) => state.present === false, 'the confirmed delete to close the form');

      runner.assert(showFailsWithDaemonError(binary, deleteID, daemonEnv), 'the definition is gone after the confirmed delete', deleteID);
    });

    runner.finishSuccess({ profile, primaryID, renamedID, githubID, toggleID, deleteID, leg3Revision, appBuild, protocolVersion });
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
