import { describe, it, expect } from 'vitest';
import {
  AutomationFormValues,
  automationFormSchema,
  formValuesToSpec,
  slugFromName,
  specToFormValues,
} from './automationFormModel';
import { effortOptionsFor } from './launchCatalog';

function baseValues(overrides: Partial<AutomationFormValues> = {}): AutomationFormValues {
  return {
    name: 'PR reviewer',
    id: 'pr-reviewer',
    idCustomized: false,
    trigger: 'manual',
    scheduleCron: '',
    continuity: 'fresh',
    catchUp: '',
    repositoriesInclude: [],
    repositoriesExclude: [],
    agent: 'codex',
    model: 'gpt-5.5',
    effort: 'medium',
    executable: '',
    directoryPath: '/Users/victor/projects/victor/attn',
    repositoryOverrides: [],
    prompt: 'Review the diff.',
    ...overrides,
  };
}

const manualFixture = baseValues();

const scheduledFixture = baseValues({
  name: 'Nightly cleanup',
  id: 'nightly-cleanup',
  trigger: 'scheduled',
  scheduleCron: '0 3 * * *',
  continuity: 'singleton',
  catchUp: 'skip',
  agent: 'claude',
  model: 'sonnet',
  effort: 'medium',
  prompt: 'Run nightly cleanup.',
});

const githubFixture = baseValues({
  name: 'PR pre-review',
  id: 'pr-pre-review',
  trigger: 'github_review_requested',
  directoryPath: '',
  repositoriesInclude: ['GitHub.com/VictorArias/Attn', 'github.com/victorarias/attn-web'],
  repositoriesExclude: ['github.com/victorarias/attn-private'],
  agent: 'codex',
  model: 'gpt-5.4-codex',
  effort: 'high',
  executable: '/usr/local/bin/codex',
  repositoryOverrides: [
    { repository: 'GitHub.com/VictorArias/Attn', path: '/Users/victor/projects/victor/attn' },
  ],
  prompt: 'Review the pull request.',
});

describe('round trip: formValuesToSpec -> JSON -> specToFormValues -> formValuesToSpec', () => {
  it('manual trigger round-trips and omits schedule/repositories/model-optional keys', () => {
    const spec = formValuesToSpec(manualFixture);
    const parsedValues = specToFormValues(JSON.stringify(spec));
    const respec = formValuesToSpec(parsedValues);
    expect(respec).toEqual(spec);

    expect(spec.trigger).not.toHaveProperty('schedule');
    expect(spec.trigger).not.toHaveProperty('repositories');
    expect(spec.trigger).not.toHaveProperty('continuity');
    expect(spec.trigger).not.toHaveProperty('catch_up');
    expect((spec.location as Record<string, unknown>).type).toBe('directory');
  });

  it('scheduled trigger round-trips, never carries time_zone, and omits empty executable/model keys when absent', () => {
    const values = baseValues({
      ...scheduledFixture,
      model: '',
      effort: '',
      executable: '',
    });
    const spec = formValuesToSpec(values);
    const parsedValues = specToFormValues(JSON.stringify(spec));
    const respec = formValuesToSpec(parsedValues);
    expect(respec).toEqual(spec);

    const schedule = (spec.trigger as Record<string, unknown>).schedule as Record<string, unknown>;
    expect(schedule).not.toHaveProperty('time_zone');
    expect(spec.launch).not.toHaveProperty('model');
    expect(spec.launch).not.toHaveProperty('effort');
    expect(spec.launch).not.toHaveProperty('executable');
  });

  it('github trigger with include, exclude, and overrides round-trips and canonicalizes identities', () => {
    const spec = formValuesToSpec(githubFixture);
    const parsedValues = specToFormValues(JSON.stringify(spec));
    const respec = formValuesToSpec(parsedValues);
    expect(respec).toEqual(spec);

    const trigger = spec.trigger as Record<string, unknown>;
    const repositories = trigger.repositories as Record<string, unknown>;
    expect(repositories.include).toEqual(['github.com/victorarias/attn', 'github.com/victorarias/attn-web']);
    expect(repositories.exclude).toEqual(['github.com/victorarias/attn-private']);

    const location = spec.location as Record<string, unknown>;
    expect(location.type).toBe('repository_worktree');
    const sources = location.repository_sources as Record<string, unknown>;
    const overrides = sources.overrides as Record<string, unknown>;
    expect(Object.keys(overrides)).toEqual(['github.com/victorarias/attn']);
  });

  it('omits the repositories key entirely on github trigger when both lists are empty', () => {
    const spec = formValuesToSpec(baseValues({ trigger: 'github_review_requested', directoryPath: '' }));
    expect(spec.trigger).not.toHaveProperty('repositories');
  });
});

