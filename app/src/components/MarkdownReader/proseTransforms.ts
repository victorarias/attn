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
 */
export function applySmartPunctuation(text: string): string {
  return text
    .replace(/\.{3}/g, "…")
    .replace(/---/g, "—") // em dash
    .replace(/(\d)--(?=\d)/g, "$1–") // en dash: NUMERIC RANGES ONLY — never --flags
    .replace(/(^|[\s([{])"/g, "$1“") // opening double quote
    .replace(/"/g, "”") // remaining doubles close
    .replace(/(^|[\s([{])'/g, "$1‘") // opening single quote
    .replace(/'/g, "’"); // remaining singles close / apostrophe
}

/**
 * The full prose transform: emoji shortcodes first, then smart punctuation.
 * Pure and idempotent — `transformText(transformText(s)) === transformText(s)`.
 */
export function transformText(text: string): string {
  return applySmartPunctuation(replaceEmojiShortcodes(text));
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
 * Rehype plugin. Usable directly in react-markdown's `rehypePlugins`:
 *
 *   rehypePlugins={[rehypeProseTransforms]}
 *
 * Mutates text-node values in place; never touches element properties (so
 * hrefs, srcs, and anchoring data attributes are untouched by construction).
 */
export default function rehypeProseTransforms() {
  return (tree: Root): void => {
    const walk = (node: Root | RootContent): void => {
      if (isElement(node) && (SKIP_TAGS.has(node.tagName) || isMathLike(node))) {
        return;
      }
      if ("children" in node) {
        for (const child of node.children) {
          if (isText(child)) {
            child.value = transformText(child.value);
          } else {
            walk(child);
          }
        }
      }
    };
    walk(tree);
  };
}
