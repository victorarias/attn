import { describe, expect, it } from "vitest";
import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import remarkRehype from "remark-rehype";
import type { Element, Root, RootContent } from "hast";
import rehypeSourceAnchors, {
  type RehypeSourceAnchorsOptions,
} from "./rehypeSourceAnchors";

async function render(
  markdown: string,
  options?: RehypeSourceAnchorsOptions,
): Promise<Root> {
  const processor = unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(remarkRehype)
    .use(rehypeSourceAnchors, options ?? {});
  const mdast = processor.parse(markdown);
  return (await processor.run(mdast)) as Root;
}

interface Anchor {
  tag: string;
  id: string;
  line: number | undefined;
  lineEnd: number | undefined;
}

function collectAnchors(tree: Root): Anchor[] {
  const anchors: Anchor[] = [];
  const walk = (node: Root | RootContent): void => {
    if (node.type === "element") {
      const element = node as Element;
      const id = element.properties?.dataBlockId;
      if (typeof id === "string") {
        anchors.push({
          tag: element.tagName,
          id,
          line: element.properties?.dataSourceLine as number | undefined,
          lineEnd: element.properties?.dataSourceLineEnd as number | undefined,
        });
      }
    }
    if ("children" in node) {
      for (const child of node.children) walk(child);
    }
  };
  walk(tree);
  return anchors;
}

