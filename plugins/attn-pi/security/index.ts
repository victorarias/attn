import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  createBashToolDefinition, createEditToolDefinition, createFindToolDefinition, createGrepToolDefinition,
  createLsToolDefinition, createReadToolDefinition, createWriteToolDefinition, getAgentDir,
  type ExtensionAPI, type ExtensionContext, type ToolDefinition,
} from "@earendil-works/pi-coding-agent";
import { credentials } from "./filter";
import { SandboxedFilesystem } from "./filesystem";
import { loadSecurityConfig, resolveSecurityPolicy, saveSecurityConfig, type SecurityPolicy } from "./policy";
import { protectedBash, protectedTools } from "./tools";
import { reviewUnavailable, type SandboxReview } from "./recovery";
import { changeSecurityConfig } from "./settings";
import { SecurityPanel, type SecuritySnapshot } from "./ui";
import { securityInstructions, securityPrompt } from "./guidance";
import type { ToolExecutionCheck } from "../automode/index";
import { dimmed, problem } from "../automode/ui";

export class PiSecurity {
  private fs: SandboxedFilesystem | undefined;
  private temp: string | undefined;
  private policy: SecurityPolicy | undefined;
  private problem: string | undefined;
  constructor(private readonly configPath = join(getAgentDir(), "attn-security.json"), private readonly review?: SandboxReview, private readonly reviewAvailable = () => !!review, private readonly checkExecution?: ToolExecutionCheck) {}

  cacheWritePaths(): readonly string[] { return this.problem ? [] : this.policy?.cacheWritePaths ?? []; }

  register(pi: ExtensionAPI): void {
    this.close();
    pi.on("session_start", (_event, ctx) => this.configure(pi, ctx));
    pi.on("session_shutdown", () => this.close());
    pi.on("before_agent_start", (event) => ({ systemPrompt: event.systemPrompt + "\n\n" + credentials.text(
      this.problem ? securityPrompt("configuration-error", { problem: this.problem })
        : this.policy ? securityInstructions(this.policy, this.reviewAvailable()) : securityPrompt("not-initialized"),
    ) }));
    pi.on("tool_result", (event) => {
      try { return credentials.value({ content: event.content, details: event.details }); }
      catch { return { content: [{ type: "text", text: "Security withheld a result it could not filter" }], details: null, isError: true }; }
    });
    pi.on("context", (event) => ({ messages: credentials.value(event.messages) }));
    pi.on("before_provider_request", (event) => credentials.request(event.payload));
    pi.on("user_bash", () => {
      if (!this.policy || this.problem) return { result: { output: this.problem ?? "Security is not initialized", exitCode: 1, cancelled: false, truncated: false } };
      return { operations: protectedBash(this.policy, credentials, this.reviewAvailable) };
    });
    pi.registerCommand("security", {
      description: "Open security settings; status, on, off, caches, allow-write, revoke-write, network",
      handler: async (args, ctx) => {
        try {
          const command = args.trim();
          const snapshot = (): SecuritySnapshot => ({
            config: loadSecurityConfig(this.configPath), policy: this.policy, problem: this.problem,
            configPath: this.configPath, cwd: ctx.cwd, reviewAvailable: this.reviewAvailable(),
          });
          const apply = async (commands: string[]) => {
            const config = loadSecurityConfig(this.configPath);
            for (const command of commands) changeSecurityConfig(config, command, ctx.cwd);
            saveSecurityConfig(this.configPath, config);
            await this.configure(pi, ctx);
            return snapshot();
          };
          if (!command && ctx.mode === "tui") {
            const initial = snapshot();
            await ctx.ui.custom<void>((tui, theme, _keys, done) => new SecurityPanel(
              initial, theme, () => tui.terminal.rows, () => tui.requestRender(), apply, () => done(),
            ));
            return;
          }
          if (command && command !== "status") await apply([command]);
          ctx.ui.notify(this.status(), this.problem ? "error" : "info");
        } catch (error) {
          ctx.ui.notify(credentials.text(error instanceof Error ? error.message : String(error)), "error");
        }
      },
    });
  }

