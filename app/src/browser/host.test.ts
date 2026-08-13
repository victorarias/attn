import { describe, expect, it, vi } from 'vitest';
import {
  isBrowserHostOwnedTarget,
  mountBrowserHost,
  serializeBrowserControlResultMessage,
  updateBrowserHost,
} from './host';

const invoke = vi.hoisted(() => vi.fn(async () => undefined));
vi.mock('@tauri-apps/api/core', () => ({ invoke, isTauri: () => true }));

describe('serializeBrowserControlResultMessage', () => {
  it('accepts results within the transport budget', () => {
    const message = {
      cmd: 'browser_control_result' as const,
      request_id: 'request-1',
      success: true,
      data: '1234',
    };
    const serialized = JSON.stringify(message);
    expect(serializeBrowserControlResultMessage(message, serialized.length)).toBe(serialized);
  });

  it('accounts for JSON escaping when enforcing the transport budget', () => {
    const plain = {
      cmd: 'browser_control_result' as const,
      request_id: 'request-1',
      success: true,
      data: '""""',
    };
    expect(() => serializeBrowserControlResultMessage(plain, JSON.stringify(plain).length - 1)).toThrow(
      /serialized browser control result is .* bytes/,
    );
  });
});

// The Rust commands take one `geometry` argument and reject anything else at
// deserialization, which no typechecker on this side can see. These pin the
// payload shape both commands are called with.
describe('browser host geometry payload', () => {
  const rect = { x: 10, y: 20, width: 300, height: 400 };

  it('mounts with the rect and visibility inside one geometry argument', async () => {
    invoke.mockClear();
    await mountBrowserHost('browser-a-b', 'https://example.com/', rect, true);

    expect(invoke).toHaveBeenCalledWith('browser_host_mount', {
      label: 'browser-a-b',
      url: 'https://example.com/',
      geometry: { ...rect, visible: true },
    });
  });

  it('updates with the same shape', async () => {
    invoke.mockClear();
    await updateBrowserHost('browser-a-b', rect, false);

    expect(invoke).toHaveBeenCalledWith('browser_host_update', {
      label: 'browser-a-b',
      geometry: { ...rect, visible: false },
    });
  });
});

describe('isBrowserHostOwnedTarget', () => {
  it('keeps browser focus for controls inside a browser tile', () => {
    const tile = document.createElement('div');
    tile.dataset.browserHostOwner = 'true';
    const button = document.createElement('button');
    tile.append(button);

    expect(isBrowserHostOwnedTarget(button)).toBe(true);
    expect(isBrowserHostOwnedTarget(document.body)).toBe(false);
  });
});
