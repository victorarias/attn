import { waitForPaneInputFocus, waitForPaneText, waitForPaneVisible } from './scenarioAssertions.mjs';

function compact(text) {
  return text.replace(/\s+/g, '');
}

function hasTrustPrompt(text) {
  return text.includes('Do you trust this folder?') || text.includes('Security guide');
}

function hasClaudePrompt(text) {
  if (hasTrustPrompt(text)) {
    return false;
  }
  return /(^|\n)\s*❯(?:\s|$)/u.test(text);
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function ensureClaudeMainPromptReady(client, sessionId, timeoutMs = 40_000) {
  const startedAt = Date.now();
  let trustHandled = false;

  while (Date.now() - startedAt < timeoutMs) {
    await client.request('select_session', { sessionId });
    await waitForPaneVisible(client, sessionId, 'main', 20_000);
    const pane = await client.request('read_pane_text', { sessionId, paneId: 'main' }, { timeoutMs: 20_000 });
    const text = pane?.text || '';

    if (hasTrustPrompt(text)) {
      await client.request('click_pane', { sessionId, paneId: 'main' });
      await waitForPaneInputFocus(client, sessionId, 'main', 15_000);
      await client.request('type_pane_via_ui', { sessionId, paneId: 'main', text: '1' });
      await client.request('write_pane', { sessionId, paneId: 'main', text: '\r', submit: false });
      trustHandled = true;
      await delay(500);
      continue;
    }

    if (hasClaudePrompt(text)) {
      await client.request('click_pane', { sessionId, paneId: 'main' });
      await waitForPaneInputFocus(client, sessionId, 'main', 15_000);
      return { trustHandled, text };
    }
  }

  throw new Error(`Timed out waiting for Claude prompt readiness in session ${sessionId}`);
}

export async function promptClaudeForStructuredBlock(client, sessionId, token, lineCount = 8) {
  const lines = Array.from({ length: lineCount }, (_, index) =>
    `${token} line ${index + 1} render width coverage verification payload ${index + 1} for split stability`
  );
  const prompt = [
    'Reply with exactly the following lines and nothing else.',
    'Preserve uppercase letters, digits, and spacing exactly.',
    'Do not add a preamble.',
    'Do not use a code block.',
    ...lines,
  ].join('\n');

  await client.request('click_pane', { sessionId, paneId: 'main' });
  await waitForPaneInputFocus(client, sessionId, 'main', 15_000);
  await client.request('type_pane_via_ui', { sessionId, paneId: 'main', text: prompt });
  await client.request('write_pane', { sessionId, paneId: 'main', text: '\r', submit: false });

  return { prompt, expectedLines: lines };
}