describe("rehypeSourceAnchors", () => {
  it("stamps top-level blocks with 1-based source lines", async () => {
    const md = [
      "# Title", //                 line 1
      "", //                        line 2
      "First paragraph.", //        line 3
      "", //                        line 4
      "Second paragraph", //        line 5
      "spanning two lines.", //     line 6
    ].join("\n");
    const anchors = collectAnchors(await render(md));
    expect(anchors).toEqual([
      { tag: "h1", id: "b0-heading", line: 1, lineEnd: 1 },
      { tag: "p", id: "b1-paragraph", line: 3, lineEnd: 3 },
      { tag: "p", id: "b2-paragraph", line: 5, lineEnd: 6 },
    ]);
  });

  it("anchors each li individually, including nested lists", async () => {
    const md = [
      "- alpha", //      line 1
      "- beta", //       line 2
      "  - beta-one", // line 3
      "  - beta-two", // line 4
      "- gamma", //      line 5
    ].join("\n");
    const anchors = collectAnchors(await render(md));
    expect(anchors).toEqual([
      { tag: "ul", id: "b0-list", line: 1, lineEnd: 5 },
      { tag: "li", id: "b1-list-item", line: 1, lineEnd: 1 },
      { tag: "li", id: "b2-list-item", line: 2, lineEnd: 4 },
      { tag: "li", id: "b3-list-item", line: 3, lineEnd: 3 },
      { tag: "li", id: "b4-list-item", line: 4, lineEnd: 4 },
      { tag: "li", id: "b5-list-item", line: 5, lineEnd: 5 },
    ]);
  });

  it("covers loose list items spanning multiple lines", async () => {
    const md = [
      "- first item", //          line 1
      "", //                      line 2
      "  continued paragraph", // line 3
      "", //                      line 4
      "- second", //              line 5
    ].join("\n");
    const anchors = collectAnchors(await render(md));
    expect(anchors).toEqual([
      { tag: "ul", id: "b0-list", line: 1, lineEnd: 5 },
      { tag: "li", id: "b1-list-item", line: 1, lineEnd: 3 },
      { tag: "li", id: "b2-list-item", line: 5, lineEnd: 5 },
    ]);
  });

  it("anchors ordered lists and gfm task-list items", async () => {
    const md = [
      "1. one", //        line 1
      "2. two", //        line 2
      "", //              line 3
      "- [ ] todo", //    line 4
      "- [x] done", //    line 5
    ].join("\n");
    const anchors = collectAnchors(await render(md));
    expect(anchors).toEqual([
      { tag: "ol", id: "b0-list", line: 1, lineEnd: 2 },
      { tag: "li", id: "b1-list-item", line: 1, lineEnd: 1 },
      { tag: "li", id: "b2-list-item", line: 2, lineEnd: 2 },
      { tag: "ul", id: "b3-list", line: 4, lineEnd: 5 },
      { tag: "li", id: "b4-list-item", line: 4, lineEnd: 4 },
      { tag: "li", id: "b5-list-item", line: 5, lineEnd: 5 },
    ]);
  });

  it("covers the whole code fence including the fence markers", async () => {
    const md = [
      "Intro.", //         line 1
      "", //               line 2
      "```ts", //          line 3
      "const x = 1;", //   line 4
      "```", //            line 5
      "", //               line 6
      "After.", //         line 7
    ].join("\n");
    const anchors = collectAnchors(await render(md));
    expect(anchors).toEqual([
      { tag: "p", id: "b0-paragraph", line: 1, lineEnd: 1 },
      { tag: "pre", id: "b1-code", line: 3, lineEnd: 5 },
      { tag: "p", id: "b2-paragraph", line: 7, lineEnd: 7 },
    ]);
  });

  it("anchors blockquotes as one block without stamping inner paragraphs", async () => {
    const md = [
      "> quoted line one", // line 1
      "> quoted line two", // line 2
    ].join("\n");
    const tree = await render(md);
    const anchors = collectAnchors(tree);
    expect(anchors).toEqual([
      { tag: "blockquote", id: "b0-blockquote", line: 1, lineEnd: 2 },
    ]);
  });

  it("anchors gfm tables spanning header through last row", async () => {
    const md = [
      "Before.", //       line 1
      "", //              line 2
      "| a | b |", //     line 3
      "| - | - |", //     line 4
      "| 1 | 2 |", //     line 5
      "| 3 | 4 |", //     line 6
    ].join("\n");
    const anchors = collectAnchors(await render(md));
    expect(anchors).toEqual([
      { tag: "p", id: "b0-paragraph", line: 1, lineEnd: 1 },
      { tag: "table", id: "b1-table", line: 3, lineEnd: 6 },
    ]);
  });

  it("anchors thematic breaks and typed headings", async () => {
    const md = [
      "## Section", // line 1
      "", //           line 2
      "***", //        line 3
      "", //           line 4
      "Text.", //      line 5
    ].join("\n");
    const anchors = collectAnchors(await render(md));
    expect(anchors).toEqual([
      { tag: "h2", id: "b0-heading", line: 1, lineEnd: 1 },
      { tag: "hr", id: "b1-thematic-break", line: 3, lineEnd: 3 },
      { tag: "p", id: "b2-paragraph", line: 5, lineEnd: 5 },
    ]);
  });

  it("keeps raw-file lines correct in blank-line-heavy documents", async () => {
    const md = [
      "", //                line 1
      "", //                line 2
      "Paragraph one.", //  line 3
      "", //                line 4
      "", //                line 5
      "", //                line 6
      "Paragraph two.", //  line 7
      "", //                line 8
    ].join("\n");
    const anchors = collectAnchors(await render(md));
    expect(anchors).toEqual([
      { tag: "p", id: "b0-paragraph", line: 3, lineEnd: 3 },
      { tag: "p", id: "b1-paragraph", line: 7, lineEnd: 7 },
    ]);
  });

  it("applies lineOffset so stamped lines reflect the raw file after frontmatter stripping", async () => {
    const raw = [
      "---", //          raw line 1
      "title: Test", //  raw line 2
      "date: 2026", //   raw line 3
      "---", //          raw line 4
      "", //             raw line 5
      "# Heading", //    raw line 6
      "", //             raw line 7
      "Body text.", //   raw line 8
      "- item", //       raw line 9
    ].join("\n");

    // Simulate the caller stripping the YAML frontmatter block (lines 1-4)
    // before parsing, and passing the number of removed lines as the offset.
    const rawLines = raw.split("\n");
    const closingFenceIndex = rawLines.indexOf("---", 1);
    const strippedLineCount = closingFenceIndex + 1; // 4
    const content = rawLines.slice(strippedLineCount).join("\n");

    const anchors = collectAnchors(
      await render(content, { lineOffset: strippedLineCount }),
    );
    expect(anchors).toEqual([
      { tag: "h1", id: "b0-heading", line: 6, lineEnd: 6 },
      { tag: "p", id: "b1-paragraph", line: 8, lineEnd: 8 },
      { tag: "ul", id: "b2-list", line: 9, lineEnd: 9 },
      { tag: "li", id: "b3-list-item", line: 9, lineEnd: 9 },
    ]);

    // Sanity: the stamped lines point at the expected raw-file text.
    expect(rawLines[6 - 1]).toBe("# Heading");
    expect(rawLines[8 - 1]).toBe("Body text.");
    expect(rawLines[9 - 1]).toBe("- item");
  });

  it("defaults lineOffset to 0", async () => {
    const withExplicitZero = collectAnchors(
      await render("Hello.", { lineOffset: 0 }),
    );
    const withDefault = collectAnchors(await render("Hello."));
    expect(withDefault).toEqual(withExplicitZero);
    expect(withDefault).toEqual([
      { tag: "p", id: "b0-paragraph", line: 1, lineEnd: 1 },
    ]);
  });

  it("is deterministic: identical content yields identical anchors across renders", async () => {
    const md = [
      "# Doc",
      "",
      "Paragraph.",
      "",
      "- a",
      "- b",
      "",
      "```sh",
      "echo hi",
      "```",
      "",
      "> quote",
      "",
      "| x |",
      "| - |",
      "| 1 |",
    ].join("\n");
    const first = collectAnchors(await render(md, { lineOffset: 2 }));
    const second = collectAnchors(await render(md, { lineOffset: 2 }));
    expect(second).toEqual(first);
    expect(first.map((a) => a.id)).toEqual([
      "b0-heading",
      "b1-paragraph",
      "b2-list",
      "b3-list-item",
      "b4-list-item",
      "b5-code",
      "b6-blockquote",
      "b7-table",
    ]);
  });

  it("does not stamp non-anchored descendants (inline code, table cells, links)", async () => {
    const md = [
      "Some `code` and [a link](https://example.com).",
      "",
      "| a |",
      "| - |",
      "| 1 |",
    ].join("\n");
    const tree = await render(md);
    const stampedTags = collectAnchors(tree).map((a) => a.tag);
    expect(stampedTags).toEqual(["p", "table"]);
  });
});
