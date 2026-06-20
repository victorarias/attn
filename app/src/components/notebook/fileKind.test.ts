import { describe, expect, it } from 'vitest';
import { extensionOf, fileKind, isBinaryPath, isMarkdownPath } from './fileKind';

describe('fileKind', () => {
  it('classifies markdown by extension', () => {
    expect(fileKind('knowledge/index.md')).toBe('markdown');
    expect(fileKind('NOTES.MARKDOWN')).toBe('markdown');
    expect(isMarkdownPath('a/b/c.md')).toBe(true);
    expect(isMarkdownPath('a/b/c.txt')).toBe(false);
  });

  it('classifies known-binary extensions as binary', () => {
    expect(fileKind('assets/cover.png')).toBe('binary');
    expect(fileKind('a/b/clip.MP4')).toBe('binary');
    expect(fileKind('fonts/Inter.woff2')).toBe('binary');
    expect(isBinaryPath('x.pdf')).toBe(true);
    expect(isBinaryPath('x.md')).toBe(false);
  });

  it('treats unknown and missing extensions as editable text', () => {
    expect(fileKind('notes.txt')).toBe('text');
    expect(fileKind('config.json')).toBe('text');
    expect(fileKind('src/main.go')).toBe('text');
    // No extension at all → text (e.g. a README or a LICENSE).
    expect(fileKind('README')).toBe('text');
    // A leading-dot dotfile with no further extension → text, not "" -> binary.
    expect(fileKind('.gitignore')).toBe('text');
  });

  it('extracts the lowercased extension of the basename only', () => {
    expect(extensionOf('a.b/c.MD')).toBe('md');
    // A dot in a directory name must not count as the file's extension.
    expect(extensionOf('a.b/c')).toBe('');
    expect(extensionOf('.gitignore')).toBe('');
    expect(extensionOf('archive.tar.gz')).toBe('gz');
  });
});
