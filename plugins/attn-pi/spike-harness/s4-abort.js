// S4 abort: abort() mid-bash-call, inspect the exact event tail and whether the
// underlying sleep process actually died. Provider is google (gemini), not the
// anthropic/bedrock pair the "stopReason becomes aborted" fact was verified
// against - record whatever stopReason/error this provider actually produces.
import { execSync } from "node:child_process";
import { buildSession, createLogger } from "./common.js";

const SCENARIO = "s4-abort";
const logger = createLogger(SCENARIO);

function psSnapshot(label) {
	let out = "";
	try {
		out = execSync("ps -eo pid,ppid,command | grep 'sleep 30' | grep -v grep", { encoding: "utf8" }).trim();
	} catch {
		out = "";
	}
	logger.log("harness", "ps_snapshot", { note: `${label}: ${out || "(no sleep 30 process found)"}` });
	return out;
}

async function main() {
	const { session } = await buildSession(SCENARIO);
	await session.bindExtensions({ mode: "print" });

	let firstToolStartSeen = false;
	let abortCalledT;
	let abortReturnedT;
	let assistantStopReason;
	let assistantErrorMessage;

	const unsubscribe = session.subscribe((event) => {
		logger.log("sdk", event.type, {
			note:
				event.type === "tool_execution_start"
					? `toolName=${event.toolName}`
					: event.type === "tool_execution_end"
						? `isError=${event.isError}`
						: event.type === "message_end" && event.message?.role === "assistant"
							? `stopReason=${event.message.stopReason} err=${event.message.errorMessage ?? ""}`
							: undefined,
		});

		if (event.type === "message_end" && event.message?.role === "assistant") {
			assistantStopReason = event.message.stopReason;
			assistantErrorMessage = event.message.errorMessage;
		}

		if (event.type === "tool_execution_start" && !firstToolStartSeen) {
			firstToolStartSeen = true;
			logger.log("harness", "first_tool_execution_start", {});
			setTimeout(async () => {
				psSnapshot("before_abort");
				logger.log("harness", "abort_called", {});
				abortCalledT = performance.now();
				await session.abort();
				abortReturnedT = performance.now();
				logger.log("harness", "abort_returned", { note: `ms=${(abortReturnedT - abortCalledT).toFixed(1)}` });
				// Give the OS a beat, then check if the sleep survived the abort.
				setTimeout(() => psSnapshot("after_abort_1s"), 1000);
			}, 2000);
		}
	});

	await session.prompt("Run the bash command `sleep 30 && echo never`.");
	unsubscribe();

	// Wait a bit longer to catch any delayed/phantom events, then dispose.
	await new Promise((r) => setTimeout(r, 1500));
	session.dispose();

	const summary = {
		abortRoundTripMs: abortReturnedT && abortCalledT ? abortReturnedT - abortCalledT : null,
		assistantStopReason,
		assistantErrorMessage,
	};
	logger.log("harness", "summary", { note: JSON.stringify(summary) });
	console.log("S4 summary:", summary);
}

main().catch((err) => {
	logger.log("harness", "error", { note: String(err?.stack ?? err) });
	console.error(err);
	process.exit(1);
});
