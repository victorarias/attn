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
    prompt: 'Do a full review of these changes. Use subagents: one implementation-focused reviewer and one system architect. Fix everything you find, including polish items, unless the tradeoff is unclear. If something is ambiguous or risky, stop and ask me.',
  },
  {
    id: 'pr-polish',
    name: 'PR Polish',
    iterationLimit: 2,
    builtin: true,
    prompt: 'Do a full review of these changes. Use subagents: one implementation-focused reviewer and one system architect. Fix bugs, design issues, and polish items. Aim for a strong PR, not just correctness. If a change needs product judgment or is risky, stop and ask me.',
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
