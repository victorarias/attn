// Where a classification's cost goes.
//
// pi sums the `usage` on toolResult messages into the session's totals
// (pi 0.84.2, core/usage-totals.ts), and a `tool_result` handler can attach
// it. A BLOCKED call never reaches that handler — pi answers it inline from
// the block reason and runs no result hook (pi-agent-core agent-loop.ts,
// `kind: "immediate"`) — so a denial's tokens have no result of their own to
// ride. They wait here and join the next one instead: the session total is
// right, which is what pays the bill; the per-call attribution is not, and
// nothing in pi can make it so today. A session whose last act is a denial
// keeps that last classification unreported — a few tenths of a cent, and the
// alternative is inventing a tool result the model never asked for.

export type UsageLike = {
  input?: number;
  output?: number;
  cacheRead?: number;
  cacheWrite?: number;
  totalTokens?: number;
  cost?: { input?: number; output?: number; cacheRead?: number; cacheWrite?: number; total?: number };
};

export class UsageLedger {
  private pending: UsageLike | undefined;

  add(usage: UsageLike | undefined): void {
    if (!usage) return;
    this.pending = mergeUsage(this.pending, usage);
  }

  /** Takes everything held; the caller now owns reporting it. */
  drain(): UsageLike | undefined {
    const held = this.pending;
    this.pending = undefined;
    return held;
  }
}

export function mergeUsage(left: UsageLike | undefined, right: UsageLike | undefined): UsageLike | undefined {
  if (!left) return right;
  if (!right) return left;
  return {
    input: sum(left.input, right.input),
    output: sum(left.output, right.output),
    cacheRead: sum(left.cacheRead, right.cacheRead),
    cacheWrite: sum(left.cacheWrite, right.cacheWrite),
    totalTokens: sum(left.totalTokens, right.totalTokens),
    cost: {
      input: sum(left.cost?.input, right.cost?.input),
      output: sum(left.cost?.output, right.cost?.output),
      cacheRead: sum(left.cost?.cacheRead, right.cost?.cacheRead),
      cacheWrite: sum(left.cost?.cacheWrite, right.cost?.cacheWrite),
      total: sum(left.cost?.total, right.cost?.total),
    },
  };
}

function sum(left: number | undefined, right: number | undefined): number {
  return (left ?? 0) + (right ?? 0);
}
