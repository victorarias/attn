import type { AutoModeDenial } from "./index";
import type { BreakerState } from "./session";

export const autoModeStatusKey = "attn-auto-mode";

export const classifyingWorkingMessage = "auto mode is checking this call…";

// The report /auto blocked prints. On demand, so it may run longer than a widget could.
export const denialReportLimit = 5;

// pi's footer dims everything it draws itself but renders extension status lines
// unstyled, so these surfaces tint their own text: quiet states dim, denials warn.
export type AutoModeTheme = {
  fg(color: "dim" | "warning" | "muted" | "error", text: string): string;
  bold(text: string): string;
};

export type AutoModeUILike = {
  setStatus(key: string, text: string | undefined): void;
  setWorkingMessage(message?: string): void;
  notify(message: string, type?: "info" | "warning" | "error"): void;
  confirm(title: string, message: string): Promise<boolean>;
  editor?(title: string, prefill?: string): Promise<string | undefined>;
  theme?: AutoModeTheme;
};

export function dimmed(theme: AutoModeTheme | undefined, text: string): string {
  return theme?.fg("dim", text) ?? text;
}

export function mutedText(theme: AutoModeTheme | undefined, text: string): string {
  return theme?.fg("muted", text) ?? text;
}

export function problem(theme: AutoModeTheme | undefined, text: string): string {
  return theme?.fg("error", text) ?? text;
}

// What the surfaces show; the model still gets the call in full — denialToolResult
// does not clamp.
export const denialActionCharLimit = 80;
export const denialReasonCharLimit = 120;

// Two denials with the same action and rule are the agent retrying, not new trouble.
export function denialKey(denial: AutoModeDenial): string {
  return `${denial.action}\u0000${denial.rule ?? ""}`;
}

export function autoModeStatusText(enabled: boolean): string {
  return enabled ? "auto: on" : "auto: off";
}

// The only standing trace of a denial: the footer gains a count, never a widget —
// /auto blocked reads the details on demand.
export function heldStatusText(enabled: boolean, held: number): string {
  if (!enabled || held === 0) return autoModeStatusText(enabled);
  return `${autoModeStatusText(enabled)} · ${held} held`;
}

export function denialNotice(denial: AutoModeDenial): string {
  return `auto mode blocked ${clamp(denial.action)} — ${denial.reason}`;
}

function clamp(text: string): string {
  return text.length <= denialActionCharLimit ? text : `${text.slice(0, denialActionCharLimit - 1)}…`;
}

function clampReason(reason: string): string {
  return reason.length <= denialReasonCharLimit ? reason : `${reason.slice(0, denialReasonCharLimit - 1)}…`;
}

type DenialGroup = { denial: AutoModeDenial; count: number };

// Retries arrive as consecutive denials of the same action; they read as one row.
function groupRetries(shown: readonly AutoModeDenial[]): DenialGroup[] {
  const groups: DenialGroup[] = [];
  for (const denial of shown) {
    const last = groups.at(-1);
    if (last && denialKey(last.denial) === denialKey(denial)) last.count += 1;
    else groups.push({ denial, count: 1 });
  }
  return groups;
}

// Distinct blocked calls, retries merged: the number the footer counts.
export function heldCount(denials: readonly AutoModeDenial[]): number {
  return groupRetries(denials).length;
}

export function denialReportLines(denials: readonly AutoModeDenial[], theme?: AutoModeTheme): string[] {
  if (denials.length === 0) return [];
  // The header counts calls, not denials: a retried call is one blocked call, said once.
  const groups = groupRetries(denials);
  const shown = groups.slice(-denialReportLimit);
  const hidden = groups.length - shown.length;
  const header = groups.length === 1 ? "auto mode is holding this call" : `auto mode is holding ${groups.length} calls`;
  const lines = [theme ? theme.fg("warning", theme.bold(`⚠ ${header}`)) : `⚠ ${header}`];
  if (hidden > 0) lines.push(dimmed(theme, `  … ${hidden} earlier`));
  for (const { denial, count } of shown) {
    const retries = count > 1 ? dimmed(theme, ` ×${count}`) : "";
    lines.push(`  ${clamp(denial.action)}${retries} ${mutedText(theme, `— ${clampReason(denial.reason)}`)}`);
  }
  lines.push(dimmed(theme, offer(denials)));
  return lines;
}

function offer(denials: readonly AutoModeDenial[]): string {
  if (denials.every((denial) => denial.rule === "classifier-incomplete" || denial.rule === "classifier-too-long")) {
    return "  Review needs missing evidence or a smaller action; see the reason above.";
  }
  if (denials.every(nothingJudged)) {
    return "  No classifier answered these, so approving will not help.";
  }
  if (denials.every((denial) => denial.clearable === false)) {
    return "  Approving will not help. Auto mode's settings decide these.";
  }
  return "  Approve in your reply to let the agent retry.";
}

function nothingJudged(denial: AutoModeDenial): boolean {
  return denial.rule === "classifier-unavailable" || denial.rule === "classifier-too-long";
}

export function tooLongQuestion(action: string): { title: string; message: string } {
  return {
    title: "auto mode could not judge this call",
    message:
      `The action and conversation do not fit in any configured classifier model. ` +
      `Auto mode could not complete its review of ${clamp(action)}. ` +
      `Run the exact arguments you just inspected once? Answering no blocks the call.`,
  };
}

export function breakerQuestion(breaker: BreakerState): { title: string; message: string } {
  if (breaker.outage) {
    return {
      title: "auto mode cannot reach its classifier",
      message:
        `It blocked ${breaker.consecutive} calls in a row and ${breaker.total} since you last spoke, ` +
        `every one of them because no classifier model answered, so nothing judged them. Your model ` +
        `endpoint looks down. Try again? Answering yes judges this call and the ones after it ` +
        `normally, which will keep blocking while the endpoint is unreachable.`,
    };
  }
  return {
    title: "auto mode stopped judging calls",
    message:
      `It has refused ${breaker.consecutive} calls in a row and ${breaker.total} since you last spoke, ` +
      `so it stopped instead of letting the agent retry against the same refusal. Resume auto mode? ` +
      `Answering yes judges this call and the ones after it normally; it does not approve anything ` +
      `on its own.`,
  };
}
