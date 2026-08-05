const DEFAULT_REPLAY_SLICE_BUDGET_MS = 6;

type YieldTask = () => Promise<void>;

let messageChannel: MessageChannel | null = null;
const pendingMessageChannelYields: Array<() => void> = [];

function yieldWithMessageChannel(): Promise<void> {
  if (!messageChannel) {
    messageChannel = new MessageChannel();
    messageChannel.port1.onmessage = () => {
      pendingMessageChannelYields.shift()?.();
    };
  }
  return new Promise<void>((resolve) => {
    pendingMessageChannelYields.push(resolve);
    messageChannel?.port2.postMessage(undefined);
  });
}

export function yieldToMainThread(): Promise<void> {
  const scheduler = (globalThis as typeof globalThis & {
    scheduler?: { yield?: () => Promise<void> };
  }).scheduler;
  if (scheduler?.yield) {
    return scheduler.yield();
  }
  if (typeof MessageChannel !== 'undefined') {
    return yieldWithMessageChannel();
  }
  return new Promise<void>((resolve) => setTimeout(resolve, 0));
}

// Historical restore is already segmented into bounded Ghostty writes so a
// resize can cancel it between chunks. This budget decides when those queued
// writes actually need to give the browser a task boundary: the old path
// yielded before every 16 KiB chunk and paid WKWebView's timer clamp even when
// parsing the preceding chunk took only a fraction of the frame budget.
export class CooperativeReplayBudget {
  private sliceStartedAt: number | null = null;

  constructor(
    private readonly budgetMs = DEFAULT_REPLAY_SLICE_BUDGET_MS,
    private readonly now: () => number = () => performance.now(),
    private readonly yieldTask: YieldTask = yieldToMainThread,
  ) {}

  reset(): void {
    this.sliceStartedAt = null;
  }

  async beforeOperation(): Promise<boolean> {
    const now = this.now();
    if (this.sliceStartedAt === null) {
      this.sliceStartedAt = now;
      return false;
    }
    if (now - this.sliceStartedAt < this.budgetMs) {
      return false;
    }
    await this.yieldTask();
    this.sliceStartedAt = this.now();
    return true;
  }
}
