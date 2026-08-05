// S3 steer: mid-run steer() while two sequential bash tool calls are running.
import { buildSession, createLogger, sleep } from "./common.js";

const SCENARIO = "s3-steer";
const logger = createLogger(SCENARIO);
const STEER_TEXT = "Also include the word BANANA in your final answer.";

async function main() {
	const { session } = await buildSession(SCENARIO);
	await session.bindExtensions({ mode: "print" });

	let turnCount = 0;
	let firstToolStartSeen = false;
	let steerCalledAtTurn = null;
	let steerMaterializedAtTurn = null;
	let finalAssistantText = "";

	const unsubscribe = session.subscribe((event) => {
		const rec = logger.log("sdk", event.type, {
			note:
				event.type === "queue_update"
					? `steering=${JSON.stringify(event.steering)} followUp=${JSON.stringify(event.followUp)}`
					: event.type === "tool_execution_start"
						? `toolName=${event.toolName}`
						: undefined,
		});

		if (event.type === "turn_start") {
			turnCount++;
		}

		if (event.type === "tool_execution_start" && !firstToolStartSeen) {
			firstToolStartSeen = true;
			logger.log("harness", "first_tool_execution_start", { note: `turn=${turnCount}` });
			setTimeout(async () => {
				logger.log("harness", "steer_called", { note: `turn=${turnCount}` });
				steerCalledAtTurn = turnCount;
				await session.steer(STEER_TEXT);
			}, 1000);
		}

		if (event.type === "message_start" && event.message?.role === "user") {
			const text = event.message.content?.find?.((c) => c.type === "text")?.text;
			if (text === STEER_TEXT) {
				steerMaterializedAtTurn = turnCount;
				logger.log("harness", "steer_materialized", { note: `turn=${turnCount}` });
			}
		}

		if (event.type === "message_end" && event.message?.role === "assistant") {
			const text = event.message.content?.find?.((c) => c.type === "text")?.text;
			if (text) finalAssistantText = text;
		}
	});

	await session.prompt(
		"Run the bash command `sleep 3 && echo one`. After it completes, run `sleep 3 && echo two`. Then tell me what both printed.",
	);
	unsubscribe();
	session.dispose();

	const summary = {
		steerCalledAtTurn,
		steerMaterializedAtTurn,
		containsBanana: finalAssistantText.includes("BANANA"),
		finalAssistantText,
	};
	logger.log("harness", "summary", { note: JSON.stringify(summary) });
	console.log("S3 summary:", summary);
}

main().catch((err) => {
	logger.log("harness", "error", { note: String(err?.stack ?? err) });
	console.error(err);
	process.exit(1);
});
