import { describe, expect, test } from "bun:test";
import {
  compareVersion,
  evaluateOpenCodeVersion,
  minimumOpenCodeVersion,
  parseStableVersion,
} from "../src/types";

describe("OpenCode version policy", () => {
  test.each([
    ["", "invalid"],
    ["garbage", "invalid"],
    ["1.17", "invalid"],
    ["1.17.15", "too_old"],
    ["1.17.16", "supported"],
    ["v1.17.16", "supported"],
    ["1.17.17", "supported"],
    ["1.18.0", "supported"],
    ["2.0.0", "supported"],
    ["1.18.0-beta.1", "invalid"],
    ["1.18.0+build.1", "invalid"],
  ] as const)("classifies %s as %s", (version, kind) => {
    expect(evaluateOpenCodeVersion(version).kind).toBe(kind);
  });

  test("normalizes a leading v and surrounding whitespace", () => {
    expect(parseStableVersion("  v1.17.16\n")).toEqual({
      raw: "1.17.16",
      major: 1,
      minor: 17,
      patch: 16,
    });
  });

  test("compares major, minor, and patch components numerically", () => {
    expect(compareVersion(parseStableVersion("1.17.16"), minimumOpenCodeVersion)).toBe(0);
    expect(compareVersion(parseStableVersion("1.18.0"), minimumOpenCodeVersion)).toBe(1);
    expect(compareVersion(parseStableVersion("1.17.15"), minimumOpenCodeVersion)).toBe(-1);
  });
});