  private async configure(pi: ExtensionAPI, ctx: ExtensionContext): Promise<void> {
    await this.close();
    try {
      const config = loadSecurityConfig(this.configPath);
      this.temp = mkdtempSync(join(tmpdir(), "attn-pi-tools-"));
      this.policy = resolveSecurityPolicy(config, ctx.cwd, this.configPath, this.temp);
      this.fs = new SandboxedFilesystem(this.policy, credentials, this.reviewAvailable);
      this.problem = undefined;
      const policy = this.policy;
      const review: SandboxReview = async (event, context) => {
        if (!this.review) return { block: true, reason: reviewUnavailable };
        if (this.policy !== policy) throw new Error("Security settings or session changed; submit the request again.");
        const result = await this.review(event, context);
        if (this.policy !== policy) throw new Error("Security settings or session changed during review; command was not run. Submit the request again.");
        return result;
      };
      for (const tool of protectedTools(policy, credentials, this.fs, review, this.reviewAvailable, this.checkExecution)) pi.registerTool(tool);
    } catch (error) {
      this.problem = credentials.text(error instanceof Error ? error.message : String(error));
      for (const make of [createBashToolDefinition, createReadToolDefinition, createWriteToolDefinition, createEditToolDefinition, createLsToolDefinition, createFindToolDefinition, createGrepToolDefinition]) {
        const tool: ToolDefinition = make(ctx.cwd);
        pi.registerTool({ ...tool,
          renderCall: () => ({ render: () => [`${tool.name}: security configuration error`], invalidate() {} }),
          execute: async () => { throw new Error(`Security configuration error: ${this.problem}`); },
        });
      }
    }
    const theme = ctx.mode === "tui" ? ctx.ui.theme : undefined;
    ctx.ui.setStatus("attn-security", this.problem
      ? problem(theme, "security: blocked")
      : dimmed(theme, `sandbox: ${this.policy?.enabled ? "on" : "off"} · credential filtering: on`));
    if (this.problem) ctx.ui.notify(`Pi tools are blocked: ${this.problem}`, "error");
    else if (this.policy?.unavailableCaches.length) ctx.ui.notify(`Some build caches are unavailable. /security status shows the paths and reasons.`, "warning");
  }

  private status(): string {
    if (this.problem) return `Security blocked: ${this.problem}\nSettings: ${this.configPath}`;
    const policy = this.policy;
    if (!policy) return "Security is not initialized";
    return credentials.text([
      `Sandbox: ${policy.enabled ? "on" : "off"}; network: ${policy.enabled ? policy.network : "unrestricted (sandbox off)"}; credential filtering: on`,
      `Build caches: ${policy.buildCaches.enabled ? "on" : "off"}. Configured paths: ${policy.buildCaches.paths.join(", ")}`,
      `Active cache grants: ${policy.cacheWritePaths.join(", ") || "none"}`,
      ...policy.unavailableCaches.map((problem) => `Unavailable cache: ${problem}`),
      `Extra access review: ${policy.enabled && this.reviewAvailable() ? "available" : "unavailable"}`,
      `Write grants: ${policy.allowWrite.join(", ")}`, `Read denies: ${policy.denyRead.join(", ")}`,
      `Write denies: ${policy.denyWrite.join(", ")}`, `Settings: ${this.configPath}`,
      "Applies to built-in tools and !/!! commands. Extensions and MCP servers remain trusted.",
    ].join("\n"));
  }

  private async close(): Promise<void> {
    const fs = this.fs;
    const temp = this.temp;
    this.fs = undefined;
    this.temp = undefined;
    this.policy = undefined;
    await fs?.close();
    if (temp) rmSync(temp, { recursive: true, force: true });
  }
}
