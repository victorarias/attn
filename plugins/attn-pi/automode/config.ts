// Auto mode's configuration: the value the decision tree is evaluated
// against, plus the loader that turns stored/on-disk JSON into it. Storage
// (daemon-owned, versioned) arrives in a later slice; this file only knows
// how to validate and normalize one config value.
//
// Patterns are matched against a call's signature (see policy.ts's
// callSignature): the bare command for `bash`, `<tool> <path>` for
// everything else. `*` matches any run of characters, `?` matches one.

/** Defaults from the classifier receipt in docs/plans/2026-08-16-pi-auto-mode.md. */
export const defaultClassifierModel = "opencode-go/glm-5.3";
export const defaultEscalationModel = "opencode-go/qwen3.8-max";

export type AutoModeConfig = {
  /** Whether new sessions start with auto mode on. */
  enabledDefault: boolean;
  /** Prose the classifier reads to learn what this machine is allowed to do. */
  environment: readonly string[];
  /** Narrow patterns that skip the classifier and run. */
  allow: readonly string[];
  /** Patterns that are refused before anything else looks at the call. */
  hardDeny: readonly string[];
  classifierModel: string;
  escalationModel: string;
};

/** The on-the-wire/on-disk shape, snake_case per the plan's schema. */
export type RawAutoModeConfig = {
  enabled_default?: unknown;
  environment?: unknown;
  allow?: unknown;
  hard_deny?: unknown;
  classifier_model?: unknown;
  escalation_model?: unknown;
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
  classifierModel: defaultClassifierModel,
  escalationModel: defaultEscalationModel,
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
    classifierModel: readString(raw.classifier_model, "classifier_model", defaultClassifierModel),
    escalationModel: readString(raw.escalation_model, "escalation_model", defaultEscalationModel),
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
