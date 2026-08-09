/**
 * Smart punctuation + emoji shortcodes for the markdown reader.
 *
 * `transformText` is the single source of truth and must stay pure and stable:
 * annotation text-search reproduces rendered text from source bytes, i.e.
 * `transformText(sourceText) === renderedText`. Order is load-bearing — emoji
 * first, then smart punctuation. The plugin applies it to hast text nodes only,
 * so URLs (element properties) are untouched while link labels transform.
 *
 * The en-dash rule is narrowed on purpose: `--` becomes `–` ONLY between two
 * digits, so CLI flags such as `bun --watch` survive. Do NOT swap in
 * remark-smartypants, which rewrites `--` everywhere.
 */

import type { Element, Root, RootContent } from "hast";
import type { Text } from "hast";

/** Hand-rolled 32-entry map, NOT the full gemoji set; keep it stable. */
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

export function replaceEmojiShortcodes(text: string): string {
  return text.replace(/:([a-z_]+):/g, (match, code: string) => EMOJI_MAP[code] ?? match);
}

/**
 * Exact chain, in order: ellipsis, em dash, narrowed en dash, curly quotes.
 * `prevChar` is the rendered character before `text`, empty at a block start.
 * hast splits prose at inline boundaries, so a quote at position 0 is not
 * necessarily an opener — it opens only when prevChar is empty or [\s([{],
 * which is what keeps `transformText(sourceSlice) === renderedText` true.
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
 * Emoji shortcodes then smart punctuation; pure and idempotent. `prevChar`
 * disambiguates a leading quote — see applySmartPunctuation.
 */
export function transformText(text: string, prevChar = ""): string {
  return applySmartPunctuation(replaceEmojiShortcodes(text), prevChar);
}

/** Subtrees whose text must NEVER be transformed: verbatim and non-prose. */
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
 * Last rendered character in a node, for quote context: skipped subtrees still
 * contribute their trailing character, and `<br>` counts as a newline.
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
 * Mutates text-node values in place, never element properties. The walk
 * threads the previous rendered character through the tree so a quote that
 * starts a text node curls the right way; block boundaries need no case of
 * their own, as mdast-util-to-hast emits `\n` nodes that reset the context.
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
