import type { Terminal as XTerm } from '@xterm/xterm';

const scrollPinTextEncoder = new TextEncoder();

interface ScrollPinStats {
  chunks: number;
  bytes: number;
}

interface InstallTerminalScrollPinOptions {
  onQueueStatsChange?: (stats: ScrollPinStats) => void;
}

interface Disposable {
  dispose(): void;
}

interface TerminalCoreLike {
  registerCsiHandler?: (identifier: { final: string }, callback: (params: unknown) => boolean) => Disposable;
}

interface TerminalScrollPinController {
  reset(): void;
  dispose(): void;
}

const scrollPinControllers = new WeakMap<XTerm, TerminalScrollPinController>();

function getCsiParamValue(params: unknown): number {
  if (Array.isArray(params)) {
    const first = params[0];
    if (Array.isArray(first)) {
      return typeof first[0] === 'number' ? first[0] : 0;
    }
    return typeof first === 'number' ? first : 0;
  }
  if (params && typeof params === 'object' && 'params' in params) {
    const nested = (params as { params?: unknown }).params;
    if (Array.isArray(nested)) {
      const first = nested[0];
      if (Array.isArray(first)) {
        return typeof first[0] === 'number' ? first[0] : 0;
      }
      return typeof first === 'number' ? first : 0;
    }
  }
  return 0;
}

export function installTerminalScrollPin(
  term: XTerm,
  container: HTMLElement,
  options: InstallTerminalScrollPinOptions = {},
) {
  cleanupTerminalScrollPin(term);

  const core = (term as unknown as { _core?: TerminalCoreLike })._core;
  const disposables: Disposable[] = [];
  const writeQueue: Uint8Array[] = [];
  const onQueueStatsChange = options.onQueueStatsChange;

  let pinned = false;
  let lastUserInteraction = 0;
  let queuedBytes = 0;
  let disposed = false;

  const updateQueueStats = () => {
    onQueueStatsChange?.({
      chunks: writeQueue.length,
      bytes: queuedBytes,
    });
  };

  const originalWrite = term.write.bind(term);

  const flushQueue = () => {
    if (writeQueue.length === 0) {
      return;
    }
    const combined = new Uint8Array(queuedBytes);
    let offset = 0;
    for (const chunk of writeQueue) {
      combined.set(chunk, offset);
      offset += chunk.length;
    }
    writeQueue.length = 0;
    queuedBytes = 0;
    updateQueueStats();
    originalWrite(combined);
  };

  term.write = ((data: string | Uint8Array, callback?: () => void) => {
    if (pinned) {
      const buffer = term.buffer.active;
      if (buffer.viewportY >= buffer.baseY) {
        pinned = false;
        flushQueue();
        originalWrite(data, callback);
        return;
      }

      const chunk = typeof data === 'string'
        ? scrollPinTextEncoder.encode(data)
        : new Uint8Array(data);
      writeQueue.push(chunk);
      queuedBytes += chunk.length;
      updateQueueStats();
      callback?.();
      return;
    }

    originalWrite(data, callback);
  }) as typeof term.write;

  const onWheel = () => {
    lastUserInteraction = performance.now();
    if (!pinned && term.buffer.active.viewportY < term.buffer.active.baseY) {
      pinned = true;
    }
  };
  container.addEventListener('wheel', onWheel, { passive: true, capture: true });
  disposables.push({
    dispose() {
      container.removeEventListener('wheel', onWheel, true);
    },
  });

  disposables.push(term.onScroll(() => {
    const isUser = performance.now() - lastUserInteraction < 300;
    if (!isUser) {
      return;
    }

    const buffer = term.buffer.active;
    const atBottom = buffer.viewportY >= buffer.baseY;

    if (!pinned && !atBottom) {
      pinned = true;
    } else if (pinned && atBottom) {
      pinned = false;
      flushQueue();
    }
  }));

  if (core?.registerCsiHandler) {
    disposables.push(core.registerCsiHandler({ final: 'J' }, (params: unknown) => {
      const param = getCsiParamValue(params);
      if (param === 3 && term.buffer.active.viewportY < term.buffer.active.baseY) {
        return true;
      }
      return false;
    }));
  }

  const controller: TerminalScrollPinController = {
    reset() {
      pinned = false;
      flushQueue();
    },
    dispose() {
      if (disposed) {
        return;
      }
      disposed = true;
      disposables.forEach((disposable) => disposable.dispose());
      term.write = originalWrite;
      pinned = false;
      flushQueue();
      updateQueueStats();
    },
  };

  scrollPinControllers.set(term, controller);
  updateQueueStats();
}

export function resetTerminalScrollPin(term: XTerm) {
  scrollPinControllers.get(term)?.reset();
}

export function cleanupTerminalScrollPin(term: XTerm) {
  const controller = scrollPinControllers.get(term);
  if (!controller) {
    return;
  }
  scrollPinControllers.delete(term);
  controller.dispose();
}
