// pi-facing entrypoint for the attn suite. pi loads this file's default
// export as an extension factory (verified against pi v0.80.10 source,
// packages/coding-agent/src/core/extensions/loader.ts: the default export
// must itself be a function, called as `factory(api)` on every session
// transition — resume/fork/new/reload all re-run this factory in-process).
//
// This file stays thin: read env once, build one AttnPiSuite at module
// scope (survives every re-run of the factory below, per
// plugins/attn-pi/AGENTS.md's "pi lifecycle invariants"), and re-register it
// against the current pi/ctx on each factory call. All testable behavior
// lives in ./core, which has no pi import so it can run under `bun test`.
import { VERSION } from "@earendil-works/pi-coding-agent";
import { AttnPiSuite, type ExtensionAPILike } from "./core";

const suite = new AttnPiSuite({
  socketPath: process.env.ATTN_PI_SUITE_SOCKET,
  token: process.env.ATTN_PI_TOKEN,
  piVersion: VERSION,
});

export default function attnPiSuite(pi: ExtensionAPILike): void {
  suite.register(pi);
}
