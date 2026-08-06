import { existsSync } from "node:fs";
import type { AttnRPCClient } from "./attn-rpc";
import type { DriverRegisterResult, DriverSpawnParams, DriverSpawnResult } from "./types";

/**
 * The `pi-host` agent: pi run headless through its SDK instead of its TUI.
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
 */
export const hostAgentName = "pi-host";

/**
 * The model a `pi-host` session runs when the launch pins none.
 *
 * Receipt: this is the provider/model pair the 2026-08-04 SDK spike and the
 * 2026-08-05 host measurements ran end to end on this machine. A user pin
 * (`--model`, or the `default_model_pi-host` setting) overrides it, and the
 * host rejects an unknown pair by name rather than falling back.
 */
export const defaultHostModel = "openai/gpt-5.6-luna";

export class PiHostDriver {
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
      agent: hostAgentName,
      capabilities: {
        conversation: true,
        model_pin: true,
        state_reporting: true,
      },
    });
    if (!result.ok) throw new Error("attn rejected pi-host driver registration");
  }

  health(): { ok: boolean; message: string } {
    const entrypoint = this.hostCommand[this.hostCommand.length - 1] ?? "";
    return existsSync(entrypoint)
      ? { ok: true, message: `pi host is ready at ${entrypoint}` }
      : { ok: false, message: `pi host is missing at ${entrypoint}; this is a build/packaging bug` };
  }

  async spawn(params: DriverSpawnParams): Promise<DriverSpawnResult> {
    const health = this.health();
    if (!health.ok) throw new Error(health.message);
    const model = params.model?.trim() || defaultHostModel;
    return {
      argv: [...this.hostCommand],
      cwd: params.cwd,
      env: { ATTN_PI_HOST_MODEL: model },
    };
  }
}