describe('validation matrix', () => {
  function issuePaths(values: AutomationFormValues): string[] {
    const result = automationFormSchema.safeParse(values);
    if (result.success) return [];
    return result.error.issues.map((issue) => issue.path.join('.'));
  }

  it('rejects an empty name', () => {
    expect(issuePaths(baseValues({ name: '   ' }))).toContain('name');
  });

  it('rejects a bad id slug', () => {
    expect(issuePaths(baseValues({ id: 'Not A Slug' }))).toContain('id');
  });

  it('rejects scheduled trigger with catchUp unset', () => {
    expect(
      issuePaths(baseValues({ trigger: 'scheduled', scheduleCron: '0 3 * * *', catchUp: '', continuity: 'fresh' })),
    ).toContain('catchUp');
  });

  it('rejects a 4-field cron expression', () => {
    expect(
      issuePaths(baseValues({ trigger: 'scheduled', scheduleCron: '0 3 * *', catchUp: 'latest', continuity: 'fresh' })),
    ).toContain('scheduleCron');
  });

  it('rejects a relative directoryPath', () => {
    expect(issuePaths(baseValues({ directoryPath: 'relative/path' }))).toContain('directoryPath');
  });

  it('rejects a malformed github repository entry', () => {
    const paths = issuePaths(
      baseValues({ trigger: 'github_review_requested', directoryPath: '', repositoriesInclude: ['not-a-repo'] }),
    );
    expect(paths).toContain('repositoriesInclude.0');
  });

  it('rejects a duplicate entry within a single list with a distinct message from cross-list overlap', () => {
    const result = automationFormSchema.safeParse(
      baseValues({
        trigger: 'github_review_requested',
        directoryPath: '',
        repositoriesInclude: ['github.com/victorarias/attn', 'github.com/victorarias/attn'],
      }),
    );
    expect(result.success).toBe(false);
    if (result.success) return;
    const issue = result.error.issues.find((candidate) => candidate.path.join('.') === 'repositoriesInclude.1');
    expect(issue?.message).toBe('This repository is already listed.');
  });

  it('rejects the same repository present in both include and exclude', () => {
    const paths = issuePaths(
      baseValues({
        trigger: 'github_review_requested',
        directoryPath: '',
        repositoriesInclude: ['github.com/victorarias/attn'],
        repositoriesExclude: ['github.com/victorarias/attn'],
      }),
    );
    expect(paths).toContain('repositoriesExclude.0');
  });

  it('rejects duplicate override repositories', () => {
    const paths = issuePaths(
      baseValues({
        trigger: 'github_review_requested',
        directoryPath: '',
        repositoryOverrides: [
          { repository: 'github.com/victorarias/attn', path: '/a' },
          { repository: 'github.com/victorarias/attn', path: '/b' },
        ],
      }),
    );
    expect(paths).toContain('repositoryOverrides.1.repository');
  });

  it('rejects an empty prompt', () => {
    expect(issuePaths(baseValues({ prompt: '  ' }))).toContain('prompt');
  });

  it('rejects an effort not offered for the chosen model', () => {
    expect(issuePaths(baseValues({ agent: 'codex', model: 'gpt-5.4-mini', effort: 'xhigh' }))).toContain('effort');
  });

  it('accepts an empty model and empty effort — both mean the agent default', () => {
    expect(issuePaths(baseValues({ model: '', effort: '' }))).not.toContain('model');
    expect(issuePaths(baseValues({ model: '', effort: '' }))).not.toContain('effort');
  });

  it('accepts a nonempty preset model with an empty (agent-default) effort', () => {
    expect(issuePaths(baseValues({ model: 'gpt-5.5', effort: '' }))).not.toContain('effort');
  });

  it('rejects an empty model with an effort outside the agent-default custom list', () => {
    expect(issuePaths(baseValues({ model: '', effort: 'nonsense' }))).toContain('effort');
  });

  it('accepts one fully-valid fixture per trigger', () => {
    expect(automationFormSchema.safeParse(manualFixture).success).toBe(true);
    expect(automationFormSchema.safeParse(scheduledFixture).success).toBe(true);
    expect(automationFormSchema.safeParse(githubFixture).success).toBe(true);
  });
});

