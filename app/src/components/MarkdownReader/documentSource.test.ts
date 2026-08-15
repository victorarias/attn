import { describe, expect, it } from 'vitest';
import {
  markdownDocumentPath,
  fileMarkdownSource,
  markdownFileDocumentUri,
  seedMarkdownSource,
} from './documentSource';

describe('Markdown document sources', () => {
  it('gives files an escaped identity carrying workspace and path', () => {
    expect(markdownFileDocumentUri('remote/ws:1', '/tmp/a plan.md')).toBe(
      'attn://file/remote%2Fws%3A1/%2Ftmp%2Fa%20plan.md',
    );
    expect(fileMarkdownSource('ws-1', '/tmp/plan.md')).toEqual({
      kind: 'file',
      uri: 'attn://file/ws-1/%2Ftmp%2Fplan.md',
      workspaceId: 'ws-1',
      path: '/tmp/plan.md',
    });
  });

  it('uses the canonical seed house URI without inventing a file path', () => {
    const source = seedMarkdownSource('s-7k3f9m');
    expect(source).toEqual({
      kind: 'seed',
      uri: 'attn://seed/s-7k3f9m',
      seedId: 's-7k3f9m',
    });
    expect(markdownDocumentPath(source)).toBe('');
  });
});
