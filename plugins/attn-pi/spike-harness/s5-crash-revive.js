// S5 crash/revive: spawn a child bun process running s5-child.js, kill -9 it
// mid-tool-call, then inspect what survived and whether reopening the session
// and continuing works. Only the PID captured at spawn is ever killed.
import { execSync, spawn } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { SPIKE_DIR, createLogger, openSession, sleep } from "./common.js";

const SCENARIO = "s5-crash";
const logger = createLogger(SCENARIO);

function psSnapshot(label, pattern) {
	let out = "";
	try {
		out = execSync(`ps -eo pid,ppid,command | grep '${pattern}' | grep -v grep`, { encoding: "utf8" }).trim();
	} catch {
		out = "";
	}
	logger.log("harness", "ps_snapshot", { note: `${label}: ${out || "(none found)"}` });
	return out;
}

async function main() {
	const child = spawn("bun", ["run", "s5-child.js", SCENARIO], { cwd: SPIKE_DIR, stdio: ["ignore", "pipe", "pipe"] });
	const childPid = child.pid; // captured at spawn - the only PID this script will ever kill
	logger.log("harness", "child_spawned", { note: `pid=${childPid}` });

	let sessionFilePath;
	let killedAt;
	const toolStartSeen = new Promise((resolve) => {
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
				} else if (line === "TOOL_EXECUTION_START") {
					logger.log("harness", "child_tool_execution_start_observed", {});
					resolve();
				} else if (line.trim()) {
					logger.log("harness", "child_stdout", { note: line });
				}
			}
		});
		child.stderr.on("data", (chunk) => logger.log("harness", "child_stderr", { note: chunk.toString().trim() }));
	});

	await toolStartSeen;
	await sleep(3000);
	psSnapshot("before_kill", "sleep 15");
	killedAt = performance.now();
	logger.log("harness", "kill_sent", { note: `pid=${childPid}` });
	process.kill(childPid, "SIGKILL");
	await sleep(500);

	psSnapshot("after_kill_immediate", "sleep 15");
	await sleep(1500);
	psSnapshot("after_kill_2s", "sleep 15");

	if (!sessionFilePath) {
		logger.log("harness", "error", { note: "child never reported a session file path" });
		console.error("No session file path captured; aborting S5.");
		process.exit(1);
	}

	const fileExists = existsSync(sessionFilePath);
	logger.log("harness", "session_file_exists_after_crash", { note: `exists=${fileExists} path=${sessionFilePath}` });

	if (!fileExists) {
		console.log("S5 summary: session file does not exist after crash - cannot reopen.");
		return;
	}

	const raw = readFileSync(sessionFilePath, "utf8");
	const entries = raw
		.trim()
		.split("\n")
		.map((l) => JSON.parse(l));
	const messageEntries = entries.filter((e) => e.type === "message");
	let danglingToolCall = false;
	for (const e of messageEntries) {
		if (e.message.role === "assistant") {
			const content = e.message.content ?? [];
			const toolCalls = content.filter((c) => c.type === "toolCall");
			for (const tc of toolCalls) {
				const hasResult = messageEntries.some(
					(other) => other.message.role === "toolResult" && other.message.toolCallId === tc.id,
				);
				if (!hasResult) danglingToolCall = true;
			}
		}
	}
	logger.log("harness", "loaded_entries", {
		note: `entryCount=${entries.length} messageCount=${messageEntries.length} danglingToolCall=${danglingToolCall}`,
	});

	// Reopen and continue.
	const { session: reopened } = await openSession(sessionFilePath);
	await reopened.bindExtensions({ mode: "print" });
	logger.log("harness", "reopened", { note: `entries=${reopened.messages.length}` });

	let completedNormally = false;
	let replyText = "";
	const unsubscribe = reopened.subscribe((event) => {
		logger.log("sdk", event.type, {
			note: event.type === "message_end" && event.message?.role === "assistant" ? `stopReason=${event.message.stopReason}` : undefined,
		});
		if (event.type === "message_end" && event.message?.role === "assistant") {
			const text = event.message.content?.find?.((c) => c.type === "text")?.text;
			if (text) replyText = text;
			completedNormally = event.message.stopReason === "stop" || event.message.stopReason === "toolUse";
		}
	});

	await reopened.prompt("What happened to the command?");
	unsubscribe();
	reopened.dispose();

	const summary = {
		sessionFileExists: fileExists,
		entryCount: entries.length,
		danglingToolCall,
		completedNormally,
		replyText,
	};
	logger.log("harness", "summary", { note: JSON.stringify(summary) });
	console.log("S5 summary:", summary);
}

main().catch((err) => {
	logger.log("harness", "error", { note: String(err?.stack ?? err) });
	console.error(err);
	process.exit(1);
});
