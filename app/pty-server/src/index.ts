import * as net from 'net';
import * as os from 'os';
import * as path from 'path';
import * as pty from 'node-pty';
import * as fs from 'fs';

const SOCKET_PATH = path.join(os.homedir(), '.cm-pty.sock');

interface Session {
  pty: pty.IPty;
  socketId: symbol;
}

const sessions = new Map<string, Session>();

// Frame protocol: [4-byte length][JSON payload]
function writeFrame(socket: net.Socket, data: object): void {
  const json = JSON.stringify(data);
  const buf = Buffer.alloc(4 + Buffer.byteLength(json));
  buf.writeUInt32BE(Buffer.byteLength(json), 0);
  buf.write(json, 4);
  socket.write(buf);
}

function handleMessage(socket: net.Socket, socketId: symbol, msg: any): void {
  switch (msg.cmd) {
    case 'spawn': {
      // Validate required fields
      if (!msg.id || typeof msg.id !== 'string') {
        console.error('[pty-server] spawn: missing or invalid id');
        return;
      }
      if (msg.cols !== undefined && (typeof msg.cols !== 'number' || msg.cols <= 0)) {
        console.error('[pty-server] spawn: invalid cols');
        return;
      }
      if (msg.rows !== undefined && (typeof msg.rows !== 'number' || msg.rows <= 0)) {
        console.error('[pty-server] spawn: invalid rows');
        return;
      }
      if (msg.cwd !== undefined && typeof msg.cwd !== 'string') {
        console.error('[pty-server] spawn: invalid cwd');
        return;
      }

      const shell = process.env.SHELL || '/bin/bash';
      const ptyProcess = pty.spawn(shell, [], {
        name: 'xterm-256color',
        cols: msg.cols || 80,
        rows: msg.rows || 24,
        cwd: msg.cwd || os.homedir(),
        env: process.env as { [key: string]: string },
      });

      sessions.set(msg.id, { pty: ptyProcess, socketId });

      ptyProcess.onData((data) => {
        writeFrame(socket, {
          event: 'data',
          id: msg.id,
          data: Buffer.from(data).toString('base64'),
        });
      });

      ptyProcess.onExit(({ exitCode }) => {
        writeFrame(socket, { event: 'exit', id: msg.id, code: exitCode });
        sessions.delete(msg.id);
      });

      writeFrame(socket, { event: 'spawned', id: msg.id, pid: ptyProcess.pid });
      break;
    }

    case 'write': {
      // Validate required fields
      if (!msg.id || typeof msg.id !== 'string') {
        console.error('[pty-server] write: missing or invalid id');
        return;
      }
      if (!msg.data || typeof msg.data !== 'string') {
        console.error('[pty-server] write: missing or invalid data');
        return;
      }

      const session = sessions.get(msg.id);
      if (session) {
        session.pty.write(msg.data);
      }
      break;
    }

    case 'resize': {
      // Validate required fields
      if (!msg.id || typeof msg.id !== 'string') {
        console.error('[pty-server] resize: missing or invalid id');
        return;
      }
      if (typeof msg.cols !== 'number' || msg.cols <= 0) {
        console.error('[pty-server] resize: missing or invalid cols');
        return;
      }
      if (typeof msg.rows !== 'number' || msg.rows <= 0) {
        console.error('[pty-server] resize: missing or invalid rows');
        return;
      }

      const session = sessions.get(msg.id);
      if (session) {
        session.pty.resize(msg.cols, msg.rows);
      }
      break;
    }

    case 'kill': {
      // Validate required fields
      if (!msg.id || typeof msg.id !== 'string') {
        console.error('[pty-server] kill: missing or invalid id');
        return;
      }

      const session = sessions.get(msg.id);
      if (session) {
        session.pty.kill();
        sessions.delete(msg.id);
      }
      break;
    }
  }
}

// Remove stale socket
try { fs.unlinkSync(SOCKET_PATH); } catch {}

const server = net.createServer((socket) => {
  console.log('[pty-server] Client connected');
  const socketId = Symbol('socket');
  let buffer = Buffer.alloc(0);

  socket.on('data', (chunk) => {
    buffer = Buffer.concat([buffer, chunk]);

    while (buffer.length >= 4) {
      const len = buffer.readUInt32BE(0);
      if (buffer.length < 4 + len) break;

      const json = buffer.subarray(4, 4 + len).toString();
      buffer = buffer.subarray(4 + len);

      try {
        const msg = JSON.parse(json);
        handleMessage(socket, socketId, msg);
      } catch (e) {
        console.error('[pty-server] Parse error:', e);
      }
    }
  });

  socket.on('close', () => {
    console.log('[pty-server] Client disconnected');
    // Kill only sessions for this socket
    for (const [id, session] of sessions) {
      if (session.socketId === socketId) {
        session.pty.kill();
        sessions.delete(id);
      }
    }
  });
});

try {
  server.listen(SOCKET_PATH, () => {
    console.log(`[pty-server] Listening on ${SOCKET_PATH}`);
  });

  server.on('error', (err) => {
    console.error('[pty-server] Server error:', err);
    process.exit(1);
  });
} catch (err) {
  console.error('[pty-server] Failed to start server:', err);
  process.exit(1);
}
