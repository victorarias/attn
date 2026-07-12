// Broken-link flags for the live markdown editor. An in-notebook link whose target
// note does not exist is flagged red with a ⚠, so a typo or a not-yet-created note
// is visible while you write — matching the prototype's `a.broken` affordance.
//
// Existence is checked asynchronously against the daemon (fs_exists), so this lives
// in its own ViewPlugin rather than the sync `liveMarkdownPreview.buildDecorations`:
// a result that arrives later forces a rebuild via a refresh effect, and a per-
// editor cache means each path is checked once, not on every keystroke. External
// links (http:, mailto:, …) and in-document anchors (#section) are never flagged.

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

// The outcome of an existence check — the shape of the daemon's FsExistsResult,
// declared structurally so this module does not depend on the socket hook.
export interface ExistsCheck {
  path: string;
  exists: boolean;
}

export interface BrokenLinkOptions {
  // Check whether an in-notebook path exists, without reading it. Resolves with
  // { exists }. A rejection (transport/daemon error, or a path the daemon refuses
  // to resolve) leaves the link UNFLAGGED — a link is only ever flagged broken on a
  // conclusive "does not exist", never on an inconclusive check.
  existsFile?: (path: string) => Promise<ExistsCheck>;
  // The editing note's directory, root-relative ('' at the root) — bare-relative
  // link targets resolve against this before the existence check.
  baseDir: string;
}

// Fired when an async existence check resolves: rebuild so a now-known result paints.
const refreshBrokenLinks = StateEffect.define<null>();

// Fired by the editor when the notebook changed on disk (an agent/external write):
// drop the cached "missing" verdicts so a link to a just-created note re-checks.
// "Exists" verdicts are kept — a note rarely vanishes mid-session, and keeping them
// avoids re-checking every link on the user's own autosave (which also fires this).
export const revalidateBrokenLinks = StateEffect.define<null>();

const BROKEN = Decoration.mark({
  class: 'cm-md-link-broken',
  attributes: { title: 'Link target not found in the notebook' },
});

// Resolve a link href to the notebook path whose existence decides whether it is
// broken, or null when the href is not an in-notebook note reference (and so must
// never be flagged): an external URL (has a scheme like http:/mailto:), a protocol-
// relative URL, a pure in-document anchor (#section), or empty. A bare-relative
// target (`sibling.md`) resolves against `baseDir` — the editing note's own
// directory — before the check; a root-absolute target (`/knowledge/x.md`) ignores
// it. Resolution (including `..` and the `#fragment`/`?query` tails) happens here,
// frontend-side, via the shared resolver — the daemon itself still only ever sees
// and interprets paths root-relative.
export function notebookLinkPath(href: string, baseDir: string): string | null {
  const resolved = resolveNotebookLink(href, baseDir);
  return resolved.kind === 'note' ? resolved.path : null;
}

// The distinct in-notebook link paths in the document — the set whose existence
// decides broken-ness. Pure over the parsed state (no view, no daemon) so the
// plugin's "what do I need to check" logic is unit-testable headlessly.
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

// The broken-link marks for the document: a red ⚠ flag over every Link whose target
// path `missing` reports absent. Pure over the parsed state and a verdict predicate
// (the plugin backs `missing` with its existence cache), mirroring the headless
// shape of liveMarkdownPreview.buildDecorations and frontmatterCardDecorations.
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
              // Inconclusive: record "exists" so an unreachable/invalid check never
              // paints a false broken flag. A later revalidate will retry it.
              this.cache.set(path, true);
            })
            .finally(() => {
              this.pending.delete(path);
              // Repaint with the new verdict. Guard the disposed-view race: a
              // navigation or unmount can land between request and resolve.
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
    // Red, dashed-underlined, with a trailing ⚠ — the prototype's `a.broken`. The
    // !important on color outranks the overlapping `.cm-md-link` accent (both are
    // single-class marks wrapping the same range, so specificity alone is a tie).
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
