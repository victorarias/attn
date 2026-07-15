/**
 * proseTransforms — smart punctuation + emoji shortcodes for the markdown
 * reader, ported from plannotator's `inlineTransforms.ts`.
 *
 * Two exports:
 *
 * - `transformText(text)` — the pure, deterministic string transform. PR4's
 *   annotation text-search must reproduce the rendered text from source bytes
 *   (`transformText(sourceText) === renderedText`), so this function is the
 *   single source of truth and must stay pure and stable.
 * - default `rehypeProseTransforms` — a rehype plugin applying the transform
 *   to hast text nodes, skipping code/pre (and other verbatim) subtrees.
 *   Link URLs live in `properties.href`, never in text nodes, so they are
 *   untouched by construction; link *label* text is transformed (matching
 *   plannotator, where labels go through the plain-text path).
 *
 * Transform order is load-bearing: emoji first, then smart punctuation
 * (`transformText = applySmartPunctuation(replaceEmojiShortcodes(text))`).
 *
 * The narrowed en-dash rule (plannotator's, load-bearing): `--` becomes `–`
 * ONLY between two digits (`3--5` → `3–5`). A broader rule would rewrite CLI
 * flags (`bun --watch` → `bun –watch`). Letter-to-letter en-dashes are
 * deliberately sacrificed so `--flags` are never corrupted. Do NOT swap this
 * for remark-smartypants — its dash handling rewrites `--` everywhere.
 */

import type { Element, Root, RootContent } from "hast";
import type { Text } from "hast";

/**
 * Exact 32-entry shortcode map (hand-rolled — NOT the full gemoji set).
 * Keep identical to plannotator's for PR4 determinism.
 */
const EMOJI_MAP: Record<string, string> = {
  smile: "😄",
  heart: "❤️",
  thumbsup: "👍",
  thumbsdown: "👎",
  fire: "🔥",
  star: "⭐",
  tada: "🎉",
  rocket: "🚀",
  bug: "🐛",
  sparkles: "✨",
  warning: "⚠️",
  white_check_mark: "✅",
  x: "❌",
  eyes: "👀",
  wave: "👋",
  thinking: "🤔",
  ok: "🆗",
  construction: "🚧",
  boom: "💥",
  gear: "⚙️",
  hourglass: "⏳",
  zap: "⚡",
  lock: "🔒",
  unlock: "🔓",
  memo: "📝",
  book: "📖",
  package: "📦",
  hammer: "🔨",
  checkered_flag: "🏁",
  question: "❓",
  exclamation: "❗",
  bulb: "💡",
};

/** `:shortcode:` → emoji. Unknown shortcodes are left exactly as written. */
export function replaceEmojiShortcodes(text: string): string {
  return text.replace(/:([a-z_]+):/g, (match, code: string) => EMOJI_MAP[code] ?? match);
}

/**
 * Smart punctuation. Exact replacement chain, in this order (plannotator's
 * smartypants): ellipsis, em dash, narrowed en dash, curly quotes.
 *
 * `prevChar` is the rendered character immediately BEFORE `text` (empty string
 * when `text` starts its block). hast splits prose at inline-element
 * boundaries, so a quote at position 0 of a text node is NOT necessarily an
 * opening quote — `He said "**hi**".` puts `".` in its own node right after
 * `</strong>`. A start-of-node quote opens only when prevChar is empty or an
 * opener context ([\s([{]); otherwise it closes, matching what the same
 * characters produce when transformed as one unsplit string (the PR4
 * `transformText(sourceSlice) === renderedText` contract depends on this).
 */
