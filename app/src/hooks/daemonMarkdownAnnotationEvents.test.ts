import { describe, expect, it, vi } from 'vitest';
import {
  handleMarkdownAnnotationDaemonEvent,
  markdownAnnotationKey,
} from './daemonMarkdownAnnotationEvents';
import type { PendingKeyedRequests } from './daemonPendingRequests';

describe('markdown annotation document identity', () => {
  it('keys one operation by the opaque document URI', () => {
    expect(markdownAnnotationKey('save', 'attn://seed/s-7k3f9m')).toBe(
      'save:attn://seed/s-7k3f9m',
    );
  });

  it('settles a result by document_uri', () => {
    const resolve = vi.fn();
    const pending: PendingKeyedRequests = new Map([
      [
        markdownAnnotationKey('get', 'attn://seed/s-7k3f9m'),
        { requestId: 'req-1', resolve, reject: vi.fn() },
      ],
    ]);

    expect(handleMarkdownAnnotationDaemonEvent({
      event: 'markdown_annotations_get_result',
      request_id: 'req-1',
      document_uri: 'attn://seed/s-7k3f9m',
      success: true,
      annotations: [],
      generation: 4,
    }, pending)).toBe(true);

    expect(resolve).toHaveBeenCalledWith({ annotations: [], generation: 4 });
    expect(pending).toHaveLength(0);
  });
});
