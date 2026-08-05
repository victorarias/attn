// S2 delta rate: stream a ~400 word answer, measure message_update volume/rate.
import { buildSession, createLogger } from "./common.js";

const SCENARIO = "s2-delta-rate";
const logger = createLogger(SCENARIO);

async function main() {
	const { session } = await buildSession(SCENARIO);
	await session.bindExtensions({ mode: "print" });

	let streamStartT;
	let streamEndT;
	let updateCount = 0;
	let updateBytes = 0;
	let largest = { bytes: 0, note: "" };

	const unsubscribe = session.subscribe((event) => {
		const rec = logger.log("sdk", event.type, {});
		if (event.type === "message_start" && streamStartT === undefined) {
			streamStartT = rec.t;
		}
		if (event.type === "message_update") {
			updateCount++;
			updateBytes += rec.bytes;
			if (rec.bytes > largest.bytes) {
				largest = { bytes: rec.bytes, note: `t=${rec.t.toFixed(1)}` };
			}
		}
		if (event.type === "message_end" && event.message?.role === "assistant") {
			streamEndT = rec.t;
		}
	});

	await session.prompt("Write about 400 words describing what a terminal emulator does.");
	unsubscribe();
	session.dispose();

	const durationMs = streamEndT - streamStartT;

	// Peak events/sec and bytes/sec over 100ms buckets, computed from the log we just wrote.
	const fs = await import("node:fs");
	const lines = fs
		.readFileSync(logger.path, "utf8")
		.trim()
		.split("\n")
		.map((l) => JSON.parse(l))
		.filter((r) => r.type === "message_update");

	const buckets = new Map(); // bucketIndex(100ms) -> {count, bytes}
	for (const r of lines) {
		const idx = Math.floor(r.t / 100);
		const b = buckets.get(idx) ?? { count: 0, bytes: 0 };
		b.count++;
		b.bytes += r.bytes;
		buckets.set(idx, b);
	}
	let peakEventsPerSec = 0;
	let peakBytesPerSec = 0;
	for (const b of buckets.values()) {
		peakEventsPerSec = Math.max(peakEventsPerSec, b.count * 10); // 100ms bucket -> *10 for /sec
		peakBytesPerSec = Math.max(peakBytesPerSec, b.bytes * 10);
	}

	const summary = {
		updateCount,
		updateBytes,
		durationMs,
		peakEventsPerSec,
		peakBytesPerSec,
		largest,
	};
	logger.log("harness", "summary", { note: JSON.stringify(summary) });
	console.log("S2 summary:", summary);
}

main().catch((err) => {
	logger.log("harness", "error", { note: String(err?.stack ?? err) });
	console.error(err);
	process.exit(1);
});
