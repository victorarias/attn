// S1 smoke: createAgentSession + bindExtensions, prompt "ok", check session file
// location/timing and sdk/ext event ordering around agent_end/agent_settled.
import { existsSync } from "node:fs";
import { SPIKE_DIR, buildSession, createLogger } from "./common.js";

const SCENARIO = "s1-smoke";
const logger = createLogger(SCENARIO);

function extensionFactory(pi) {
	for (const evt of ["session_start", "agent_start", "agent_end", "agent_settled"]) {
		pi.on(evt, (event) => {
			logger.log("ext", evt, { note: JSON.stringify(event).slice(0, 200) });
		});
	}
}

async function main() {
	const { session, sessionDir } = await buildSession(SCENARIO, { extensionFactory });

	if (!sessionDir.startsWith(SPIKE_DIR)) {
		throw new Error(`REFUSING: sessionDir ${sessionDir} is not under spike dir ${SPIKE_DIR}`);
	}
	logger.log("harness", "session_dir_verified", { note: sessionDir });

	await session.bindExtensions({ mode: "print" });

	const unsubscribe = session.subscribe((event) => {
		logger.log("sdk", event.type, {
			note:
				event.type === "agent_end"
					? `willRetry=${event.willRetry}`
					: event.type === "message_end" && event.message?.role
						? `role=${event.message.role}`
						: undefined,
		});
	});

	logger.log("harness", "session_file_before_prompt", {
		note: `exists=${existsSync(session.sessionFile ?? "")} path=${session.sessionFile}`,
	});

	await session.prompt("Reply with the single word: ok");

	logger.log("harness", "session_file_after_prompt", {
		note: `exists=${existsSync(session.sessionFile ?? "")} path=${session.sessionFile}`,
	});

	unsubscribe();
	session.dispose();
	console.log("S1 done. session file:", session.sessionFile);
}

main().catch((err) => {
	logger.log("harness", "error", { note: String(err?.stack ?? err) });
	console.error(err);
	process.exit(1);
});
