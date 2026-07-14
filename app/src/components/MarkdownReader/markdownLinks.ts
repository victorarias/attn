/**
 * Link/image target safety for rendered markdown documents. Moved here from
 * WorkspaceDockTile so the reader owns one implementation (the tile re-exports
 * `resolveMarkdownTarget` for its existing consumers/tests).
 */
import { invoke } from '@tauri-apps/api/core';
import { openUrl } from '@tauri-apps/plugin-opener';

export type MarkdownTarget =
  | { kind: 'external'; value: string }
  | { kind: 'fragment'; value: string }
  | { kind: 'local'; value: string };

// Plannotator's dangerous-protocol rule, minus `file:` — attn routes `file:`
// links through resolveMarkdownTarget's host check plus the safe-extension
// gate below instead of dropping them outright.
const DANGEROUS_LINK_PROTOCOL = /^\s*(javascript|data|vbscript)\s*:/i;

/** Returns null when the URL must not render as a link at all. */
export function sanitizeLinkUrl(url: string): string | null {
  return DANGEROUS_LINK_PROTOCOL.test(url) ? null : url;
}

const safeLocalDocumentExtensions = new Set([
  '.md',
  '.markdown',
  '.pdf',
  '.rst',
  '.text',
  '.txt',
]);

const safeLocalImageExtensions = new Set([
  '.bmp',
  '.gif',
  '.jpeg',
  '.jpg',
  '.png',
  '.tif',
  '.tiff',
  '.webp',
]);

function localTargetExtension(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const slash = trimmed.lastIndexOf('/');
  const dot = trimmed.lastIndexOf('.');
  return dot > slash ? trimmed.slice(dot).toLowerCase() : '';
}

export function isSafeLocalMarkdownTarget(path: string): boolean {
  const extension = localTargetExtension(path);
  return safeLocalDocumentExtensions.has(extension) || safeLocalImageExtensions.has(extension);
}

export function isSafeLocalMarkdownImageTarget(path: string): boolean {
  return safeLocalImageExtensions.has(localTargetExtension(path));
}

function decodedPath(url: URL): string {
  try {
    return decodeURIComponent(url.pathname);
  } catch {
    return url.pathname;
  }
}

export function resolveMarkdownTarget(documentPath: string, target: string): MarkdownTarget | null {
  const trimmed = target.trim();
  if (!trimmed) {
    return null;
  }
  if (trimmed.startsWith('#')) {
    return { kind: 'fragment', value: trimmed };
  }
  if (trimmed.startsWith('//')) {
    return null;
  }

  const scheme = trimmed.match(/^([a-z][a-z0-9+.-]*):/i)?.[1]?.toLowerCase();
  if (scheme) {
    if (scheme === 'http' || scheme === 'https' || scheme === 'mailto') {
      return { kind: 'external', value: trimmed };
    }
    if (scheme !== 'file') {
      return null;
    }
    try {
      const url = new URL(trimmed);
      return url.hostname ? null : { kind: 'local', value: decodedPath(url) };
    } catch {
      return null;
    }
  }

  if (!documentPath) {
    return null;
  }
  try {
    const base = new URL('file:///');
    base.pathname = documentPath;
    const url = new URL(trimmed, base);
    return url.hostname ? null : { kind: 'local', value: decodedPath(url) };
  } catch {
    return null;
  }
}

export function openMarkdownTarget(target: MarkdownTarget): void {
  if (target.kind === 'fragment') {
    return;
  }
  if (target.kind === 'local' && !isSafeLocalMarkdownTarget(target.value)) {
    console.warn('[MarkdownReader] Blocked unsafe local Markdown target:', target.value);
    return;
  }
  const action = target.kind === 'local'
    ? invoke('open_safe_markdown_target', { path: target.value })
    : openUrl(target.value);
  void action.catch((error) => {
    console.warn('[MarkdownReader] Failed to open Markdown target:', error);
  });
}
