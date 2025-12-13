import * as net from 'net';
import * as os from 'os';
import * as path from 'path';
import * as pty from 'node-pty';
import * as fs from 'fs';
import {
  PTY_COMMANDS,
  PTY_EVENTS,
  PtyCommand,
  PtyDataEvent,
  PtyExitEvent,
  PtySpawnedEvent,
  PtyErrorEvent,
  PtyEvent,
} from './pty-protocol.js';

const SOCKET_PATH = path.join(os.homedir(), '.attn-pty.sock');
const MAX_BUFFER_SIZE = 1024 * 1024; // 1MB max buffer to prevent memory exhaustion

interface Session {
  pty: pty.IPty;
  socketId: symbol;
  dataDisposable?: { dispose(): void }; // IDisposable from onData listener
}

const sessions = new Map<string, Session>();

// Frame protocol: [4-byte length][JSON payload]
function writeFrame(socket: net.Socket, data: PtyEvent): void {
  const json = JSON.stringify(data);
  const buf = Buffer.alloc(4 + Buffer.byteLength(json));
  buf.writeUInt32BE(Buffer.byteLength(json), 0);
  buf.write(json, 4);
  socket.write(buf);
}

function sendError(socket: net.Socket, cmd: string, error: string): void {
  const event: PtyErrorEvent = {
    event: PTY_EVENTS.ERROR,
    cmd,
    error,
  };
  writeFrame(socket, event);
}

function handleMessage(socket: net.Socket, socketId: symbol, msg: PtyCommand): void {
  switch (msg.cmd) {
    case PTY_COMMANDS.SPAWN: {
      // Validate required fields
      if (!msg.id || typeof msg.id !== 'string') {
        sendError(socket, PTY_COMMANDS.SPAWN, 'missing or invalid id');
        return;
      }
      if (msg.cols !== undefined && (typeof msg.cols !== 'number' || msg.cols <= 0)) {
        sendError(socket, PTY_COMMANDS.SPAWN, 'invalid cols');
        return;
      }
      if (msg.rows !== undefined && (typeof msg.rows !== 'number' || msg.rows <= 0)) {
        sendError(socket, PTY_COMMANDS.SPAWN, 'invalid rows');
        return;
      }
      if (msg.cwd !== undefined && typeof msg.cwd !== 'string') {
        sendError(socket, PTY_COMMANDS.SPAWN, 'invalid cwd');
        return;
      }

      // Spawn attn (Attention Manager) wrapper which registers with daemon and sets up hooks
      // Use fish login shell to ensure PATH includes ~/.local/bin
      // Set TERM=xterm-256color for proper xterm.js compatibility
      const ptyProcess = pty.spawn('/opt/homebrew/bin/fish', ['-l', '-c', 'set -x TERM xterm-256color; attn -y'], {
        name: 'xterm-256color',
        cols: msg.cols || 80,
        rows: msg.rows || 24,
        cwd: msg.cwd || os.homedir(),
        env: { ...process.env, TERM: 'xterm-256color' } as { [key: string]: string },
      });

      const dataDisposable = ptyProcess.onData((data) => {
        const event: PtyDataEvent = {
          event: PTY_EVENTS.DATA,
          id: msg.id,
          data: Buffer.from(data).toString('base64'),
        };
        writeFrame(socket, event);
      });

      sessions.set(msg.id, { pty: ptyProcess, socketId, dataDisposable });

      ptyProcess.onExit(({ exitCode }) => {
        // Clean up the data listener before removing from sessions
        const session = sessions.get(msg.id);
        if (session?.dataDisposable) {
          session.dataDisposable.dispose();
        }

        const event: PtyExitEvent = {
          event: PTY_EVENTS.EXIT,
          id: msg.id,
          code: exitCode,
        };
        writeFrame(socket, event);
        sessions.delete(msg.id);
      });

      const spawnedEvent: PtySpawnedEvent = {
        event: PTY_EVENTS.SPAWNED,
        id: msg.id,
        pid: ptyProcess.pid,
      };
      writeFrame(socket, spawnedEvent);
      break;
    }

    case PTY_COMMANDS.WRITE: {
      // Validate required fields
      if (!msg.id || typeof msg.id !== 'string') {
        sendError(socket, PTY_COMMANDS.WRITE, 'missing or invalid id');
        return;
      }
      if (!msg.data || typeof msg.data !== 'string') {
        sendError(socket, PTY_COMMANDS.WRITE, 'missing or invalid data');
        return;
      }

      const session = sessions.get(msg.id);
      if (session) {
        session.pty.write(msg.data);
      }
      break;
    }

    case PTY_COMMANDS.RESIZE: {
      // Validate required fields
      if (!msg.id || typeof msg.id !== 'string') {
        sendError(socket, PTY_COMMANDS.RESIZE, 'missing or invalid id');
        return;
      }
      if (typeof msg.cols !== 'number' || msg.cols <= 0) {
        sendError(socket, PTY_COMMANDS.RESIZE, 'missing or invalid cols');
        return;
      }
      if (typeof msg.rows !== 'number' || msg.rows <= 0) {
        sendError(socket, PTY_COMMANDS.RESIZE, 'missing or invalid rows');
        return;
      }

      const session = sessions.get(msg.id);
      if (session) {
        session.pty.resize(msg.cols, msg.rows);
      }
      break;
    }

    case PTY_COMMANDS.KILL: {
      // Validate required fields
      if (!msg.id || typeof msg.id !== 'string') {
        sendError(socket, PTY_COMMANDS.KILL, 'missing or invalid id');
        return;
      }

      const session = sessions.get(msg.id);
      if (session) {
        if (session.dataDisposable) {
          session.dataDisposable.dispose();
        }
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

    // Prevent memory exhaustion from malicious/misbehaving clients
    if (buffer.length > MAX_BUFFER_SIZE) {
      console.error('[pty-server] Buffer overflow, disconnecting client');
      socket.destroy(new Error('Buffer overflow'));
      return;
    }

    while (buffer.length >= 4) {
      const len = buffer.readUInt32BE(0);
      if (buffer.length < 4 + len) break;

      const json = buffer.subarray(4, 4 + len).toString();
      buffer = buffer.subarray(4 + len);

      try {
        const msg = JSON.parse(json) as PtyCommand;
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
        if (session.dataDisposable) {
          session.dataDisposable.dispose();
        }
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
