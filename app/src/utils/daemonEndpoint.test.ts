import { describe, expect, it } from 'vitest';
import { resolveDaemonWebSocketURL } from './daemonEndpoint';

describe('resolveDaemonWebSocketURL', () => {
  it('uses endpoint override before env defaults', () => {
    expect(resolveDaemonWebSocketURL({
      endpoint: { id: 'remote-1', wsUrl: 'wss://remote.example/ws' },
      wsUrl: 'ws://localhost:9999/ws',
    })).toBe('wss://remote.example/ws');
  });

  it('uses direct wsUrl override when no endpoint profile is provided', () => {
    expect(resolveDaemonWebSocketURL({
      wsUrl: 'ws://localhost:9999/ws',
    })).toBe('ws://localhost:9999/ws');
  });

  it('falls back to the local daemon default when no override exists', () => {
    expect(resolveDaemonWebSocketURL()).toBe('ws://127.0.0.1:9849/ws');
  });
});
