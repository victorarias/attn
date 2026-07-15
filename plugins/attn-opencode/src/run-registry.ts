import { randomBytes } from "node:crypto";
import { chmod, mkdir, open, readFile, rename, rm, stat } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import type { LaunchConfig, PluginLaunchInstructions, RunRecord } from "./types";

const directoryMode = 0o700;
const fileMode = 0o600;

type RegistryContents = {
  schema: 3;
  runs: Record<string, RunRecord>;
};

type PersistedRegistryContents = Omit<RegistryContents, "schema"> & { schema: 1 | 2 | 3 };

export function runtimeRootFromSocket(socketPath: string): string {
  return join(dirname(socketPath), "plugins", "attn-opencode");
}

export class RunRegistry {
  private readonly registryPath: string;
  private readonly secretsDir: string;
  private readonly promptsDir: string;
  private readonly instructionsDir: string;
  private readonly launchDir: string;
  private runs = new Map<string, RunRecord>();
  private work = Promise.resolve();

  constructor(readonly root: string) {
    this.registryPath = join(root, "runs.json");
    this.secretsDir = join(root, "secrets");
    this.promptsDir = join(root, "prompts");
    this.instructionsDir = join(root, "instructions");
    this.launchDir = join(root, "launch");
  }

  async initialize(): Promise<void> {
    await this.withLock(async () => {
      for (const path of [this.root, this.secretsDir, this.promptsDir, this.instructionsDir, this.launchDir]) {
        await ensurePrivateDirectory(path);
      }
      try {
        const parsed = JSON.parse(await readFile(this.registryPath, "utf8")) as PersistedRegistryContents;
        if ((parsed.schema !== 1 && parsed.schema !== 2 && parsed.schema !== 3) || !parsed.runs || typeof parsed.runs !== "object") {
          throw new Error("invalid run registry schema");
        }
        this.runs = new Map(Object.entries(parsed.runs));
        if (parsed.schema !== 3) await this.persist();
      } catch (error) {
        if (!isNotFound(error)) throw error;
        await this.persist();
      }
    });
  }

  async create(
    record: Omit<RunRecord, "password_ref" | "prompt_ref" | "instruction_ref" | "instruction_kind" | "launch_config_ref">,
    prompt: string,
    instructions?: PluginLaunchInstructions,
  ): Promise<RunRecord> {
    return this.withLock(async () => {
      if (this.runs.has(record.run_id)) throw new Error(`run ${record.run_id} already exists`);
      const passwordRef = join(this.secretsDir, `${safeRunID(record.run_id)}.secret`);
      const promptRef = prompt === "" ? undefined : join(this.promptsDir, `${safeRunID(record.run_id)}.prompt`);
      const instructionRef = instructions ? join(this.instructionsDir, `${safeRunID(record.run_id)}.md`) : undefined;
      const instructionContents = instructions ? requireInstructionContent(instructions) : undefined;
      const launchConfigRef = join(this.launchDir, `${safeRunID(record.run_id)}.json`);
      await writePrivate(passwordRef, randomBytes(32).toString("base64url"));
      if (promptRef) await writePrivate(promptRef, prompt);
      if (instructionRef && instructionContents) await writePrivate(instructionRef, instructionContents);
      const full: RunRecord = {
        ...record,
        password_ref: passwordRef,
        prompt_ref: promptRef,
        instruction_ref: instructionRef,
        instruction_kind: instructions?.kind,
        launch_config_ref: launchConfigRef,
      };
      this.runs.set(full.run_id, full);
      await this.persist();
      return structuredClone(full);
    });
  }

  async get(runID: string): Promise<RunRecord | undefined> {
    return this.withLock(() => {
      const value = this.runs.get(runID);
      return value ? structuredClone(value) : undefined;
    });
  }

  async update(runID: string, changes: Partial<RunRecord>): Promise<RunRecord> {
    return this.withLock(async () => {
      const current = this.runs.get(runID);
      if (!current) throw new Error(`run ${runID} is not registered`);
      const next = { ...current, ...changes };
      this.runs.set(runID, next);
      await this.persist();
      return structuredClone(next);
    });
  }

