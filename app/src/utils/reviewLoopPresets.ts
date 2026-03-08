export interface ReviewLoopPreset {
  id: string;
  name: string;
  prompt: string;
  iterationLimit: number;
  builtin: boolean;
}

export const REVIEW_LOOP_SETTINGS_CUSTOM_PRESETS = 'review_loop_prompt_presets';
export const REVIEW_LOOP_SETTINGS_LAST_PRESET = 'review_loop_last_preset';
export const REVIEW_LOOP_SETTINGS_LAST_PROMPT = 'review_loop_last_prompt';
export const REVIEW_LOOP_SETTINGS_LAST_ITERATIONS = 'review_loop_last_iterations';

export const BUILTIN_REVIEW_LOOP_PRESETS: ReviewLoopPreset[] = [
  {
    id: 'full-review-fix',
    name: 'Full Review + Fix',
    iterationLimit: 3,
    builtin: true,
    prompt: `You are running a multi-pass review loop for this changeset.

Use three review subagents, each focused on a distinct lens:
1. implementation reviewer
2. system architect
3. pragmatic security engineer

Instructions for the subagents:
- each subagent should review only
- they should identify bugs, regressions, edge cases, design issues, security risks, and polish opportunities
- they should not make code changes
- they should give concrete, actionable feedback with rationale

Instructions for you, the main agent:
- you are the only agent that makes code changes
- collect the subagent feedback, critically evaluate it, and decide what to act on
- do not blindly accept every suggestion
- do not casually dismiss polish, minor, or low-severity items; we are aiming for a polished changeset
- fix anything that is safe, local, and clearly improves the result
- if reviewers disagree, or if a change needs product judgment or has meaningful tradeoffs, stop and ask one concrete question
- do not make speculative refactors unrelated to the reviewed changes

Your goal is a polished final changeset:
- correct
- coherent
- well-reviewed
- cleaned up beyond just "it works"

At the end, return a concise markdown summary for the UI with:
- What changed
- What was verified
- Remaining risks or open questions

Keep the summary readable in the app:
- short paragraphs
- bullets where useful
- preserve line breaks
- do not collapse everything into one long paragraph`,
  },
  {
    id: 'pr-polish',
    name: 'PR Polish',
    iterationLimit: 2,
    builtin: true,
    prompt: `Review this changeset with three review subagents:
1. implementation reviewer
2. system architect
3. pragmatic security engineer

The subagents review only. You are the only agent that changes code.

Critically evaluate their feedback and fix anything safe, local, and clearly worthwhile.
Do not dismiss polish or minor items just because they are small.
Aim for a polished PR, not just basic correctness.
If a decision needs product judgment or meaningful tradeoffs, stop and ask one concrete question.

Return a concise markdown summary for the UI with what changed, what was verified, and any remaining risks or open questions.`,
  },
];

const LEGACY_ADVANCE_PATTERNS = [
  /(?:\s+|^)(?:When you believe this pass is complete|When this pass is complete),?\s*run:\s*attn review-loop advance --session \{\{session_id\}\} --token \{\{advance_token\}\}\s*$/i,
  /(?:\s+|^)attn review-loop advance --session \{\{session_id\}\} --token \{\{advance_token\}\}\s*$/i,
];

export function stripLegacyReviewLoopAdvanceInstruction(prompt: string): string {
  let cleaned = prompt.trim();
  for (const pattern of LEGACY_ADVANCE_PATTERNS) {
    cleaned = cleaned.replace(pattern, '').trim();
  }
  return cleaned;
}

export function parseSavedReviewLoopPresets(raw: string | undefined): ReviewLoopPreset[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    const mapped: Array<ReviewLoopPreset | null> = parsed.map((item) => {
      if (!item || typeof item !== 'object') return null;
      const record = item as Record<string, unknown>;
      const id = typeof record.id === 'string' ? record.id.trim() : '';
      const name = typeof record.name === 'string' ? record.name.trim() : '';
      const prompt = typeof record.prompt === 'string' ? stripLegacyReviewLoopAdvanceInstruction(record.prompt) : '';
      const iterationLimit = typeof record.iterationLimit === 'number' && record.iterationLimit > 0
        ? Math.floor(record.iterationLimit)
        : 1;
      if (!id || !name || !prompt) return null;
      return { id, name, prompt, iterationLimit, builtin: false };
    });
    return mapped.filter((item): item is ReviewLoopPreset => item !== null);
  } catch {
    return [];
  }
}

export function serializeSavedReviewLoopPresets(presets: ReviewLoopPreset[]): string {
  return JSON.stringify(
    presets
      .filter((preset) => !preset.builtin)
      .map((preset) => ({
        id: preset.id,
        name: preset.name,
        prompt: preset.prompt,
        iterationLimit: preset.iterationLimit,
      }))
  );
}

export function buildCustomReviewLoopPresetID(name: string): string {
  const base = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/(^-|-$)/g, '') || 'custom-review-loop';
  return `custom-${base}`;
}
