// Broken-link flags for the live markdown editor: an in-notebook link whose target
// does not exist gets a red ⚠. External links and in-document anchors never do.
//
// Existence is checked asynchronously (fs_exists), so this is its own ViewPlugin
// rather than the sync buildDecorations: a late result forces a rebuild through a
// refresh effect, and a per-editor cache checks each path once, not per keystroke.

import { syntaxTree } from '@codemirror/language';
import { type EditorState, type Extension, type Range, StateEffect } from '@codemirror/state';
import {
  Decoration,
  type DecorationSet,
  EditorView,
  ViewPlugin,
  type ViewUpdate,
} from '@codemirror/view';
import { resolveNotebookLink } from './linkResolver';

// Structural shape of the daemon's FsExistsResult; no dependency on the hook.
export interface ExistsCheck {
  path: string;
  exists: boolean;
}

export interface BrokenLinkOptions {
  // A rejection leaves the link UNFLAGGED: only a conclusive absence flags one.
  existsFile?: (path: string) => Promise<ExistsCheck>;
  // The editing note's root-relative directory; bare-relative targets use it.
  baseDir: string;
}

// Fired when an async existence check resolves: rebuild so a now-known result paints.
const refreshBrokenLinks = StateEffect.define<null>();

// Drops cached "missing" verdicts on a disk change; "exists" verdicts stay, or the
// user's own autosave re-checks every link.
export const revalidateBrokenLinks = StateEffect.define<null>();

const BROKEN = Decoration.mark({
  class: 'cm-md-link-broken',
  attributes: { title: 'Link target not found in the notebook' },
});

// The notebook path whose existence decides broken-ness, or null when the href is
// not an in-notebook reference (external, protocol-relative, anchor, empty) and so
// must never be flagged. Bare-relative targets resolve against `baseDir`; the
// daemon only ever sees root-relative paths.
export function notebookLinkPath(href: string, baseDir: string): string | null {
  const resolved = resolveNotebookLink(href, baseDir);
  return resolved.kind === 'note' ? resolved.path : null;
}

// The distinct in-notebook link paths; pure over the parsed state, so testable.
export function notebookLinkPaths(state: EditorState, baseDir: string): string[] {
  const seen = new Set<string>();
  syntaxTree(state).iterate({
    enter: (node) => {
      if (node.name !== 'Link') return;
      const url = node.node.getChild('URL');
      const href = url ? state.doc.sliceString(url.from, url.to) : '';
      const path = notebookLinkPath(href, baseDir);
      if (path) seen.add(path);
    },
  });
  return [...seen];
}

// Marks every Link whose target `missing` reports absent; pure over parsed state.
export function brokenLinkDecorations(
  state: EditorState,
  baseDir: string,
  missing: (path: string) => boolean,
): DecorationSet {
  const decos: Range<Decoration>[] = [];
  syntaxTree(state).iterate({
    enter: (node) => {
      if (node.name !== 'Link') return;
      const url = node.node.getChild('URL');
      const href = url ? state.doc.sliceString(url.from, url.to) : '';
      const path = notebookLinkPath(href, baseDir);
      if (path && missing(path)) decos.push(BROKEN.range(node.from, node.to));
    },
  });
  return Decoration.set(decos, true);
}

export function brokenLinks(options: BrokenLinkOptions): Extension {
  const { existsFile, baseDir } = options;

  const plugin = ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;
      // path -> exists. A path is checked at most once until a revalidate clears it.
      private readonly cache = new Map<string, boolean>();
      // paths with an in-flight check, so concurrent rebuilds don't double-request.
      private readonly pending = new Set<string>();

      constructor(view: EditorView) {
        this.decorations = this.build(view);
      }

      update(update: ViewUpdate) {
        let revalidated = false;
        let refreshed = false;
        for (const tr of update.transactions) {
          for (const effect of tr.effects) {
            if (effect.is(revalidateBrokenLinks)) revalidated = true;
            if (effect.is(refreshBrokenLinks)) refreshed = true;
          }
        }
        if (revalidated) {
          for (const [path, exists] of this.cache) {
            if (!exists) this.cache.delete(path);
          }
        }
        if (update.docChanged || update.viewportChanged || refreshed || revalidated) {
          this.decorations = this.build(update.view);
        }
      }

      private build(view: EditorView): DecorationSet {
        if (!existsFile) return Decoration.none;
        const paths = notebookLinkPaths(view.state, baseDir);
        const toCheck = paths.filter((p) => !this.cache.has(p) && !this.pending.has(p));
        if (toCheck.length) this.scheduleChecks(view, toCheck);
        return brokenLinkDecorations(view.state, baseDir, (p) => this.cache.get(p) === false);
      }

      private scheduleChecks(view: EditorView, paths: string[]) {
        if (!existsFile) return;
        for (const path of paths) {
          if (this.pending.has(path) || this.cache.has(path)) continue;
          this.pending.add(path);
          existsFile(path)
            .then((res) => {
              this.cache.set(path, !!res.exists);
            })
            .catch(() => {
              // Inconclusive: record "exists" so no false flag paints; retried later.
              this.cache.set(path, true);
            })
            .finally(() => {
              this.pending.delete(path);
              // Guard the disposed-view race: unmount can land before this resolves.
              if (view.dom.isConnected) {
                view.dispatch({ effects: refreshBrokenLinks.of(null) });
              }
            });
        }
      }
    },
    { decorations: (v) => v.decorations },
  );

  const theme = EditorView.baseTheme({
    // !important outranks `.cm-md-link`: single-class marks tie on specificity.
    '.cm-md-link-broken': {
      color: 'var(--color-danger, #e5534b) !important',
      borderBottom: '1px dashed color-mix(in srgb, var(--color-danger, #e5534b) 50%, transparent)',
    },
    '.cm-md-link-broken::after': {
      content: '" ⚠"',
      fontSize: '10px',
    },
  });

  return [plugin, theme];
}