  async reserveSequence(runID: string): Promise<number> {
    return this.withLock(async () => {
      const current = this.runs.get(runID);
      if (!current) throw new Error(`run ${runID} is not registered`);
      const sequence = current.next_seq;
      current.next_seq += 1;
      await this.persist();
      return sequence;
    });
  }

  async password(record: RunRecord): Promise<string> {
    return readPrivate(record.password_ref);
  }

  async prompt(record: RunRecord): Promise<string> {
    return record.prompt_ref ? readPrivate(record.prompt_ref) : "";
  }

  async writeLaunchConfig(record: RunRecord, config: LaunchConfig): Promise<void> {
    await this.withLock(async () => {
      if (this.runs.get(record.run_id)?.launch_config_ref !== record.launch_config_ref) {
        throw new Error(`run ${record.run_id} launch config does not belong to this registry`);
      }
      await writePrivate(record.launch_config_ref, `${JSON.stringify(config)}\n`);
    });
  }

  async readLaunchConfig(record: RunRecord): Promise<LaunchConfig> {
    const parsed = JSON.parse(await readPrivate(record.launch_config_ref)) as LaunchConfig;
    if (parsed.schema !== 1 || parsed.run_id !== record.run_id || !Number.isSafeInteger(parsed.port)) {
      throw new Error(`invalid launch config for run ${record.run_id}`);
    }
    return parsed;
  }

  async cleanup(runID: string): Promise<void> {
    await this.withLock(async () => {
      const record = this.runs.get(runID);
      if (!record) return;
      this.runs.delete(runID);
      await this.persist();
      await Promise.all([
        rm(record.password_ref, { force: true }),
        record.prompt_ref ? rm(record.prompt_ref, { force: true }) : Promise.resolve(),
        record.instruction_ref ? rm(record.instruction_ref, { force: true }) : Promise.resolve(),
        rm(record.launch_config_ref, { force: true }),
      ]);
    });
  }

  async pruneDead(healthProbe: (record: RunRecord) => Promise<boolean>): Promise<void> {
    const candidates = await this.withLock(() => [...this.runs.values()].map((run) => structuredClone(run)));
    for (const record of candidates) {
      if (!(await healthProbe(record))) await this.cleanup(record.run_id);
    }
  }

  private async persist(): Promise<void> {
    const runs = Object.fromEntries(this.runs.entries());
    await writePrivate(this.registryPath, `${JSON.stringify({ schema: 3, runs } satisfies RegistryContents)}\n`);
  }

  private async withLock<T>(operation: () => Promise<T> | T): Promise<T> {
    const next = this.work.then(operation, operation);
    this.work = next.then(
      () => undefined,
      () => undefined,
    );
    return next;
  }
}

function requireInstructionContent(instructions: PluginLaunchInstructions | undefined): string {
  if (!instructions || (instructions.kind !== "workspace" && instructions.kind !== "chief")) {
    throw new Error("launch instructions require a supported kind");
  }
  if (typeof instructions.content !== "string" || instructions.content.trim() === "") {
    throw new Error("launch instructions require non-empty content");
  }
  return instructions.content;
}

export async function writePrivate(path: string, contents: string): Promise<void> {
  await ensurePrivateDirectory(dirname(path));
  const temp = join(dirname(path), `.${basename(path)}.${randomBytes(8).toString("hex")}.tmp`);
  const file = await open(temp, "w", fileMode);
  try {
    await file.writeFile(contents, "utf8");
    await file.sync();
  } finally {
    await file.close();
  }
  await chmod(temp, fileMode);
  await rename(temp, path);
  await chmod(path, fileMode);
}

export async function readPrivate(path: string): Promise<string> {
  const info = await stat(path);
  if ((info.mode & 0o077) !== 0) throw new Error(`private runtime file has unsafe permissions: ${path}`);
  return readFile(path, "utf8");
}

async function ensurePrivateDirectory(path: string): Promise<void> {
  await mkdir(path, { recursive: true, mode: directoryMode });
  await chmod(path, directoryMode);
}

function isNotFound(error: unknown): boolean {
  return typeof error === "object" && error !== null && "code" in error && error.code === "ENOENT";
}

function safeRunID(runID: string): string {
  if (!/^[A-Za-z0-9_-]+$/.test(runID)) throw new Error("run_id contains unsafe path characters");
  return runID;
}
