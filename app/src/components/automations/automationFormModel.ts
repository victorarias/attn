import { z } from 'zod';
import { AutomationAgent, effortOptionsFor } from './launchCatalog';

// Headless form model for the structured automation editor (PR6). This
// mirrors the validation rules of internal/automation/automation.go's
// DefinitionSpec/ValidateDefinition so the client rejects the same shapes
// the daemon would, before a round trip. It intentionally narrows a few
// daemon-legal combinations the CLI still supports (see the manual-trigger
// note on directoryPath below) in exchange for a much simpler form.

export const AUTOMATION_API_VERSION = 'attn.dev/automations/v1alpha1';

export type AutomationTrigger = 'manual' | 'scheduled' | 'github_review_requested';

export interface AutomationRepositoryOverride {
  repository: string;
  path: string;
}

export interface AutomationFormValues {
  name: string;
  id: string;
  idCustomized: boolean; // UI-only: id follows slugFromName(name) until true
  trigger: AutomationTrigger;
  scheduleCron: string;
  continuity: 'fresh' | 'singleton';
  catchUp: '' | 'skip' | 'latest'; // '' = not chosen yet; schema rejects on scheduled
  repositoriesInclude: string[];
  repositoriesExclude: string[];
  agent: AutomationAgent;
  model: string; // resolved model id, including custom free-text
  effort: string;
  executable: string; // '' = default from PATH
  directoryPath: string;
  repositoryOverrides: AutomationRepositoryOverride[];
  prompt: string;
}

export function slugFromName(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

const ID_PATTERN = /^[a-z0-9][a-z0-9-]*$/;
const REPO_HOST_PATTERN = /^[a-z0-9][a-z0-9.-]*$/;
const REPO_COMPONENT_PATTERN = /^[a-z0-9_.-]+$/;
const REPOSITORY_MESSAGE = 'Use host/owner/repository, e.g. github.com/victorarias/attn.';
const CROSS_LIST_MESSAGE = 'This repository is both included and excluded.';
const DUPLICATE_LIST_ENTRY_MESSAGE = 'This repository is already listed.';
const ABSOLUTE_PATH_MESSAGE = 'Must be an absolute path.';

function canonicalRepositoryIdentity(raw: string): string {
  return raw.trim().toLowerCase();
}

function isValidRepositoryIdentity(raw: string): boolean {
  const identity = canonicalRepositoryIdentity(raw);
  const parts = identity.split('/');
  if (parts.length !== 3) return false;
  const [host, owner, repo] = parts;
  if (!REPO_HOST_PATTERN.test(host)) return false;
  for (const component of [owner, repo]) {
    if (component === '.' || component === '..') return false;
    if (!REPO_COMPONENT_PATTERN.test(component)) return false;
  }
  return true;
}

function isFiveFieldCron(cron: string): boolean {
  const fields = cron.trim().split(/\s+/).filter(Boolean);
  return fields.length === 5;
}

function validateRepositoryList(
  list: string[],
  field: 'repositoriesInclude' | 'repositoriesExclude',
  ctx: z.RefinementCtx,
): void {
  const seen = new Set<string>();
  list.forEach((entry, index) => {
    if (!isValidRepositoryIdentity(entry)) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: [field, index], message: REPOSITORY_MESSAGE });
      return;
    }
    const canonical = canonicalRepositoryIdentity(entry);
    if (seen.has(canonical)) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: [field, index], message: DUPLICATE_LIST_ENTRY_MESSAGE });
    }
    seen.add(canonical);
  });
}

function checkCrossListOverlap(include: string[], exclude: string[], ctx: z.RefinementCtx): void {
  const includeSet = new Set(include.filter(isValidRepositoryIdentity).map(canonicalRepositoryIdentity));
  exclude.forEach((entry, index) => {
    if (!isValidRepositoryIdentity(entry)) return; // already reported by validateRepositoryList
    const canonical = canonicalRepositoryIdentity(entry);
    if (includeSet.has(canonical)) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['repositoriesExclude', index], message: CROSS_LIST_MESSAGE });
    }
  });
}

function validateOverrides(overrides: AutomationRepositoryOverride[], ctx: z.RefinementCtx): void {
  const seen = new Set<string>();
  overrides.forEach((override, index) => {
    if (!isValidRepositoryIdentity(override.repository)) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['repositoryOverrides', index, 'repository'],
        message: REPOSITORY_MESSAGE,
      });
    } else {
      const canonical = canonicalRepositoryIdentity(override.repository);
      if (seen.has(canonical)) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['repositoryOverrides', index, 'repository'],
          message: 'This repository already has a directory override.',
        });
      }
      seen.add(canonical);
    }
    if (!override.path.startsWith('/')) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['repositoryOverrides', index, 'path'], message: ABSOLUTE_PATH_MESSAGE });
    }
  });
}

