import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import WebSocket from 'ws';
import { defaultDaemonPortForProfile } from './harnessProfile.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
const DAEMON_SOCKET_HOOK_PATH = path.resolve(HARNESS_DIR, '../../src/hooks/useDaemonSocket.ts');

// Reads the app's own PROTOCOL_VERSION at runtime rather than hardcoding it,
// so the harness stays in lockstep with protocol bumps automatically instead
// of silently talking a stale version to the daemon.
export function readFrontendProtocolVersion() {
  const source = fs.readFileSync(DAEMON_SOCKET_HOOK_PATH, 'utf8');
  const match = /export const PROTOCOL_VERSION = '(\d+)'/.exec(source);
  if (!match) {
    throw new Error(`Could not find PROTOCOL_VERSION in ${DAEMON_SOCKET_HOOK_PATH}`);
  }
  return match[1];
}

// Opens a daemon websocket, sends client_hello, waits for the daemon's
// initial_state ack (mirroring daemonObserver.mjs), runs fn against a
// sendAndWait helper, then always closes the socket.
export async function withDaemonSocket(fn, { port = defaultDaemonPortForProfile() } = {}) {
  const wsUrl = `ws://localhost:${port}/ws`;
  const ws = new WebSocket(wsUrl);

  function sendAndWait(message, resultType, timeoutMs = 10_000) {
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        ws.off('message', onMessage);
        reject(new Error(`Timed out after ${timeoutMs}ms waiting for '${resultType}' from ${wsUrl}`));
      }, timeoutMs);
      function onMessage(raw) {
        let data;
        try {
          data = JSON.parse(raw.toString());
        } catch {
          return;
        }
        if (data.event !== resultType) {
          return;
        }
        clearTimeout(timeout);
        ws.off('message', onMessage);
        resolve(data);
      }
      ws.on('message', onMessage);
      ws.send(JSON.stringify(message));
    });
  }

  await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error(`Timed out connecting to ${wsUrl}`)), 10_000);
    ws.once('open', () => {
      clearTimeout(timeout);
      resolve();
    });
    ws.once('error', (error) => {
      clearTimeout(timeout);
      reject(error);
    });
  });

  try {
    // client_kind/version mirror the real app's hello (see useDaemonSocket.ts)
    // so daemon-side diagnostics can tell harness connections apart.
    await sendAndWait(
      {
        cmd: 'client_hello',
        client_kind: 'harness-present-daemon',
        version: `protocol-${readFrontendProtocolVersion()}`,
        capabilities: ['workspace_sessions'],
      },
      'initial_state',
    );
    return await fn(sendAndWait);
  } finally {
    ws.close();
  }
}

export async function getPresentations({ port } = {}) {
  return withDaemonSocket(async (sendAndWait) => {
    const result = await sendAndWait({ cmd: 'get_presentations' }, 'get_presentations_result');
    if (!result.success) {
      throw new Error(result.error || 'get_presentations failed');
    }
    return result.presentations;
  }, { port });
}

export async function getPresentationRound(presentationId, { port, seq } = {}) {
  return withDaemonSocket(async (sendAndWait) => {
    const message = { cmd: 'get_presentation_round', presentation_id: presentationId };
    if (typeof seq === 'number') {
      message.seq = seq;
    }
    const result = await sendAndWait(message, 'get_presentation_round_result');
    if (!result.success) {
      throw new Error(result.error || `get_presentation_round failed for ${presentationId}`);
    }
    return result;
  }, { port });
}

// Shapes a present_submit_round message. Exported (pure, no socket) so tests
// can cover payload shaping without a live daemon. verdict mirrors the
// daemon's own validation (handlePresentSubmitRound in internal/daemon/present.go):
// it must be "approved" or "feedback".
export function buildSubmitMessage({ roundId, comments = [], handback = true, verdict = 'feedback' }) {
  if (!roundId) {
    throw new Error('buildSubmitMessage requires roundId');
  }
  if (verdict !== 'approved' && verdict !== 'feedback') {
    throw new Error(`verdict must be "approved" or "feedback", got ${JSON.stringify(verdict)}`);
  }
  return {
    cmd: 'present_submit_round',
    round_id: roundId,
    verdict,
    comments: comments.map((comment, index) => {
      if (!comment.filepath) {
        throw new Error(`comments[${index}].filepath is required`);
      }
      if (comment.side !== 'new' && comment.side !== 'old') {
        throw new Error(`comments[${index}].side must be "new" or "old"`);
      }
      return {
        filepath: comment.filepath,
        line_start: comment.line_start,
        line_end: comment.line_end,
        side: comment.side,
        content: comment.content,
      };
    }),
    handback,
  };
}

// If roundId is omitted, fetches the presentation's latest round first and
// submits against it.
export async function submitPresentationRound({ presentationId, roundId, comments = [], handback = true, verdict = 'feedback' } = {}, { port } = {}) {
  return withDaemonSocket(async (sendAndWait) => {
    let resolvedRoundId = roundId;
    if (!resolvedRoundId) {
      if (!presentationId) {
        throw new Error('submitPresentationRound requires roundId or presentationId');
      }
      const roundResult = await sendAndWait(
        { cmd: 'get_presentation_round', presentation_id: presentationId },
        'get_presentation_round_result',
      );
      if (!roundResult.success) {
        throw new Error(roundResult.error || `get_presentation_round failed for ${presentationId}`);
      }
      resolvedRoundId = roundResult.round?.id;
      if (!resolvedRoundId) {
        throw new Error(`No round found for presentation ${presentationId}`);
      }
    }
    const submitMessage = buildSubmitMessage({ roundId: resolvedRoundId, comments, handback, verdict });
    const result = await sendAndWait(submitMessage, 'present_submit_round_result');
    if (!result.success) {
      throw new Error(result.error || `present_submit_round failed for round ${resolvedRoundId}`);
    }
    return result;
  }, { port });
}
