// Classifies a file path by how the notebook browser should render it. The daemon's
// filesystem surface is structure-blind — it serves ANY file under the root — so the
// browser decides per-extension which editor, if any, to show:
//   markdown -> the live markdown editor (preview + backlinks + send-to-chief)
//   text     -> a plain text editor (edit + autosave, no markdown affordances)
//   binary   -> a read-only placeholder; we never even read it (fs_read returns a
//               string, which is meaningless for binary bytes)
//
// The gate is an explicit text-extension allowlist. Ticket attachments can carry
// arbitrary files, so unknown formats must fail closed: opening an opaque file as
// text could transform its bytes on a later autosave. Add an extension only when
// the Notebook can safely edit it as UTF-8 source.
const TEXT_EXTENSIONS = new Set([
  'txt', 'text', 'html', 'htm', 'css', 'scss', 'sass', 'less',
  'js', 'mjs', 'cjs', 'ts', 'tsx', 'jsx',
  'json', 'jsonc', 'jsonl', 'yaml', 'yml', 'toml', 'xml', 'csv', 'tsv',
  'ini', 'cfg', 'conf', 'env', 'log',
  'sh', 'bash', 'zsh', 'fish', 'ps1',
  'go', 'rs', 'py', 'rb', 'php', 'java', 'kt', 'kts', 'c', 'cc', 'cpp', 'cxx', 'h', 'hpp', 'cs', 'swift', 'scala', 'sql',
  'md', 'markdown',
]);

const TEXT_FILENAMES = new Set(['readme', 'license', 'makefile', 'dockerfile', 'brewfile']);

export type FileKind = 'markdown' | 'text' | 'binary';

// The lowercased extension of a path's basename, without the dot. A dotfile with no
// other dot (".gitignore") has no extension here, so it classifies as editable text.
export function extensionOf(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1);
  const dot = name.lastIndexOf('.');
  if (dot <= 0) return '';
  return name.slice(dot + 1).toLowerCase();
}

export function fileKind(path: string): FileKind {
  const ext = extensionOf(path);
  if (ext === 'md' || ext === 'markdown') return 'markdown';
  const name = path.slice(path.lastIndexOf('/') + 1).toLowerCase();
  return TEXT_EXTENSIONS.has(ext) || TEXT_FILENAMES.has(name) ? 'text' : 'binary';
}

export function isMarkdownPath(path: string): boolean {
  return fileKind(path) === 'markdown';
}

export function isBinaryPath(path: string): boolean {
  return fileKind(path) === 'binary';
}
