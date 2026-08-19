// pi-facing entrypoint for the attn suite. pi loads this file's default
// export as an extension factory (verified against pi v0.80.10 source,
// packages/coding-agent/src/core/extensions/loader.ts: the default export
// must itself be a function, called as `factory(api)` on every session
// transition — resume/fork/new/reload all re-run this factory in-process).
//
// This file stays thin: read env once, build one AttnPiSuite in a
// process-wide slot (module scope is NOT one — see ./singleton), and
// re-register it against the current pi/ctx on each factory call. All testable
// behavior lives in ./core, which has no pi import so it can run under
// `bun test`.
import { VERSION } from "@earendil-works/pi-coding-agent";
import { AutoMode, type AutoModePiLike } from "../automode/mode";
import { denialLedgerFor } from "../automode/ledger";
import { attnAutoModeSource } from "../automode/source";
import { AttnPiSuite, type ExtensionAPILike } from "./core";
import { processSingleton } from "./singleton";

const suite = processSingleton(
  "attn:pi-suite",
  () =>
    new AttnPiSuite({
      socketPath: process.env.ATTN_PI_SUITE_SOCKET,
      token: process.env.ATTN_PI_TOKEN,
      piVersion: VERSION,
    }),
);

// Auto mode exists only when attn sent a config. A bare pi that loads this
// suite registers no command, no flag and no handlers for it, and behaves
// exactly as it does without the file. Every denial is written to the local
// ledger first and then rides the relay the rest of the suite reports on: the
// relay is what lets attn notify and list it live, the ledger is what keeps it
// when the relay is not there to carry it.
const autoMode = processSingleton("attn:pi-automode", () => {
  const source = attnAutoModeSource(process.env);
  return source
    ? new AutoMode({
        config: source.config,
        notice: source.problem,
        ledger: denialLedgerFor(process.env),
        onDenial: (denial) => suite.reportDenial(denial),
        onWaitingForUser: (waiting) => suite.reportApprovalWindow(waiting),
      })
    : undefined;
});

export default function attnPiSuite(pi: ExtensionAPILike & AutoModePiLike): void {
  suite.register(pi);
  autoMode?.register(pi);
}
