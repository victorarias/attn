// Auto mode for a pi that knows nothing about attn: `pi -e automode.js`. With
// no config anywhere, auto mode is built but starts off. One AutoMode at module
// scope, so the user's `/auto` survives a session transition.
import { readFileSync } from "node:fs";
import { denialLedgerFor } from "./ledger";
import { AutoMode, type AutoModePiLike } from "./mode";
import { standaloneAutoModeSource } from "./source";

const source = standaloneAutoModeSource(process.env, readConfigFile);
const autoMode = new AutoMode({
  config: source.config,
  notice: source.problem,
  // No relay here, so the ledger is the only record a denial leaves.
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
