// A loopback model provider for pi, so a scenario can script what the agent
// says and what auto mode's classifier answers instead of asking a real model.
//
// pi resolves providers from `models.json` under its agent dir, which
// `PI_CODING_AGENT_DIR` relocates; a provider declared there with
// `api: "openai-completions"` reaches this server over the OpenAI streaming
// wire, which is what pi's client speaks. The two roles are told apart by the
// system prompt: auto mode's classifier prompt opens with a line no coding
// agent's does.
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';

/** The first line of automode/prompt.ts's classifierSystemPrompt. */
const CLASSIFIER_MARKER = 'You are a safety classifier for an autonomous coding agent';

export const stubProviderName = 'attn-harness';
export const stubAgentModel = `${stubProviderName}/stub-agent`;
export const stubJudgeModel = `${stubProviderName}/stub-judge`;

function sse(res, payload) {
  res.write(`data: ${JSON.stringify(payload)}\n\n`);
}

function chunk(delta, finishReason = null) {
  return {
    id: 'attn-harness-stub',
    object: 'chat.completion.chunk',
    created: 1,
    model: 'stub',
    choices: [{ index: 0, delta, finish_reason: finishReason }],
  };
}

// pi's reader throws "Stream ended without finish_reason", so the closing chunk
// is not optional.
function writeText(res, text) {
  sse(res, chunk({ role: 'assistant', content: text }));
  sse(res, { ...chunk({}, 'stop'), usage: { prompt_tokens: 8, completion_tokens: 4, total_tokens: 12 } });
}

function writeToolCall(res, { id, name, args }) {
  sse(res, chunk({
    tool_calls: [{ index: 0, id, type: 'function', function: { name, arguments: '' } }],
  }));
  sse(res, chunk({
    tool_calls: [{ index: 0, function: { arguments: JSON.stringify(args) } }],
  }));
  sse(res, { ...chunk({}, 'tool_calls'), usage: { prompt_tokens: 8, completion_tokens: 6, total_tokens: 14 } });
}

/**
 * Starts the server.
 *
 * `agent(request)` answers the session's own model and returns either
 * `{ text }` or `{ tool: { name, args } }`. `judge(request)` answers auto
 * mode's classifier and returns `{ verdict, reason, highStakes }`. Both are
 * given `{ body, messages, systemPrompt, turn }`, where `turn` counts the calls
 * that role has already answered.
 */
export function startPiStubProvider({ agent, judge }) {
  const calls = { agent: [], judge: [] };
  const server = http.createServer((req, res) => {
    // Somewhere for a scripted `curl` to reach that is not the internet.
    if (!req.url.endsWith('/chat/completions')) {
      res.writeHead(200, { 'Content-Type': 'text/plain' }).end('STUB-OK\n');
      return;
    }
    let raw = '';
    req.on('data', (piece) => { raw += piece; });
    req.on('end', () => {
      let body;
      try {
        body = JSON.parse(raw);
      } catch (error) {
        res.writeHead(400).end(String(error));
        return;
      }
      const messages = body.messages ?? [];
      const systemPrompt = messages
        .filter((m) => m.role === 'system' || m.role === 'developer')
        .map((m) => (typeof m.content === 'string' ? m.content : JSON.stringify(m.content)))
        .join('\n');
      const role = systemPrompt.includes(CLASSIFIER_MARKER) ? 'judge' : 'agent';
      const request = { body, messages, systemPrompt, turn: calls[role].length };
      calls[role].push(request);

      res.writeHead(200, {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
      });
      try {
        if (role === 'judge') {
          const verdict = judge(request);
          writeText(res, JSON.stringify({
            verdict: verdict.verdict,
            reason: verdict.reason,
            high_stakes: verdict.highStakes === true,
          }));
        } else {
          const answer = agent(request);
          if (answer.tool) {
            writeToolCall(res, {
              id: answer.tool.id ?? `call-${calls.agent.length}`,
              name: answer.tool.name,
              args: answer.tool.args,
            });
          } else {
            writeText(res, answer.text ?? '');
          }
        }
      } catch (error) {
        writeText(res, `stub provider failed: ${error?.message ?? error}`);
      }
      res.write('data: [DONE]\n\n');
      res.end();
    });
  });

  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      resolve({
        port,
        baseUrl: `http://127.0.0.1:${port}/v1`,
        calls,
        close: () => new Promise((done) => server.close(() => done())),
      });
    });
  });
}

/**
 * Writes a throwaway pi agent dir naming the stub provider, and returns the
 * value `PI_CODING_AGENT_DIR` wants. Nothing is copied out of the real
 * `~/.pi/agent`: a run against this dir reaches no model but the stub.
 */
export function writeStubAgentDir(dir, baseUrl) {
  fs.mkdirSync(dir, { recursive: true });
  const model = (id) => ({
    id,
    name: id,
    api: 'openai-completions',
    provider: stubProviderName,
    baseUrl,
    reasoning: false,
    input: ['text'],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 128000,
    maxTokens: 8192,
  });
  fs.writeFileSync(
    path.join(dir, 'models.json'),
    `${JSON.stringify({
      providers: {
        [stubProviderName]: {
          baseUrl,
          apiKey: 'attn-harness-stub',
          api: 'openai-completions',
          models: [model('stub-agent'), model('stub-judge')],
        },
      },
    }, null, 2)}\n`,
    'utf8',
  );
  return dir;
}
