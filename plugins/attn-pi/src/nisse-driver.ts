import { existsSync } from "node:fs";
import type { AttnRPCClient } from "./attn-rpc";
import type { DriverRegisterResult, DriverSpawnParams, DriverSpawnResult } from "./types";

/**
 * The `nisse` agent: pi run headless through its SDK instead of its TUI. pi is
 * the engine; the host process, the envelope stream, the verbs and the pane are
 * attn's, which is why the agent carries a name of attn's own — see the nisse
 * entry in `docs/glossary.md`.
 *
 * This driver is a launcher and nothing else. It hands attn the argv for the
 * host binary and the model to run; everything after that — the envelope
 * stream, the prompt verbs, the process group — is between the host and attn's
 * daemon. That is why it registers no message_delivery: the `pi` agent needs a
 * relay to carry a message to a pi it does not own, and this one has a pipe to
 * a pi attn spawned itself, so the daemon writes the verb straight down it.
 *
 * `state_reporting` it does declare, and it means the same thing here as for
 * any other driver: this agent's state is declared, not observed. The
 * declaration reaches the daemon on the host's envelope stream rather than over
 * this connection, but the consequence is what the capability is for — the
 * evidence resolver must not have an opinion about a session it can see no
 * evidence for.
 *
 * `initial_prompt` it declares too, and that is what makes a `nisse` session
 * delegable: attn's delegation refuses any agent that cannot be launched with a
 * brief. The prompt travels in the environment rather than in argv — a brief is
 * multi-line prose, and argv is world-readable text that a sibling's `pkill -f`
 * can match on. The host decides when to deliver it; see
 * ATTN_NISSE_INITIAL_PROMPT in `host/index.ts`.
 */
export const nisseAgentName = "nisse";

/**
 * The model a `nisse` session runs when the launch pins none.
 *
 * Receipt: this is the provider/model pair the 2026-08-04 SDK spike and the
 * 2026-08-05 host measurements ran end to end on this machine. A user pin
 * (`--model`, or the `default_model_nisse` setting) overrides it, and the
 * host rejects an unknown pair by name rather than falling back.
 */
export const defaultNisseModel = "openai/gpt-5.6-luna";

export class NisseDriver {
  private readonly rpc: AttnRPCClient;
  // The launch command, argv[0] first. Compiled it is one path; from source it
  // is bun plus the entrypoint, which is why this is a command and not a path.
  private readonly hostCommand: string[];

  constructor(options: { rpc: AttnRPCClient; hostCommand: string[] }) {
    this.rpc = options.rpc;
    this.hostCommand = options.hostCommand;
  }

  async initialize(): Promise<void> {
    const result = await this.rpc.request<DriverRegisterResult>("driver.register", {
      agent: nisseAgentName,
      capabilities: {
        conversation: true,
        initial_prompt: true,
        model_pin: true,
        state_reporting: true,
      },
    });
    if (!result.ok) throw new Error("attn rejected nisse driver registration");
  }

  health(): { ok: boolean; message: string } {
    const entrypoint = this.hostCommand[this.hostCommand.length - 1] ?? "";
    return existsSync(entrypoint)
      ? { ok: true, message: `nisse is ready at ${entrypoint}` }
      : { ok: false, message: `nisse is missing at ${entrypoint}; this is a build/packaging bug` };
  }

  async spawn(params: DriverSpawnParams): Promise<DriverSpawnResult> {
    const health = this.health();
    if (!health.ok) throw new Error(health.message);
    const model = params.model?.trim() || defaultNisseModel;
    const initialPrompt = params.initial_prompt?.trim() ?? "";
    return {
      argv: [...this.hostCommand],
      cwd: params.cwd,
      env: {
        ATTN_NISSE_MODEL: model,
        ...(initialPrompt === "" ? {} : { ATTN_NISSE_INITIAL_PROMPT: initialPrompt }),
      },
    };
  }
}
