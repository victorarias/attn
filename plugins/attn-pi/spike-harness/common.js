// Shared helpers for the pi SDK spike scenarios.
import { appendFileSync, existsSync, mkdirSync, readdirSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import {
	createAgentSession,
	DefaultResourceLoader,
	SessionManager,
	SettingsManager,
} from "@earendil-works/pi-coding-agent";
// NOTE (surprise vs. sdk.ts docstring): getModel is NOT exported from the
// package root in 0.80.10 - only from the /compat subpath (deprecated there,
// but functional; the non-deprecated form is modelRuntime.getModel()).
import { getModel } from "@earendil-works/pi-ai/compat";

export const SPIKE_DIR = resolve(import.meta.dirname);
export const LOGS_DIR = join(SPIKE_DIR, "logs");
export const SESSIONS_DIR = join(SPIKE_DIR, "sessions");
// Real ~/.pi/agent, used ONLY for auth/resource resolution the same way plain `pi` does.
// Never written to, never redirected: session storage is redirected separately (see buildSession).
export const REAL_AGENT_DIR = join(homedir(), ".pi", "agent");
export const REAL_SESSIONS_DIR = join(REAL_AGENT_DIR, "sessions");

if (!existsSync(LOGS_DIR)) mkdirSync(LOGS_DIR, { recursive: true });
if (!existsSync(SESSIONS_DIR)) mkdirSync(SESSIONS_DIR, { recursive: true });

// Cheap model for the whole spike. Anthropic was the original design, but this
// machine's ~/.pi/agent/auth.json has no anthropic credential configured (`pi
// --list-models anthropic` -> "No models matching anthropic"; no ANTHROPIC_*
// env vars set). Was briefly on google/gemini-flash-lite-latest, then Victor
// corrected: use OpenAI. Cheapest openai model per `pi --list-models openai`
// + packages/ai/src/providers/openai.models.ts cost table is gpt-5.6-luna
// ($1/$6 per M input/output - cheaper than sibling gpt-5.6-sol $5/$30 and
// gpt-5.6-terra $2.5/$15).
export const MODEL_PROVIDER = "openai";
export const MODEL_ID = "gpt-5.6-luna";
export const CHEAP_MODEL = () => getModel(MODEL_PROVIDER, MODEL_ID);

/** Create a JSONL logger for one scenario. Every record follows the pinned shape. */
export function createLogger(scenario) {
	const path = join(LOGS_DIR, `${scenario}.jsonl`);
	return {
		path,
		log(surface, type, extra = {}) {
			const rec = { t: performance.now(), scenario, surface, type, bytes: 0, ...extra };
			rec.bytes = Buffer.byteLength(JSON.stringify(rec));
			appendFileSync(path, `${JSON.stringify(rec)}\n`);
			return rec;
		},
	};
}

/**
 * Build an AgentSession for a scenario, with its session file redirected under
 * the spike dir (never ~/.pi/agent/sessions). Auth resolution is left to pi's
 * own defaults against the real ~/.pi/agent/auth.json, matching normal `pi` usage.
 * We never pass a custom `agentDir` to createAgentSession, so ModelRuntime's
 * authPath/modelsPath stay at their defaults (real ~/.pi/agent).
 *
 * extensionFactory, if given, is registered as an inline extension so its
 * pi.on(...) handlers fire (session_start only fires via bindExtensions()).
 */
export async function buildSession(scenario, { extensionFactory } = {}) {
	const cwd = SPIKE_DIR;
	const sessionDir = join(SESSIONS_DIR, scenario);
	if (!existsSync(sessionDir)) mkdirSync(sessionDir, { recursive: true });

	const sessionManager = SessionManager.create(cwd, sessionDir);
	const settingsManager = SettingsManager.create(cwd); // agentDir defaults to real ~/.pi/agent

	const resourceLoader = new DefaultResourceLoader({
		cwd,
		agentDir: REAL_AGENT_DIR, // resource/extension discovery only, read-only, matches normal pi
		settingsManager,
		extensionFactories: extensionFactory ? [extensionFactory] : undefined,
	});
	await resourceLoader.reload();

	const { session, extensionsResult, modelFallbackMessage } = await createAgentSession({
		cwd,
		model: CHEAP_MODEL(),
		sessionManager,
		settingsManager,
		resourceLoader,
	});

	return { session, sessionManager, extensionsResult, modelFallbackMessage, sessionDir };
}

/**
 * Reopen an existing session file (e.g. after a simulated crash) with a fresh
 * AgentSession. Still under the spike dir; never touches ~/.pi/agent/sessions.
 */
export async function openSession(sessionFilePath) {
	const cwd = SPIKE_DIR;
	const sessionManager = SessionManager.open(sessionFilePath);
	const settingsManager = SettingsManager.create(cwd);

	const resourceLoader = new DefaultResourceLoader({ cwd, agentDir: REAL_AGENT_DIR, settingsManager });
	await resourceLoader.reload();

	const { session, modelFallbackMessage } = await createAgentSession({
		cwd,
		model: CHEAP_MODEL(),
		sessionManager,
		settingsManager,
		resourceLoader,
	});

	return { session, sessionManager, modelFallbackMessage };
}

export function sleep(ms) {
	return new Promise((r) => setTimeout(r, ms));
}

export function countFiles(dir) {
	try {
		return readdirSync(dir).length;
	} catch {
		return 0;
	}
}
