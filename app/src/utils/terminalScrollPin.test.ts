import { describe, expect, it, vi } from 'vitest';
import {
  cleanupTerminalScrollPin,
  installTerminalScrollPin,
  resetTerminalScrollPin,
} from './terminalScrollPin';

function createMockTerminal() {
  const writeSpy = vi.fn();
  const scrollHandlers = new Set<() => void>();

  const term = {
    buffer: {
      active: {
        viewportY: 0,
        baseY: 10,
      },
    },
    write: writeSpy,
    onScroll(handler: () => void) {
      scrollHandlers.add(handler);
      return {
        dispose() {
          scrollHandlers.delete(handler);
        },
      };
    },
    _core: {
      registerCsiHandler: vi.fn(() => ({ dispose() {} })),
    },
    __emitScroll() {
      for (const handler of Array.from(scrollHandlers)) {
        handler();
      }
    },
  };

  return { term, writeSpy };
}

describe('terminalScrollPin', () => {
  it('reset flushes queued writes after the viewport was pinned above the bottom', () => {
    const container = document.createElement('div');
    const { term, writeSpy } = createMockTerminal();
    const callback = vi.fn();

    installTerminalScrollPin(term as any, container);

    term.buffer.active.viewportY = 4;
    container.dispatchEvent(new WheelEvent('wheel'));

    term.write('queued-data', callback);

    expect(writeSpy).not.toHaveBeenCalled();
    expect(callback).toHaveBeenCalledOnce();

    resetTerminalScrollPin(term as any);

    expect(writeSpy).toHaveBeenCalledTimes(1);
    expect(writeSpy).toHaveBeenCalledWith(new TextEncoder().encode('queued-data'));

    cleanupTerminalScrollPin(term as any);
  });
});
