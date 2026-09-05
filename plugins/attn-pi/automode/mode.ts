// One instance lives at module scope and `register` runs once per pi factory run, so the
// user's `/auto` outlives a /new or a resume while the factory-owned caches do not.
import type { Classifier } from "./classifier";
import type { AutoModeConfig } from "./config";
import {
  createAutoMode,
  type AutoModeContextLike,
  type AutoModeDenial,
  type AutoModeExtensionAPILike,
  type ToolCallReview,
  type ToolExecutionCheck,
} from "./index";
import type { DenialLedgerLike } from "./ledger";
import { ModelClassifier, type ModelRegistryLike } from "./model-classifier";
import {
  autoModeStatusKey,
  denialReportLines,
  dimmed,
  heldCount,
  heldStatusText,
} from "./ui";
import { UsageLedger } from "./usage";
import { reviewUnavailable } from "../security/recovery";
import { toolEvidenceLimits } from "./evidence";

export type AutoModeSessionContextLike = AutoModeContextLike & {
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

  ledger?: DenialLedgerLike;

  onDenial?: (denial: AutoModeDenial) => void;

  onWaitingForUser?: (waiting: boolean) => void;

  sessionKey?: string;

  notice?: string;
  sandboxReviewInExecutor?: boolean;
  cacheWritePaths?: () => readonly string[];
};

export class AutoMode {
  private readonly usage = new UsageLedger();
  private readonly extension: (pi: AutoModeExtensionAPILike) => void;
  private registry: ModelRegistryLike | undefined;
  private classifier: { registry: ModelRegistryLike; instance: Classifier } | undefined;
  private choice: boolean | undefined;
  private flag: boolean | undefined;
  private noticed = false;
  private review: ToolCallReview | undefined;
  private executionCheck: ToolExecutionCheck | undefined;
  private standing: () => readonly AutoModeDenial[] = () => [];

  constructor(private readonly setup: AutoModeSetup) {
    this.extension = createAutoMode({
      config: setup.config,
      classifier: {
        classify: (request) => this.judge().classify(request),
        evidenceLimits: () => this.registry ? this.judge().evidenceLimits?.() ?? toolEvidenceLimits() : toolEvidenceLimits(),
      },
      isEnabled: () => this.enabled(),
      ledger: setup.ledger,
      onDenial: setup.onDenial,
      onWaitingForUser: setup.onWaitingForUser,
      usageLedger: this.usage,
      onReady: (review, checkExecution, standing) => {
        this.review = review;
        this.executionCheck = checkExecution;
        this.standing = standing;
      },
      sandboxReviewInExecutor: setup.sandboxReviewInExecutor,
      cacheWritePaths: setup.cacheWritePaths,
    });
  }

  enabled(): boolean {
    if (this.setup.config.models.length === 0) return false;
    return this.choice ?? this.flag ?? this.setup.config.enabledDefault;
  }

  readonly canReviewSandbox = (): boolean => this.enabled() && !!this.review && !!this.registry;

  readonly checkExecution: ToolExecutionCheck = (event, ctx) => {
    if (!this.executionCheck) throw new Error(reviewUnavailable);
    this.executionCheck(event, ctx);
  };

  readonly reviewSandbox: ToolCallReview = async (event, ctx) => {
    if (!this.enabled() || !this.review) return { block: true, reason: reviewUnavailable };
    const result = await this.review(event, ctx);
    if (!this.enabled()) return { block: true, reason: reviewUnavailable };
    return result;
  };

  register(pi: AutoModePiLike): void {
    pi.registerFlag("auto", { description: "Start with attn auto mode on", type: "boolean" });
    pi.registerFlag("no-auto", { description: "Start with attn auto mode off", type: "boolean" });
    // No flag carries a default, so an unset one reads as undefined rather than as a false
    // somebody typed. --no-auto wins a session given both.
    this.flag = pi.getFlag("no-auto") === true ? false : pi.getFlag("auto") === true ? true : undefined;

    pi.registerCommand("auto", {
      description: "Toggle attn auto mode (on | off | status | blocked)",
      handler: (args, ctx) => this.command(args, ctx),
    });

    pi.on("session_start", (_event, ctx) => {
      // Pi parses extension flags after loading the factory and before session_start.
      this.flag = pi.getFlag("no-auto") === true ? false : pi.getFlag("auto") === true ? true : undefined;
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
    else if (asked === "blocked") { this.reportBlocked(ctx); return; }
    else if (asked !== "status") {
      ctx.ui?.notify(`/auto takes on, off, status, blocked, or nothing at all, not ${JSON.stringify(asked)}.`, "error");
      return;
    }
    this.paint(ctx);
    const held = this.held();
    ctx.ui?.notify(
      this.setup.config.models.length === 0
        ? "auto mode is off: no model is set to judge a call. Add one in attn's settings."
        : this.enabled()
          ? held > 0
            ? `auto mode is on: work inside this directory runs free, anything past it is judged. ${held} call${held === 1 ? " is" : "s are"} held — /auto blocked reviews them.`
            : "auto mode is on: work inside this directory runs free, anything past it is judged."
          : "auto mode is off: pi runs every tool call, as it does without it.",
      "info",
    );
  }

  private held(): number {
    return this.enabled() ? heldCount(this.standing()) : 0;
  }

  private reportBlocked(ctx: AutoModeContextLike): void {
    const denials = this.standing();
    if (!this.enabled() || denials.length === 0) {
      ctx.ui?.notify("auto mode is not holding any calls.", "info");
      return;
    }
    // Only the TUI draws color; RPC relays notify text verbatim, so it stays plain.
    const theme = ctx.mode === "tui" ? ctx.ui?.theme : undefined;
    ctx.ui?.notify(denialReportLines(denials, theme).join("\n"), "warning");
  }

  private paint(ctx: AutoModeContextLike): void {
    // Only the TUI draws the footer; RPC relays status text verbatim, so it stays plain.
    const theme = ctx.mode === "tui" ? ctx.ui?.theme : undefined;
    ctx.ui?.setStatus(autoModeStatusKey, dimmed(theme, heldStatusText(this.enabled(), this.held())));
  }

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
          ...(this.setup.sessionKey ? { sessionKey: this.setup.sessionKey } : {}),
          onUsage: (usage) => this.usage.add(usage),
        }),
      };
    }
    return this.classifier.instance;
  }
}
