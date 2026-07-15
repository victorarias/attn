import { describe, expect, it } from "vitest";
import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import remarkRehype from "remark-rehype";
import type { Element, Root, RootContent } from "hast";
import rehypeProseTransforms, {
  applySmartPunctuation,
  replaceEmojiShortcodes,
  transformText,
} from "./proseTransforms";

describe("transformText — smart quotes", () => {
  it("curls a double-quoted word at start of string", () => {
    expect(transformText('"hello"')).toBe("“hello”");
  });

  it("curls double quotes after a space", () => {
    expect(transformText('He said "hi" to me')).toBe("He said “hi” to me");
  });

  it("opens after ( [ and {", () => {
    expect(transformText('("a" ["b" {"c"')).toBe("(“a” [“b” {“c”");
  });

  it("closes a double quote that follows punctuation", () => {
    expect(transformText('"hi!"')).toBe("“hi!”");
  });

  it("handles multiple quoted spans in one string", () => {
    expect(transformText('"a" and "b"')).toBe("“a” and “b”");
  });

  it("curls single quotes", () => {
    expect(transformText("'hello'")).toBe("‘hello’");
  });

  it("turns mid-word apostrophes into right single quotes", () => {
    expect(transformText("don't stop; it's Victor's")).toBe(
      "don’t stop; it’s Victor’s",
    );
  });

  it("a quote not preceded by whitespace/open-bracket closes", () => {
    expect(transformText('5" nail')).toBe("5” nail");
  });
});

describe("transformText — ellipsis and dashes", () => {
  it("converts three dots to an ellipsis", () => {
    expect(transformText("wait...")).toBe("wait…");
  });

  it("converts triple hyphen to an em dash", () => {
    expect(transformText("yes --- no")).toBe("yes — no");
  });

  it("em dash between digits wins over en dash (--- runs first)", () => {
    expect(transformText("1---2")).toBe("1—2");
  });

  it("converts numeric ranges to en dashes", () => {
    expect(transformText("pages 3--5")).toBe("pages 3–5");
    expect(transformText("10--20 items")).toBe("10–20 items");
  });

  it("NEVER rewrites CLI flags", () => {
    expect(transformText("--watch")).toBe("--watch");
    expect(transformText("bun --watch")).toBe("bun --watch");
    expect(transformText("a --b")).toBe("a --b");
    expect(transformText("run --flag=value")).toBe("run --flag=value");
  });

  it("requires digits on BOTH sides of the en dash", () => {
    expect(transformText("1--x")).toBe("1--x");
    expect(transformText("x--2")).toBe("x--2");
  });

  it("leaves a lone double hyphen between words alone", () => {
    expect(transformText("this -- that")).toBe("this -- that");
  });
});

describe("transformText — emoji shortcodes", () => {
  it("replaces known shortcodes", () => {
    expect(transformText(":tada:")).toBe("🎉");
    expect(transformText("ship it :rocket: now")).toBe("ship it 🚀 now");
    expect(transformText(":fire::sparkles:")).toBe("🔥✨");
  });

  it("leaves unknown shortcodes untouched", () => {
    expect(transformText(":unknown_code:")).toBe(":unknown_code:");
    expect(transformText(":shrug: but :tada:")).toBe(":shrug: but 🎉");
  });

  it("only matches lowercase letters and underscores", () => {
    expect(transformText(":TADA:")).toBe(":TADA:");
    expect(transformText(":thumbs-up:")).toBe(":thumbs-up:");
    expect(transformText(":tada2:")).toBe(":tada2:");
  });

  it("covers the multi-word underscore codes", () => {
    expect(transformText(":white_check_mark: :checkered_flag:")).toBe("✅ 🏁");
  });

  it("runs emoji before punctuation (a shortcode inside quotes still resolves)", () => {
    expect(transformText('"look :eyes:"')).toBe("“look 👀”");
  });
});

describe("transformText — idempotency", () => {
  const samples = [
    'He said "don\'t" --- pages 3--5... :tada: bun --watch',
    "'a' \"b\" ... --- 1--2 :fire: :unknown:",
    "plain text with nothing special",
    '5" and rock \'n\' roll',
  ];

  it("transformText(transformText(s)) === transformText(s)", () => {
    for (const s of samples) {
      const once = transformText(s);
      expect(transformText(once)).toBe(once);
    }
  });
});