describe('slugFromName', () => {
  it('lowercases and collapses non-alphanumeric runs into single dashes', () => {
    expect(slugFromName('Clean merged worktrees')).toBe('clean-merged-worktrees');
  });

  it('trims leading and trailing dashes produced by punctuation', () => {
    expect(slugFromName(' PR pre-review! ')).toBe('pr-pre-review');
  });

  it('reduces an all-punctuation name to an empty string', () => {
    expect(slugFromName('---')).toBe('');
  });
});

describe('specToFormValues', () => {
  it('defaults continuity to fresh when the scheduled trigger omits the continuity key', () => {
    const specJson = JSON.stringify({
      api_version: 'attn.dev/automations/v1alpha1',
      id: 'nightly-cleanup',
      name: 'Nightly cleanup',
      trigger: { type: 'scheduled', schedule: { cron: '0 3 * * *' }, catch_up: 'latest' },
      prompt: 'Run nightly cleanup.',
      launch: { driver: 'codex' },
      location: { type: 'directory', path: '/repo' },
    });
    expect(specToFormValues(specJson).continuity).toBe('fresh');
  });

  it('throws with a use-the-CLI message for a manual trigger whose location is not directory', () => {
    const specJson = JSON.stringify({
      api_version: 'attn.dev/automations/v1alpha1',
      id: 'legacy-manual',
      name: 'Legacy manual',
      trigger: { type: 'manual' },
      prompt: 'Do the thing.',
      launch: { driver: 'codex' },
      location: { type: 'repository_worktree', repository_sources: { default: { type: 'managed_cache' } } },
    });
    expect(() => specToFormValues(specJson)).toThrow(/use the cli/i);
  });

  it('throws with a use-the-CLI message when the spec uses an unsupported driver', () => {
    const specJson = JSON.stringify({
      api_version: 'attn.dev/automations/v1alpha1',
      id: 'legacy',
      name: 'Legacy',
      trigger: { type: 'manual' },
      prompt: 'Do the thing.',
      launch: { driver: 'copilot' },
      location: { type: 'directory', path: '/repo' },
    });
    expect(() => specToFormValues(specJson)).toThrow(/use the cli/i);
  });

  it('infers idCustomized from whether id matches slugFromName(name)', () => {
    const matching = JSON.stringify({
      api_version: 'attn.dev/automations/v1alpha1',
      id: 'pr-reviewer',
      name: 'PR reviewer',
      trigger: { type: 'manual' },
      prompt: 'Review it.',
      launch: { driver: 'codex' },
      location: { type: 'directory', path: '/repo' },
    });
    const customized = JSON.stringify({
      api_version: 'attn.dev/automations/v1alpha1',
      id: 'my-custom-id',
      name: 'PR reviewer',
      trigger: { type: 'manual' },
      prompt: 'Review it.',
      launch: { driver: 'codex' },
      location: { type: 'directory', path: '/repo' },
    });
    expect(specToFormValues(matching).idCustomized).toBe(false);
    expect(specToFormValues(customized).idCustomized).toBe(true);
  });
});

describe('effortOptionsFor', () => {
  it('returns the model-specific list for a known model', () => {
    expect(effortOptionsFor('codex', 'gpt-5.4-mini').efforts).toEqual(['minimal', 'low', 'medium', 'high']);
    expect(effortOptionsFor('claude', 'haiku').efforts).toEqual(['low', 'medium', 'high']);
  });

  it('falls back to the agent custom entry for an unrecognized model id', () => {
    expect(effortOptionsFor('codex', 'some-custom-model')).toEqual({
      efforts: ['minimal', 'low', 'medium', 'high', 'xhigh'],
      defaultEffort: 'medium',
    });
  });

  it('falls back to the agent custom entry for an empty model id', () => {
    expect(effortOptionsFor('claude', '')).toEqual({
      efforts: ['low', 'medium', 'high', 'xhigh', 'max'],
      defaultEffort: 'medium',
    });
  });
});