const baseFormSchema = z.object({
  name: z.string(),
  id: z.string(),
  idCustomized: z.boolean(),
  trigger: z.enum(['manual', 'scheduled', 'github_review_requested']),
  scheduleCron: z.string(),
  continuity: z.enum(['fresh', 'singleton']),
  catchUp: z.enum(['', 'skip', 'latest']),
  repositoriesInclude: z.array(z.string()),
  repositoriesExclude: z.array(z.string()),
  agent: z.enum(['codex', 'claude']),
  model: z.string(),
  effort: z.string(),
  executable: z.string(),
  directoryPath: z.string(),
  repositoryOverrides: z.array(z.object({ repository: z.string(), path: z.string() })),
  prompt: z.string(),
});

export const automationFormSchema: z.ZodType<AutomationFormValues> = baseFormSchema.superRefine((values, ctx) => {
  if (values.name.trim() === '') {
    ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['name'], message: 'A name is required.' });
  }
  if (!ID_PATTERN.test(values.id)) {
    ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['id'], message: 'ID must be a lowercase slug (a–z, 0–9, dashes).' });
  }
  if (values.prompt.trim() === '') {
    ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['prompt'], message: 'A prompt is required.' });
  }

  // model '' and effort '' both mean "use the agent's default" — the daemon
  // treats launch.model/launch.effort as optional, so a CLI-authored
  // definition that omits them (an ordinary shape) must stay editable here,
  // not get stuck on a client-side requirement the daemon never had.
  if (values.effort !== '') {
    const { efforts } = effortOptionsFor(values.agent, values.model);
    if (!efforts.includes(values.effort)) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['effort'], message: "Effort isn't available for this model." });
    }
  }

  switch (values.trigger) {
    case 'manual': {
      // Deliberate simplification: the form only offers a directory
      // location for manual triggers. The CLI/YAML path can still express
      // manual + repository_worktree; editing such a definition through
      // the form is out of scope for this pass.
      if (!values.directoryPath.startsWith('/')) {
        ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['directoryPath'], message: ABSOLUTE_PATH_MESSAGE });
      }
      break;
    }
    case 'scheduled': {
      if (!isFiveFieldCron(values.scheduleCron)) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['scheduleCron'],
          message: 'Enter a valid 5-field cron expression.',
        });
      }
      if (values.catchUp !== 'skip' && values.catchUp !== 'latest') {
        ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['catchUp'], message: 'Choose what happens to missed runs.' });
      }
      if (!values.directoryPath.startsWith('/')) {
        ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['directoryPath'], message: ABSOLUTE_PATH_MESSAGE });
      }
      break;
    }
    case 'github_review_requested': {
      validateRepositoryList(values.repositoriesInclude, 'repositoriesInclude', ctx);
      validateRepositoryList(values.repositoriesExclude, 'repositoriesExclude', ctx);
      checkCrossListOverlap(values.repositoriesInclude, values.repositoriesExclude, ctx);
      validateOverrides(values.repositoryOverrides, ctx);
      break;
    }
  }
});

function buildLaunch(values: AutomationFormValues): Record<string, unknown> {
  const launch: Record<string, unknown> = { driver: values.agent };
  const model = values.model.trim();
  const executable = values.executable.trim();
  if (model !== '') launch.model = model;
  if (values.effort !== '') launch.effort = values.effort;
  if (executable !== '') launch.executable = executable;
  return launch;
}

function buildTrigger(values: AutomationFormValues): Record<string, unknown> {
  switch (values.trigger) {
    case 'manual':
      return { type: 'manual' };
    case 'scheduled':
      // No time_zone key ever: schedules run in the machine's local
      // timezone.
      return {
        type: 'scheduled',
        schedule: { cron: values.scheduleCron.trim() },
        continuity: values.continuity,
        catch_up: values.catchUp,
      };
    case 'github_review_requested': {
      const trigger: Record<string, unknown> = { type: 'github_review_requested' };
      const include = dedupeCanonical(values.repositoriesInclude);
      const exclude = dedupeCanonical(values.repositoriesExclude);
      if (include.length > 0 || exclude.length > 0) {
        const repositories: Record<string, unknown> = {};
        if (include.length > 0) repositories.include = include;
        if (exclude.length > 0) repositories.exclude = exclude;
        trigger.repositories = repositories;
      }
      return trigger;
    }
  }
}

function dedupeCanonical(entries: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const entry of entries) {
    const canonical = canonicalRepositoryIdentity(entry);
    if (seen.has(canonical)) continue;
    seen.add(canonical);
    out.push(canonical);
  }
  return out;
}

