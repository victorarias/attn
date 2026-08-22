import { readFileSync } from "node:fs";
import { denialLedgerFor } from "./ledger";
import { AutoMode, type AutoModePiLike } from "./mode";
import { standaloneAutoModeSource } from "./source";

const source = standaloneAutoModeSource(process.env, readConfigFile);
const autoMode = new AutoMode({
  config: source.config,
  notice: source.problem,

  ledger: denialLedgerFor(process.env),
});

export default function attnAutoMode(pi: AutoModePiLike): void {
  autoMode.register(pi);
}

function readConfigFile(path: string): string | undefined {
  try {
    return readFileSync(path, "utf8");
  } catch (error) {
    if ((error as NodeJS.ErrnoException)?.code === "ENOENT") return undefined;
    throw error;
  }
}
