// The config value the decision tree is evaluated against, and the loader that
// validates one. Patterns match a call's signature (policy.ts's callSignature):
// `*` matches any run of characters, `?` matches one.

/** Defaults from the classifier receipt in docs/plans/2026-08-16-pi-auto-mode.md. */
export const defaultClassifierModel = "opencode-go/glm-5.3";
export const defaultEscalationModel = "opencode-go/qwen3.8-max";

/** Primary first, the rest walked on an unreachable endpoint. Receipts only. */
export const defaultClassifierModels: readonly string[] = [defaultClassifierModel];
export const defaultEscalationModels: readonly string[] = [defaultEscalationModel];

export type AutoModeConfig = {
  /** Whether new sessions start with auto mode on. */
  enabledDefault: boolean;
  /** Prose the classifier reads to learn what this machine is allowed to do. */
  environment: readonly string[];
  /** Narrow patterns that skip the classifier and run. */
  allow: readonly string[];
  /** Patterns refused before anything else looks at the call. No approval lifts one. */
  hardDeny: readonly string[];
  /** Layer 2a's models, primary first. Never empty. */
  classifierModels: readonly string[];
  /** Layer 2b's models, primary first. Never empty. */
  escalationModels: readonly string[];
};

/** The on-the-wire/on-disk shape, snake_case per the plan's schema. */
export type RawAutoModeConfig = {
  enabled_default?: unknown;
  environment?: unknown;
  allow?: unknown;
  hard_deny?: unknown;
  /** The singular spellings predate the lists and load as a one-entry list. */
  classifier_model?: unknown;
  escalation_model?: unknown;
  classifier_models?: unknown;
  escalation_models?: unknown;
};

export class AutoModeConfigError extends Error {
  constructor(
    readonly field: string,
    message: string,
  ) {
    super(message);
    this.name = "AutoModeConfigError";
  }
}

export const defaultAutoModeConfig: AutoModeConfig = {
  enabledDefault: true,
  environment: [],
  allow: [],
  hardDeny: [],
  classifierModels: defaultClassifierModels,
  escalationModels: defaultEscalationModels,
};

export function loadAutoModeConfig(raw: RawAutoModeConfig | undefined): AutoModeConfig {
  if (raw === undefined) return defaultAutoModeConfig;
  const allow = readPatterns(raw.allow, "allow");
  for (const pattern of allow) {
    if (isBroadPattern(pattern)) {
      throw new AutoModeConfigError(
        "allow",
        `broad allow pattern ${JSON.stringify(pattern)} is refused: an allow entry must name ` +
          `something. A blanket allow is what the classifier exists to replace.`,
      );
    }
  }
  return {
    enabledDefault: readBoolean(raw.enabled_default, "enabled_default", defaultAutoModeConfig.enabledDefault),
    environment: readStrings(raw.environment, "environment"),
    allow,
    hardDeny: readPatterns(raw.hard_deny, "hard_deny"),
    classifierModels: readModels({
      list: raw.classifier_models,
      listField: "classifier_models",
      single: raw.classifier_model,
      singleField: "classifier_model",
      fallback: defaultClassifierModel,
    }),
    escalationModels: readModels({
      list: raw.escalation_models,
      listField: "escalation_models",
      single: raw.escalation_model,
      singleField: "escalation_model",
      fallback: defaultEscalationModel,
    }),
  };
}

/** A pattern with no literal characters matches everything it is asked about. */
export function isBroadPattern(pattern: string): boolean {
  return pattern.replace(/[*?\s]/g, "") === "";
}

export function matchesPattern(pattern: string, signature: string): boolean {
  return globToRegExp(pattern).test(signature);
}

export function matchesAnyPattern(patterns: readonly string[], signature: string): string | undefined {
  for (const pattern of patterns) if (matchesPattern(pattern, signature)) return pattern;
  return undefined;
}

function globToRegExp(pattern: string): RegExp {
  let source = "";
  for (const character of pattern) {
    if (character === "*") source += ".*";
    else if (character === "?") source += ".";
    else source += character.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  }
  return new RegExp(`^${source}$`, "s");
}

function readPatterns(value: unknown, field: string): readonly string[] {
  return readStrings(value, field).map((pattern) => pattern.trim());
}

function readStrings(value: unknown, field: string): string[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new AutoModeConfigError(field, `${field} must be a list of strings`);
  return value.map((entry, index) => {
    if (typeof entry !== "string") throw new AutoModeConfigError(field, `${field}[${index}] must be a string`);
    return entry;
  });
}

/**
 * The singular field is what stored configs say today and loads as one entry.
 * An empty list is refused rather than defaulted: it is always a typo.
 */
function readModels(raw: {
  list: unknown;
  listField: string;
  single: unknown;
  singleField: string;
  fallback: string;
}): readonly string[] {
  if (raw.list === undefined || raw.list === null) {
    return [readString(raw.single, raw.singleField, raw.fallback)];
  }
  const models = readStrings(raw.list, raw.listField).map((model, index) => {
    const trimmed = model.trim();
    if (trimmed === "") throw new AutoModeConfigError(raw.listField, `${raw.listField}[${index}] is blank`);
    return trimmed;
  });
  if (models.length === 0) {
    throw new AutoModeConfigError(
      raw.listField,
      `${raw.listField} names no model: a layer with an empty list can judge nothing. ` +
        `Omit the field to run on the shipped default.`,
    );
  }
  return models;
}

function readString(value: unknown, field: string, fallback: string): string {
  if (value === undefined || value === null) return fallback;
  if (typeof value !== "string") throw new AutoModeConfigError(field, `${field} must be a string`);
  const trimmed = value.trim();
  return trimmed === "" ? fallback : trimmed;
}

function readBoolean(value: unknown, field: string, fallback: boolean): boolean {
  if (value === undefined || value === null) return fallback;
  if (typeof value !== "boolean") throw new AutoModeConfigError(field, `${field} must be a boolean`);
  return value;
}
