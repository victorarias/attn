// Auto mode for a pi that knows nothing about attn: `pi -e automode.js`.
//
// Config comes from ATTN_PI_AUTOMODE_CONFIG when something set it, otherwise
// from `attn-automode.json` under pi's config directory (`PI_CODING_AGENT_DIR`,
// or `~/.pi/agent`) — the same JSON schema attn stores. With neither, auto
// mode is built but starts off: `--auto` or `/auto` turns it on.
//
// Like suite/index.ts this file stays thin: one AutoMode at module scope, so
// the user's `/auto` survives every session transition in this process, and
// `register` re-binds it to each factory run.
import { readFileSync } from "node:fs";
import { denialLedgerFor } from "./ledger";
import { AutoMode, type AutoModePiLike } from "./mode";
import { standaloneAutoModeSource } from "./source";

const source = standaloneAutoModeSource(process.env, readConfigFile);
const autoMode = new AutoMode({
  config: source.config,
  notice: source.problem,
  // Bare pi has no relay to report a denial over, so the ledger is the only
  // record one leaves at all.
  ledger: denialLedgerFor(process.env),
});

export default function attnAutoMode(pi: AutoModePiLike): void {
  autoMode.register(pi);
}

/** Undefined for "there is no file", which is the ordinary case, not an error. */
function readConfigFile(path: string): string | undefined {
  try {
    return readFileSync(path, "utf8");
  } catch (error) {
    if ((error as NodeJS.ErrnoException)?.code === "ENOENT") return undefined;
    throw error;
  }
}