export function applySmartPunctuation(text: string, prevChar = ""): string {
  const opensAtStart = prevChar === "" || /[\s([{]/.test(prevChar);
  const openOrKeep = (match: string, pre: string, open: string): string =>
    pre === "" && !opensAtStart ? match : pre + open;
  return text
    .replace(/\.{3}/g, "…")
    .replace(/---/g, "—") // em dash
    .replace(/(\d)--(?=\d)/g, "$1–") // en dash: NUMERIC RANGES ONLY — never --flags
    .replace(/(^|[\s([{])"/g, (m, pre: string) => openOrKeep(m, pre, "“")) // opening double quote
    .replace(/"/g, "”") // remaining doubles close
    .replace(/(^|[\s([{])'/g, (m, pre: string) => openOrKeep(m, pre, "‘")) // opening single quote
    .replace(/'/g, "’"); // remaining singles close / apostrophe
}

/**
 * The full prose transform: emoji shortcodes first, then smart punctuation.
 * Pure and idempotent — `transformText(transformText(s)) === transformText(s)`.
 * `prevChar` (optional) disambiguates a leading quote; see applySmartPunctuation.
 */
export function transformText(text: string, prevChar = ""): string {
  return applySmartPunctuation(replaceEmojiShortcodes(text), prevChar);
}

/**
 * Subtrees whose text must NEVER be transformed: code (inline + block via
 * `pre`), keyboard/sample/variable verbatim text, math (remark-math output
 * uses `<math>`/katex spans — defensive, math is out of PR3 scope), and
 * non-prose containers.
 */
const SKIP_TAGS = new Set([
  "code",
  "pre",
  "kbd",
  "samp",
  "var",
  "script",
  "style",
  "textarea",
  "title",
  "svg",
  "math",
]);

/** Defensive: skip math-ish elements by class (e.g. `math math-inline`, katex). */
function isMathLike(node: Element): boolean {
  const className: unknown = node.properties?.className;
  const classes = Array.isArray(className)
    ? className.map(String)
    : typeof className === "string"
      ? className.split(/\s+/)
      : [];
  return classes.some((c) => c === "math" || c.startsWith("math-") || c.startsWith("katex"));
}

function isElement(node: Root | RootContent): node is Element {
  return node.type === "element";
}

function isText(node: RootContent): node is Text {
  return node.type === "text";
}

/**
 * Last rendered character inside a node, for the prev-char quote context:
 * skipped subtrees (inline code, kbd, …) still contribute their trailing
 * character, so `` `foo`" `` closes. `<br>` counts as a newline.
 */
function trailingTextChar(node: RootContent): string | null {
  if (isText(node)) {
    return node.value.length > 0 ? node.value.slice(-1) : null;
  }
  if (isElement(node)) {
    if (node.tagName === "br") {
      return "\n";
    }
    for (let i = node.children.length - 1; i >= 0; i--) {
      const char = trailingTextChar(node.children[i]);
      if (char !== null) {
        return char;
      }
    }
  }
  return null;
}

/**
 * Rehype plugin. Usable directly in react-markdown's `rehypePlugins`:
 *
 *   rehypePlugins={[rehypeProseTransforms]}
 *
 * Mutates text-node values in place; never touches element properties (so
 * hrefs, srcs, and anchoring data attributes are untouched by construction).
 *
 * The walk threads the previous rendered character through the whole tree so
 * a quote that starts a text node (i.e. directly follows an inline element)
 * still curls the right way. Block boundaries need no special-casing:
 * mdast-util-to-hast emits `\n` text nodes between block elements, which
 * reset the context to whitespace (= opening).
 */
export default function rehypeProseTransforms() {
  return (tree: Root): void => {
    const ctx = { prev: "" };
    const walk = (node: Root | RootContent): void => {
      if (isElement(node) && (SKIP_TAGS.has(node.tagName) || isMathLike(node))) {
        return;
      }
      if ("children" in node) {
        for (const child of node.children) {
          if (isText(child)) {
            child.value = transformText(child.value, ctx.prev);
            if (child.value.length > 0) {
              ctx.prev = child.value.slice(-1);
            }
          } else {
            walk(child);
            const char = trailingTextChar(child);
            if (char !== null) {
              ctx.prev = char;
            }
          }
        }
      }
    };
    walk(tree);
  };
}
