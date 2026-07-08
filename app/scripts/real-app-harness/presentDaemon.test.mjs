import { describe, expect, it } from 'vitest';
import { buildSubmitMessage, readFrontendProtocolVersion } from './presentDaemon.mjs';

describe('readFrontendProtocolVersion', () => {
  it('reads a non-empty digit string from the real useDaemonSocket.ts', () => {
    const version = readFrontendProtocolVersion();
    expect(version).toMatch(/^\d+$/);
  });
});

describe('buildSubmitMessage', () => {
  it('shapes a present_submit_round message with protocol field names', () => {
    const message = buildSubmitMessage({
      roundId: 'round-1',
      comments: [
        { filepath: 'a.go', line_start: 10, line_end: 12, side: 'new', content: 'nit' },
      ],
      handback: true,
      verdict: 'approved',
    });

    expect(message).toEqual({
      cmd: 'present_submit_round',
      round_id: 'round-1',
      verdict: 'approved',
      comments: [
        { filepath: 'a.go', line_start: 10, line_end: 12, side: 'new', content: 'nit' },
      ],
      handback: true,
    });
  });

  it('defaults handback to true, verdict to feedback, and comments to empty', () => {
    const message = buildSubmitMessage({ roundId: 'round-2' });
    expect(message.handback).toBe(true);
    expect(message.verdict).toBe('feedback');
    expect(message.comments).toEqual([]);
  });

  it('throws without a roundId', () => {
    expect(() => buildSubmitMessage({ comments: [] })).toThrow('requires roundId');
  });

  it('throws on an invalid verdict', () => {
    expect(() => buildSubmitMessage({ roundId: 'round-5', verdict: 'lgtm' })).toThrow(
      'verdict must be "approved" or "feedback"',
    );
  });

  it('throws on an invalid comment side', () => {
    expect(() =>
      buildSubmitMessage({
        roundId: 'round-3',
        comments: [{ filepath: 'a.go', line_start: 1, line_end: 1, side: 'sideways', content: 'x' }],
      }),
    ).toThrow('side must be "new" or "old"');
  });

  it('throws when a comment is missing filepath', () => {
    expect(() =>
      buildSubmitMessage({
        roundId: 'round-4',
        comments: [{ line_start: 1, line_end: 1, side: 'new', content: 'x' }],
      }),
    ).toThrow('filepath is required');
  });
});
