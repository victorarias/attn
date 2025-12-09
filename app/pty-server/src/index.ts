import * as net from 'net';
import * as os from 'os';
import * as path from 'path';
import * as pty from 'node-pty';

const SOCKET_PATH = path.join(os.homedir(), '.cm-pty.sock');

interface Session {
  pty: pty.IPty;
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

function handleMessage(socket: net.Socket, msg: any): void {
  switch (msg.cmd) {
    case 'spawn': {
      const shell = process.env.SHELL || '/bin/bash';
      const ptyProcess = pty.spawn(shell, [], {
        name: 'xterm-256color',
        cols: msg.cols || 80,
        rows: msg.rows || 24,
        cwd: msg.cwd || os.homedir(),
        env: process.env as { [key: string]: string },
      });

      sessions.set(msg.id, { pty: ptyProcess });

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
      const session = sessions.get(msg.id);
      if (session) {
        session.pty.write(msg.data);
      }
      break;
    }

    case 'resize': {
      const session = sessions.get(msg.id);
      if (session) {
        session.pty.resize(msg.cols, msg.rows);
      }
      break;
    }

    case 'kill': {
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
import * as fs from 'fs';
try { fs.unlinkSync(SOCKET_PATH); } catch {}

const server = net.createServer((socket) => {
  console.log('[pty-server] Client connected');
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
        handleMessage(socket, msg);
      } catch (e) {
        console.error('[pty-server] Parse error:', e);
      }
    }
  });

  socket.on('close', () => {
    console.log('[pty-server] Client disconnected');
    // Kill all sessions for this socket
    for (const [id, session] of sessions) {
      session.pty.kill();
      sessions.delete(id);
    }
  });
});

server.listen(SOCKET_PATH, () => {
  console.log(`[pty-server] Listening on ${SOCKET_PATH}`);
});
