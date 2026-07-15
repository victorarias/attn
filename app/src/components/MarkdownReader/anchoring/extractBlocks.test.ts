/**
 * Smoke test for extractBlockTexts output shape — the full anchoring fixture
 * suite (pipeline parity, resolve/rebase cases) lands separately.
 */

import { describe, expect, it } from "vitest";
import { extractBlockTexts } from "./extractBlocks";
import { createAnchor } from "./create";
import { resolveAnchor } from "./resolve";

const DOC = [
  "# Title :rocket:",
  "",
  'A paragraph with "quotes" and code.',
  "",
  "- first item",
  "- second item",
  "",
  "```js",
  "const x = 1;",
  "```",
].join("\n");

describe("extractBlockTexts — smoke", () => {
  it("emits stamped blocks in document order with rendered text", () => {
    const blocks = extractBlockTexts(DOC);
    const byId = new Map(blocks.map((b) => [b.blockId, b]));

    expect(blocks.map((b) => b.blockId)).toEqual([
      "b0-heading",
      "b1-paragraph",
      "b2-list",
      "b3-list-item",
      "b4-list-item",
      "b5-code",
    ]);

    // Prose transforms already applied: emoji shortcode + curly quotes.
    expect(byId.get("b0-heading")!.text).toBe("Title 🚀");
    expect(byId.get("b1-paragraph")!.text).toBe("A paragraph with “quotes” and code.");

    // List items nest inside the list: depth, parentId, startInParent, and
    // the containment contract (child text is a slice of the parent's).
    const list = byId.get("b2-list")!;
    const first = byId.get("b3-list-item")!;
    expect(list.depth).toBe(0);
    expect(list.parentId).toBeNull();
    expect(first.depth).toBe(1);
    expect(first.parentId).toBe("b2-list");
    expect(first.text).toBe("first item");
    expect(
      list.text.slice(first.startInParent, first.startInParent + first.text.length),
    ).toBe(first.text);

    // pre trailing-newline rule: hast keeps a trailing \n, the DOM does not.
    expect(byId.get("b5-code")!.text).toBe("const x = 1;");

    // Raw-file line stamps.
    expect(byId.get("b1-paragraph")!.startLine).toBe(3);
    expect(byId.get("b5-code")!.startLine).toBe(8);
    expect(byId.get("b5-code")!.endLine).toBe(10);
  });

  it("createAnchor + resolveAnchor round-trip on unchanged content", () => {
    const blocks = extractBlockTexts(DOC);
    const paragraph = blocks.find((b) => b.blockId === "b1-paragraph")!;
    const start = paragraph.text.indexOf("“quotes”");
    const anchor = createAnchor(DOC, "b1-paragraph", start, start + "“quotes”".length);
    expect(anchor).not.toBeNull();
    expect(anchor!.exact).toBe("“quotes”");
    expect(anchor!.end - anchor!.start).toBe(anchor!.exact.length);

    const resolved = resolveAnchor(DOC, anchor!);
    expect(resolved).toEqual({
      state: "exact",
      blockId: "b1-paragraph",
      start: anchor!.start,
      end: anchor!.end,
    });
  });

  it("createAnchor rejects whitespace-only selections", () => {
    expect(createAnchor(DOC, "b1-paragraph", 0, 1)).not.toBeNull(); // "A"
    const blocks = extractBlockTexts(DOC);
    const paragraph = blocks.find((b) => b.blockId === "b1-paragraph")!;
    const spaceAt = paragraph.text.indexOf(" ");
    expect(createAnchor(DOC, "b1-paragraph", spaceAt, spaceAt + 1)).toBeNull();
    expect(createAnchor(DOC, "b1-paragraph", 3, 3)).toBeNull(); // empty
  });
});
