// S5 child: runs a single prompt with a 15s bash sleep. The outer runner
// spawns this as its own bun process, captures its pid, and kill -9's it
// mid-flight. This process must NOT catch SIGKILL (it can't) - it just runs
// normally so the outer can kill it uncooperatively.
import { buildSession, createLogger } from "./common.js";

const SCENARIO = process.argv[2] ?? "s5-crash";
const logger = createLogger(`${SCENARIO}-child`);

async function main() {
	const { session } = await buildSession(SCENARIO);
	await session.bindExtensions({ mode: "print" });

	// Tell the outer runner where the session file lives and that we're up.
	console.log(`SESSION_FILE:${session.sessionFile}`);
	console.log(`CHILD_PID:${process.pid}`);

	session.subscribe((event) => {
		logger.log("sdk", event.type, {
			note: event.type === "tool_execution_start" ? `toolName=${event.toolName}` : undefined,
		});
		if (event.type === "tool_execution_start") {
			console.log("TOOL_EXECUTION_START");
		}
	});

	console.log("PROMPT_CALLED");
	await session.prompt("Run the bash command `sleep 15 && echo done`, then summarize.");
	console.log("CHILD_PROMPT_SETTLED");
}

main().catch((err) => {
	logger.log("harness", "error", { note: String(err?.stack ?? err) });
	console.error(err);
	process.exit(1);
});
