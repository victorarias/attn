import { readFile } from "node:fs/promises";

const instructionRefEnv = "ATTN_OPENCODE_INSTRUCTION_REF";

export async function server() {
  return {
    "experimental.chat.system.transform": async (_input: unknown, output: { system: string[] }) => {
      const instructionRef = process.env[instructionRefEnv]?.trim();
      if (!instructionRef) throw new Error(`${instructionRefEnv} is required`);
      const content = await readFile(instructionRef, "utf8");
      if (!content.trim()) throw new Error("attn launch instructions are empty");
      output.system.push(`attn launch instructions:\n${content}`);
    },
  };
}

export default server;
