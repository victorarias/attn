// S6 memory slope: 15 sequential turns on one session, sampling
// process.memoryUsage() after each settle. This is a 15-turn slope, NOT a
// day-long soak receipt - it says nothing about long-run behavior.
import { buildSession, createLogger } from "./common.js";

const SCENARIO = "s6-memory-slope";
const logger = createLogger(SCENARIO);
const TURNS = 15;

async function main() {
	const { session } = await buildSession(SCENARIO);
	await session.bindExtensions({ mode: "print" });

	const samples = [];
	for (let i = 0; i < TURNS; i++) {
		await session.prompt("Reply with the single word: ok");
		if (global.gc) global.gc(); // only runs if bun started with --expose-gc; otherwise noise is part of the receipt
		const mem = process.memoryUsage();
		const rec = logger.log("harness", "memory_sample", {
			note: `turn=${i} rss=${mem.rss} heapUsed=${mem.heapUsed} heapTotal=${mem.heapTotal} external=${mem.external}`,
		});
		samples.push({ turn: i, rss: mem.rss, heapUsed: mem.heapUsed });
	}

	session.dispose();

	// Simple linear slope (least squares) over turn index for rss and heapUsed.
	function slope(points, key) {
		const n = points.length;
		const xs = points.map((p) => p.turn);
		const ys = points.map((p) => p[key]);
		const xMean = xs.reduce((a, b) => a + b, 0) / n;
		const yMean = ys.reduce((a, b) => a + b, 0) / n;
		let num = 0;
		let den = 0;
		for (let i = 0; i < n; i++) {
			num += (xs[i] - xMean) * (ys[i] - yMean);
			den += (xs[i] - xMean) ** 2;
		}
		return den === 0 ? 0 : num / den;
	}

	const rssSlopeBytesPerTurn = slope(samples, "rss");
	const heapSlopeBytesPerTurn = slope(samples, "heapUsed");
	const summary = {
		turns: TURNS,
		firstRss: samples[0].rss,
		lastRss: samples[TURNS - 1].rss,
		rssSlopeBytesPerTurn,
		firstHeapUsed: samples[0].heapUsed,
		lastHeapUsed: samples[TURNS - 1].heapUsed,
		heapSlopeBytesPerTurn,
		label: "15-turn slope only, not a soak test",
	};
	logger.log("harness", "summary", { note: JSON.stringify(summary) });
	console.log("S6 summary:", summary);
}

main().catch((err) => {
	logger.log("harness", "error", { note: String(err?.stack ?? err) });
	console.error(err);
	process.exit(1);
});