describe("unit exports", () => {
  it("replaceEmojiShortcodes only does emoji", () => {
    expect(replaceEmojiShortcodes(':tada: "x" --flag')).toBe('🎉 "x" --flag');
  });

  it("applySmartPunctuation only does punctuation", () => {
    expect(applySmartPunctuation(':tada: "x"')).toBe(":tada: “x”");
  });
});

// --- rehype plugin -------------------------------------------------------

async function render(markdown: string): Promise<Root> {
  const processor = unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(remarkRehype)
    .use(rehypeProseTransforms);
  const mdast = processor.parse(markdown);
  return (await processor.run(mdast)) as Root;
}

function collectText(node: Root | RootContent): string {
  if (node.type === "text") {
    return node.value;
  }
  if ("children" in node) {
    return node.children.map(collectText).join("");
  }
  return "";
}

function findElements(node: Root | RootContent, tagName: string): Element[] {
  const found: Element[] = [];
  if (node.type === "element" && node.tagName === tagName) {
    found.push(node);
  }
  if ("children" in node) {
    for (const child of node.children) {
      found.push(...findElements(child, tagName));
    }
  }
  return found;
}

describe("rehypeProseTransforms plugin", () => {
  it("transforms prose paragraphs", async () => {
    const tree = await render('He said "hi" --- pages 3--5... :tada:');
    expect(collectText(tree)).toContain("He said “hi” — pages 3–5… 🎉");
  });

  it("never touches inline code", async () => {
    const tree = await render('Run `bun --watch "now" :fire:` today');
    const [code] = findElements(tree, "code");
    expect(collectText(code)).toBe('bun --watch "now" :fire:');
  });

  it("never touches fenced code blocks", async () => {
    const tree = await render('```sh\necho "3--5" ... :tada:\n```');
    const [pre] = findElements(tree, "pre");
    expect(collectText(pre)).toBe('echo "3--5" ... :tada:\n');
  });

  it("keeps link URLs raw but transforms link labels", async () => {
    const tree = await render('[see "docs"](https://example.com/a--b?q=")');
    const [anchor] = findElements(tree, "a");
    expect(anchor.properties?.href).toBe('https://example.com/a--b?q=%22');
    expect(collectText(anchor)).toBe("see “docs”");
  });

  it("leaves gfm autolinked bare URLs untouched", async () => {
    const tree = await render("visit https://example.com/a--b now");
    const [anchor] = findElements(tree, "a");
    expect(anchor.properties?.href).toBe("https://example.com/a--b");
    expect(collectText(anchor)).toBe("https://example.com/a--b");
  });

  it("transforms text inside blockquotes and list items", async () => {
    const tree = await render('> "quoted"\n\n- item\'s :tada:');
    const [blockquote] = findElements(tree, "blockquote");
    const [li] = findElements(tree, "li");
    expect(collectText(blockquote)).toContain("“quoted”");
    expect(collectText(li)).toBe("item’s 🎉");
  });

  it("skips math-classed spans", async () => {
    const tree = await render("prose \"here\"");
    // Inject a math-like element the way remark-math/katex would emit it.
    const mathSpan: Element = {
      type: "element",
      tagName: "span",
      properties: { className: ["math", "math-inline"] },
      children: [{ type: "text", value: 'x -- y "raw"' }],
    };
    tree.children.push(mathSpan);
    rehypeProseTransforms()(tree);
    expect(collectText(mathSpan)).toBe('x -- y "raw"');
  });

  it("closes a quote that starts a text node right after an inline element", async () => {
    // hast splits at inline boundaries: `".` lands in its own text node after
    // </strong>; it must close, not open.
    const tree = await render('He said "**hi**". And \'*word*\' too.');
    expect(collectText(tree)).toBe("He said “hi”. And ‘word’ too.");
  });

  it("still opens a quote at the start of a new block after inline-heavy prose", async () => {
    const tree = await render('End **bold**\n\n"New paragraph"');
    expect(collectText(tree)).toContain("“New paragraph”");
  });

  it("closes a quote directly after inline code (skipped subtrees still set context)", async () => {
    const tree = await render('Run `foo`" done');
    expect(collectText(tree)).toContain("”" + " done");
    const [code] = findElements(tree, "code");
    expect(collectText(code)).toBe("foo");
  });

  it("is idempotent when run twice over the same tree", async () => {
    const tree = await render('"quotes" --- 3--5 :tada: and `code "x"`');
    const once = collectText(tree);
    rehypeProseTransforms()(tree);
    expect(collectText(tree)).toBe(once);
  });
});
