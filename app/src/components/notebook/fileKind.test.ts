import { describe, expect, it } from 'vitest';
import { extensionOf, fileKind, isBinaryPath, isMarkdownPath } from './fileKind';

describe('fileKind', () => {
  it('classifies markdown by extension', () => {
    expect(fileKind('knowledge/index.md')).toBe('markdown');
    expect(fileKind('NOTES.MARKDOWN')).toBe('markdown');
    expect(isMarkdownPath('a/b/c.md')).toBe(true);
    expect(isMarkdownPath('a/b/c.txt')).toBe(false);
  });

  it('classifies opaque and unknown extensions as binary', () => {
    expect(fileKind('assets/cover.png')).toBe('binary');
    expect(fileKind('a/b/clip.MP4')).toBe('binary');
    expect(fileKind('fonts/Inter.woff2')).toBe('binary');
    expect(isBinaryPath('x.pdf')).toBe(true);
    expect(fileKind('attachments/prototype.docx')).toBe('binary');
    expect(fileKind('attachments/installer.pkg')).toBe('binary');
    expect(isBinaryPath('x.md')).toBe(false);
  });

  it('classifies known text source formats as editable text', () => {
    expect(fileKind('notes.txt')).toBe('text');
    expect(fileKind('config.json')).toBe('text');
    expect(fileKind('src/main.go')).toBe('text');
    expect(fileKind('prototype.html')).toBe('text');
    // Well-known extensionless source files remain editable.
    expect(fileKind('README')).toBe('text');
    expect(fileKind('Makefile')).toBe('text');
    // Ambiguous extensionless names and dotfiles fail closed.
    expect(fileKind('attachment')).toBe('binary');
    expect(fileKind('.gitignore')).toBe('binary');
  });

  it('extracts the lowercased extension of the basename only', () => {
    expect(extensionOf('a.b/c.MD')).toBe('md');
    // A dot in a directory name must not count as the file's extension.
    expect(extensionOf('a.b/c')).toBe('');
    expect(extensionOf('.gitignore')).toBe('');
    expect(extensionOf('archive.tar.gz')).toBe('gz');
  });
});