function buildLocation(values: AutomationFormValues): Record<string, unknown> {
  if (values.trigger === 'github_review_requested') {
    const overrides: Record<string, unknown> = {};
    for (const override of values.repositoryOverrides) {
      overrides[canonicalRepositoryIdentity(override.repository)] = { type: 'local_clone', path: override.path };
    }
    const repositorySources: Record<string, unknown> = { default: { type: 'managed_cache' } };
    if (Object.keys(overrides).length > 0) repositorySources.overrides = overrides;
    return { type: 'repository_worktree', repository_sources: repositorySources };
  }
  return { type: 'directory', path: values.directoryPath };
}

// formValuesToSpec renders a DefinitionSpec-shaped plain object (snake_case
// keys, optional keys omitted when empty, matching Go's json omitempty),
// ready for JSON.stringify.
export function formValuesToSpec(values: AutomationFormValues): Record<string, unknown> {
  return {
    api_version: AUTOMATION_API_VERSION,
    id: values.id,
    name: values.name,
    trigger: buildTrigger(values),
    prompt: values.prompt,
    launch: buildLaunch(values),
    location: buildLocation(values),
  };
}

export function specJSONString(values: AutomationFormValues): string {
  return JSON.stringify(formValuesToSpec(values));
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' ? (value as Record<string, unknown>) : {};
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function asStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === 'string') : [];
}

// specToFormValues parses canonical spec JSON (the daemon's spec_json) into
// form values. It throws a readable Error on any shape the form cannot
// edit, rather than producing a silently-wrong form.
export function specToFormValues(specJson: string): AutomationFormValues {
  let raw: unknown;
  try {
    raw = JSON.parse(specJson);
  } catch {
    throw new Error('Automation spec is not valid JSON.');
  }
  const root = asRecord(raw);
  if (root.api_version !== AUTOMATION_API_VERSION) {
    throw new Error(`Unrecognized automation spec api_version ${JSON.stringify(root.api_version)}.`);
  }

  const launch = asRecord(root.launch);
  const driver = launch.driver;
  if (driver !== 'codex' && driver !== 'claude') {
    throw new Error(
      `This automation uses launch driver ${JSON.stringify(driver)}, which the form cannot edit. Use the CLI for this definition.`,
    );
  }

  const name = asString(root.name);
  const id = asString(root.id);
  const prompt = asString(root.prompt);

  const trigger = asRecord(root.trigger);
  const triggerType = trigger.type;

  let directoryPath = '';
  let scheduleCron = '';
  let continuity: 'fresh' | 'singleton' = 'fresh';
  let catchUp: '' | 'skip' | 'latest' = '';
  let repositoriesInclude: string[] = [];
  let repositoriesExclude: string[] = [];
  let repositoryOverrides: AutomationRepositoryOverride[] = [];

  switch (triggerType) {
    case 'manual': {
      const location = asRecord(root.location);
      if (location.type !== 'directory') {
        throw new Error('This automation uses a location the form cannot edit. Use the CLI for this definition.');
      }
      directoryPath = asString(location.path);
      break;
    }
    case 'scheduled': {
      const scheduledLocation = asRecord(root.location);
      if (scheduledLocation.type !== 'directory') {
        throw new Error('This automation uses a location the form cannot edit. Use the CLI for this definition.');
      }
      directoryPath = asString(scheduledLocation.path);
      const schedule = asRecord(trigger.schedule);
      scheduleCron = asString(schedule.cron);
      continuity = trigger.continuity === 'singleton' ? 'singleton' : 'fresh';
      catchUp = trigger.catch_up === 'skip' || trigger.catch_up === 'latest' ? trigger.catch_up : '';
      break;
    }
    case 'github_review_requested': {
      const repositories = asRecord(trigger.repositories);
      repositoriesInclude = asStringArray(repositories.include);
      repositoriesExclude = asStringArray(repositories.exclude);
      const location = asRecord(root.location);
      const sources = asRecord(location.repository_sources);
      const overrides = asRecord(sources.overrides);
      repositoryOverrides = Object.entries(overrides).map(([repository, source]) => ({
        repository,
        path: asString(asRecord(source).path),
      }));
      break;
    }
    default:
      throw new Error(`Unrecognized automation trigger type ${JSON.stringify(triggerType)}.`);
  }

  return {
    name,
    id,
    idCustomized: id !== slugFromName(name),
    trigger: triggerType as AutomationTrigger,
    scheduleCron,
    continuity,
    catchUp,
    repositoriesInclude,
    repositoriesExclude,
    agent: driver,
    model: asString(launch.model),
    effort: asString(launch.effort),
    executable: asString(launch.executable),
    directoryPath,
    repositoryOverrides,
    prompt,
  };
}
