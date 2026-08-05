// S5b: kill -9 the child ~immediately after prompt() is called, before any
// assistant output. Expected: zero session file on disk (session file is
// created only after the first assistant message, per S1/sdk.ts).
import { existsSync } from "node:fs";
import { spawn } from "node:child_process";
import { SPIKE_DIR, createLogger } from "./common.js";

const SCENARIO = "s5b-crash";
const logger = createLogger(SCENARIO);

async function main() {
	const child = spawn("bun", ["run", "s5-child.js", SCENARIO], { cwd: SPIKE_DIR, stdio: ["ignore", "pipe", "pipe"] });
	const childPid = child.pid; // captured at spawn
	logger.log("harness", "child_spawned", { note: `pid=${childPid}` });

	let sessionFilePath;
	const promptCalled = new Promise((resolve) => {
		let buf = "";
		child.stdout.on("data", (chunk) => {
			buf += chunk.toString();
			let idx;
			while ((idx = buf.indexOf("\n")) !== -1) {
				const line = buf.slice(0, idx);
				buf = buf.slice(idx + 1);
				if (line.startsWith("SESSION_FILE:")) {
					sessionFilePath = line.slice("SESSION_FILE:".length);
					logger.log("harness", "child_session_file", { note: sessionFilePath });
				} else if (line === "PROMPT_CALLED") {
					logger.log("harness", "child_prompt_called_observed", {});
					resolve();
				} else if (line.trim()) {
					logger.log("harness", "child_stdout", { note: line });
				}
			}
		});
		child.stderr.on("data", (chunk) => logger.log("harness", "child_stderr", { note: chunk.toString().trim() }));
	});

	await promptCalled;
	logger.log("harness", "kill_sent", { note: `pid=${childPid}` });
	process.kill(childPid, "SIGKILL");

	// Give the OS a moment, then check.
	await new Promise((r) => setTimeout(r, 500));

	const fileExists = sessionFilePath ? existsSync(sessionFilePath) : false;
	logger.log("harness", "session_file_exists_after_crash", {
		note: `exists=${fileExists} path=${sessionFilePath ?? "(none captured)"}`,
	});

	const summary = { sessionFilePathCaptured: Boolean(sessionFilePath), fileExists };
	logger.log("harness", "summary", { note: JSON.stringify(summary) });
	console.log("S5b summary:", summary);
}

main().catch((err) => {
	logger.log("harness", "error", { note: String(err?.stack ?? err) });
	console.error(err);
	process.exit(1);
});
