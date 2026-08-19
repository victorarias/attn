// pi's `/reload` re-evaluates this plugin's module scope. The relay socket
// lives there, so a re-evaluation that rebuilt it would dial a second time and
// leave the first connection open with nobody reading it.
import { describe, expect, test } from "bun:test";
import { mkdtempSync } from "node:fs";
import { createServer, type Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";

const probe = join(import.meta.dir, "suite-reload-probe.ts");
const suiteEntry = join(import.meta.dir, "..", "suite", "index.ts");
// Keep filenames short: macOS unix socket paths cap sun_path at 104 bytes.
const socketPath = join(mkdtempSync(join(tmpdir(), "attn-pi-")), "r.sock");

describe("suite reload", () => {
  test("a second evaluation of the entrypoint dials no second relay connection", async () => {
    const connections: Socket[] = [];
    const server = createServer((socket) => connections.push(socket));
    await new Promise<void>((resolve) => server.listen(socketPath, resolve));

    try {
      const run = Bun.spawn([process.execPath, "run", probe, suiteEntry], {
        env: {
          PATH: process.env.PATH ?? "",
          HOME: process.env.HOME ?? "",
          ATTN_PI_SUITE_SOCKET: socketPath,
          ATTN_PI_TOKEN: "token-1",
        },
        stdout: "pipe",
        stderr: "pipe",
      });
      const [stderr, code] = await Promise.all([new Response(run.stderr).text(), run.exited]);
      if (code !== 0) throw new Error(`suite reload probe failed (${code}): ${stderr}`);

      expect(connections.length).toBe(1);
    } finally {
      for (const socket of connections) socket.destroy();
      server.close();
    }
  });
});
