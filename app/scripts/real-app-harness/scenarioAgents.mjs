import { waitForPaneInputFocus, waitForPaneText, waitForPaneVisible } from './scenarioAssertions.mjs';

function compact(text) {
  return text.replace(/\s+/g, '');
}

export async function ensureClaudeMainPromptReady(client, sessionId, timeoutMs = 40_000) {
  const startedAt = Date.now();
  let trustHandled = false;

  while (Date.now() - startedAt < timeoutMs) {
    await client.request('select_session', { sessionId });
    await waitForPaneVisible(client, sessionId, 'main', 20_000);
    const pane = await client.request('read_pane_text', { sessionId, paneId: 'main' }, { timeoutMs: 20_000 });
    const text = pane?.text || '';

    if (text.includes('Do you trust this folder?') || text.includes('Security guide')) {
      await client.request('click_pane', { sessionId, paneId: 'main' });
      await waitForPaneInputFocus(client, sessionId, 'main', 15_000);
      await client.request('type_pane_via_ui', { sessionId, paneId: 'main', text: '1' });
      await waitForPaneText(
        client,
        sessionId,
        'main',
        (nextText) => compact(nextText).includes('1yesitrustthisfolder') || compact(nextText).includes('1.Yes,Itrustthisfolder'),
        'claude trust selection',
        10_000,
      );
      await client.request('write_pane', { sessionId, paneId: 'main', text: '\r', submit: false });
      trustHandled = true;
      continue;
    }

    if (compact(text).includes('❯')) {
      await client.request('click_pane', { sessionId, paneId: 'main' });
      await waitForPaneInputFocus(client, sessionId, 'main', 15_000);
      return { trustHandled, text };
    }
  }

  throw new Error(`Timed out waiting for Claude prompt readiness in session ${sessionId}`);
}

export async function promptClaudeForStructuredBlock(client, sessionId, token, lineCount = 8) {
  const lines = Array.from({ length: lineCount }, (_, index) =>
    `${token} line ${index + 1} :: render visibility coverage verification payload ${index + 1}`
  );
  const prompt = [
    'Reply with ONLY the following lines and nothing else.',
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
