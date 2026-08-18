// Auto mode as a pi session sees it: the `/auto` command, the `--auto` /
// `--no-auto` flags, the status indicator, and the classifier built from
// whatever model registry the running session hands over.
//
// One instance of this class lives at module scope in the entrypoints
// (suite/index.ts, standalone.ts) and `register` runs once per pi factory run,
// per plugins/attn-pi/AGENTS.md's lifecycle invariants. That is what makes the
// user's `/auto` choice outlive a /new or a resume in the same process, while
// the verdict cache and the breaker — owned by the factory below — do not.
import type { Classifier } from "./classifier";
import type { AutoModeConfig } from "./config";
import {
  createAutoMode,
  type AutoModeContextLike,
  type AutoModeDenial,
  type AutoModeExtensionAPILike,
} from "./index";
import { ModelClassifier, type ModelRegistryLike } from "./model-classifier";
import { autoModeStatusKey, autoModeStatusText } from "./ui";
import { UsageLedger } from "./usage";

export type AutoModeSessionContextLike = AutoModeContextLike & {
  /** pi's ModelRegistry, the only way to reach the classifier's model. */
  modelRegistry?: ModelRegistryLike;
};

export type SessionStartEventLike = { type: "session_start" };

export type AutoModePiLike = AutoModeExtensionAPILike & {
  on(event: "session_start", handler: (event: SessionStartEventLike, ctx: AutoModeSessionContextLike) => void): void;
  registerCommand(
    name: string,
    options: { description?: string; handler: (args: string, ctx: AutoModeContextLike) => Promise<void> | void },
  ): void;
  registerFlag(name: string, options: { description?: string; type: "boolean" | "string" }): void;
  getFlag(name: string): boolean | string | undefined;
};

export type AutoModeSetup = {
  config: AutoModeConfig;
  /**
   * Reported for every blocked call. The seam attn's own surfaces hang off;
   * bare pi leaves it unset.
   */
  onDenial?: (denial: AutoModeDenial) => void;
  /**
   * True while the breaker's question waits on the user, false once answered.
   * attn's suite declares `pending_approval` from it; bare pi leaves it unset.
   */
  onWaitingForUser?: (waiting: boolean) => void;
  /** Said once, at the first session start that has a UI. A broken config is the caller. */
  notice?: string;
};

export class AutoMode {
  private readonly usage = new UsageLedger();
  private readonly extension: (pi: AutoModeExtensionAPILike) => void;
  /** The session's own registry, captured from the first ctx that carries one. */
  private registry: ModelRegistryLike | undefined;
  private classifier: { registry: ModelRegistryLike; instance: Classifier } | undefined;
  /** What `/auto` last said. Undefined until the user says anything. */
  private choice: boolean | undefined;
  /** What the launch flags said. Undefined when neither was passed. */
  private flag: boolean | undefined;
  private noticed = false;

  constructor(private readonly setup: AutoModeSetup) {
    this.extension = createAutoMode({
      config: setup.config,
      classifier: { classify: (request) => this.judge().classify(request) },
      isEnabled: () => this.enabled(),
      onDenial: setup.onDenial,
      onWaitingForUser: setup.onWaitingForUser,
      usageLedger: this.usage,
    });
  }

  /**
   * The mode in force. A launch flag outranks the configured default, and the
   * user's own `/auto` outranks both from the moment they use it — a command
   * that silently loses to the flag it was typed to override is not a command.
   */
  enabled(): boolean {
    return this.choice ?? this.flag ?? this.setup.config.enabledDefault;
  }

  register(pi: AutoModePiLike): void {
    pi.registerFlag("auto", { description: "Start with attn auto mode on", type: "boolean" });
    pi.registerFlag("no-auto", { description: "Start with attn auto mode off", type: "boolean" });
    // No flag carries a default, so an unset one reads as undefined rather
    // than as a false somebody typed. --no-auto wins a session given both.
    this.flag = pi.getFlag("no-auto") === true ? false : pi.getFlag("auto") === true ? true : undefined;

    pi.registerCommand("auto", {
      description: "Toggle attn auto mode (on | off | status)",
      handler: (args, ctx) => this.command(args, ctx),
    });

    pi.on("session_start", (_event, ctx) => {
      if (ctx.modelRegistry) this.registry = ctx.modelRegistry;
      this.paint(ctx);
      if (this.setup.notice !== undefined && !this.noticed && ctx.ui) {
        this.noticed = true;
        ctx.ui.notify(this.setup.notice, "warning");
      }
    });

    this.extension(pi);
  }

  private command(args: string, ctx: AutoModeContextLike): void {
    const asked = args.trim().toLowerCase();
    if (asked === "on") this.choice = true;
    else if (asked === "off") this.choice = false;
    else if (asked === "" || asked === "toggle") this.choice = !this.enabled();
    else if (asked !== "status") {
      ctx.ui?.notify(`/auto takes on, off, status, or nothing at all — not ${JSON.stringify(asked)}.`, "error");
      return;
    }
    this.paint(ctx);
    ctx.ui?.notify(
      this.enabled()
        ? "auto mode is on: work inside this directory runs free, anything past it is judged."
        : "auto mode is off: pi runs every tool call, as it does without it.",
      "info",
    );
  }

  private paint(ctx: AutoModeContextLike): void {
    ctx.ui?.setStatus(autoModeStatusKey, autoModeStatusText(this.enabled()));
  }

  /** The classifier for the registry this session is running against. */
  private judge(): Classifier {
    const registry = this.registry;
    if (!registry) {
      throw new Error("auto mode has no model catalog to judge this call against");
    }
    if (this.classifier?.registry !== registry) {
      this.classifier = {
        registry,
        instance: new ModelClassifier({
          registry,
          config: this.setup.config,
          onUsage: (usage) => this.usage.add(usage),
        }),
      };
    }
    return this.classifier.instance;
  }
}
