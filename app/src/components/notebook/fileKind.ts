// Classifies a file path by how the notebook browser should render it. The daemon's
// filesystem surface is structure-blind — it serves ANY file under the root — so the
// browser decides per-extension which editor, if any, to show:
//   markdown -> the live markdown editor (preview + backlinks + send-to-chief)
//   text     -> a plain text editor (edit + autosave, no markdown affordances)
//   binary   -> a read-only placeholder; we never even read it (fs_read returns a
//               string, which is meaningless for binary bytes)
//
// The gate is an explicit binary-extension denylist: anything not known-binary is
// treated as editable text. That errs toward editability (a stray unknown extension
// opens as text) rather than hiding files behind a placeholder.

const BINARY_EXTENSIONS = new Set([
  // images
  'png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'ico', 'svgz', 'tif', 'tiff', 'heic', 'avif',
  // documents / archives
  'pdf', 'zip', 'gz', 'tgz', 'bz2', 'xz', '7z', 'rar', 'tar',
  // audio / video
  'mp4', 'mov', 'avi', 'mkv', 'webm', 'mp3', 'wav', 'flac', 'ogg', 'm4a',
  // fonts
  'woff', 'woff2', 'ttf', 'otf', 'eot',
  // compiled / opaque binaries
  'exe', 'dll', 'dylib', 'so', 'o', 'a', 'wasm', 'bin', 'dat', 'class', 'jar',
  // databases / misc
  'sqlite', 'db',
]);

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
  if (BINARY_EXTENSIONS.has(ext)) return 'binary';
  return 'text';
}

export function isMarkdownPath(path: string): boolean {
  return fileKind(path) === 'markdown';
}

export function isBinaryPath(path: string): boolean {
  return fileKind(path) === 'binary';
}
