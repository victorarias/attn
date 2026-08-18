// What auto mode shows a person, and the slice of pi's ExtensionUIContext it
// shows it through. Duck-typed like the rest of this module (pi 0.83.0,
// core/extensions/types.ts) so `bun test` drives the whole surface.
//
// Everything here is set once, on a transition: a status that changes when the
// mode changes, a working message that appears for the length of one
// classification, a widget that changes when a call is denied. Nothing repaints
// on a timer, and a session where nothing happens draws nothing.
import type { AutoModeDenial } from "./index";
import type { BreakerState } from "./session";

/** Keys are namespaced: a user's own extensions share these registries. */
export const autoModeStatusKey = "attn-auto-mode";
export const autoModeDenialWidgetKey = "attn-auto-mode-denials";

/** Shown for the ~2s a classification takes, so it does not read as a hang. */
export const classifyingWorkingMessage = "auto mode is checking this call…";

/** Denials the widget lists in full before it starts counting the rest. */
export const denialWidgetLimit = 5;

export type AutoModeUILike = {
  setStatus(key: string, text: string | undefined): void;
  setWorkingMessage(message?: string): void;
  notify(message: string, type?: "info" | "warning" | "error"): void;
  setWidget(key: string, content: string[] | undefined): void;
  confirm(title: string, message: string): Promise<boolean>;
};

/**
 * How much of a blocked call the surfaces show. A bash command carries the
 * whole shell line and reads past four wrapped rows in the widget, which
 * buries the reason underneath it — and the reason is what a person acts on.
 * The model still gets the call in full: `denialToolResult` does not clamp.
 */
export const denialActionCharLimit = 80;

export function autoModeStatusText(enabled: boolean): string {
  return enabled ? "auto: on" : "auto: off";
}

export function denialNotice(denial: AutoModeDenial): string {
  return `auto mode blocked ${clamp(denial.action)} — ${denial.reason}`;
}

function clamp(text: string): string {
  return text.length <= denialActionCharLimit ? text : `${text.slice(0, denialActionCharLimit - 1)}…`;
}

/**
 * The denials since the user last spoke. Speaking is what clears the list,
 * which is the same act that clears the denials themselves: the next message
 * may be the approval.
 */
export function denialWidgetLines(denials: readonly AutoModeDenial[]): string[] {
  if (denials.length === 0) return [];
  const shown = denials.slice(-denialWidgetLimit);
  const hidden = denials.length - shown.length;
  const lines = [`auto mode blocked ${denials.length} call${denials.length === 1 ? "" : "s"}:`];
  if (hidden > 0) lines.push(`  … ${hidden} earlier`);
  for (const denial of shown) lines.push(`  ${clamp(denial.action)} — ${denial.reason}`);
  lines.push("  Approve in your reply to let the agent retry.");
  return lines;
}

/**
 * The breaker's question, in the flavour the episode earned. An episode of
 * blocks that no model ever judged is an outage, and asking the user to weigh
 * in on refusals that never happened sends them after the wrong problem.
 */
export function breakerQuestion(breaker: BreakerState): { title: string; message: string } {
  if (breaker.outage) {
    return {
      title: "auto mode cannot reach its classifier",
      message:
        `It blocked ${breaker.consecutive} calls in a row and ${breaker.total} since you last spoke, ` +
        `every one of them because no classifier model answered — nothing judged them. Your model ` +
        `endpoint looks to be down. Try again? Answering yes judges this call and the ones after it ` +
        `normally, which will keep blocking while the endpoint is unreachable.`,
    };
  }
  return {
    title: "auto mode stopped judging calls",
    message:
      `It has refused ${breaker.consecutive} calls in a row and ${breaker.total} since you last spoke, ` +
      `so it stopped rather than let the agent grind against the same refusal. Resume auto mode? ` +
      `Answering yes judges this call and the ones after it normally; it does not approve anything ` +
      `on its own.`,
  };
}
