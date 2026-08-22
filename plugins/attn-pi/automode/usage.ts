// A blocked call never reaches pi's `tool_result` handler (pi answers it
// inline, `kind: "immediate"`), so a denial's classification tokens have no
// result to ride. They wait here and join the next one: the session total is
// right, the per-call attribution is not, and a session ending on a denial
// leaves that last classification unreported.

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
