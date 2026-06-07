import { describe, expect, it } from 'vitest';
import { isBrowserHostOwnedTarget, serializeBrowserControlResultMessage } from './host';

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
