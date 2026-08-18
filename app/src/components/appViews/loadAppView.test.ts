import { describe, expect, it } from 'vitest';
import { errorText } from './loadAppView';

// What the authoring agent reads in `attn app logs`. The engine decides whether
// `stack` carries the message, and attn ships on WebKit, where it does not.

describe('the text of a crash report', () => {
  it('leads with what was thrown when the stack is frames only, as on WebKit', () => {
    const error = new Error('the ticket board is not there');
    error.stack = 'Approvals@http://127.0.0.1:9849/apps/bundle/reviewer/abc/approvals.js:1:199';

    const text = errorText(error);
    expect(text.startsWith('Error: the ticket board is not there')).toBe(true);
    expect(text).toContain('approvals.js:1:199');
  });

  it('does not repeat the message on an engine whose stack already carries it', () => {
    const error = new Error('boom');
    error.stack = 'Error: boom\n    at Approvals (approvals.js:1:1)';

    expect(errorText(error)).toBe(error.stack);
  });

  it('says what it got when what was thrown is not an Error', () => {
    expect(errorText('a string nobody wrapped')).toBe('a string nobody wrapped');
  });
});
