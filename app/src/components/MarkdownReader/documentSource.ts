/**
 * The document a MarkdownReader renders.
 *
 * `uri` is opaque identity for client-side draft correlation. Typed fields are
 * the authority whenever the daemon acts; neither side recovers a path or seed
 * id by parsing the URI.
 */
export interface FileMarkdownDocumentSource {
  kind: 'file';
  uri: string;
  workspaceId: string;
  path: string;
}

export interface SeedMarkdownDocumentSource {
  kind: 'seed';
  uri: `attn://seed/${string}`;
  seedId: string;
}

export type MarkdownDocumentSource = FileMarkdownDocumentSource | SeedMarkdownDocumentSource;

/** Stable document identity for a file as seen from one owning workspace. */
export function markdownFileDocumentUri(workspaceId: string, path: string): string {
  return `attn://file/${encodeURIComponent(workspaceId)}/${encodeURIComponent(path)}`;
}

export function fileMarkdownSource(
  workspaceId: string,
  path: string,
): FileMarkdownDocumentSource {
  return {
    kind: 'file',
    uri: markdownFileDocumentUri(workspaceId, path),
    workspaceId,
    path,
  };
}

export function seedMarkdownSource(seedId: string): SeedMarkdownDocumentSource {
  return {
    kind: 'seed',
    uri: `attn://seed/${encodeURIComponent(seedId)}`,
    seedId,
  };
}

/** Files resolve relative targets beside themselves; seeds have no directory. */
export function markdownDocumentPath(source: MarkdownDocumentSource): string {
  return source.kind === 'file' ? source.path : '';
}
