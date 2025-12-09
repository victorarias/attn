// PTY client that communicates with pty-server via Tauri commands
// For now, direct socket connection for dev; will use Tauri bridge in prod

type DataCallback = (data: string) => void;
type ExitCallback = (code: number) => void;

interface PtySession {
  id: string;
  onData: (cb: DataCallback) => void;
  onExit: (cb: ExitCallback) => void;
  write: (data: string) => void;
  resize: (cols: number, rows: number) => void;
  kill: () => void;
}

interface PtyClient {
  spawn: (cwd: string, cols: number, rows: number) => Promise<PtySession>;
  close: () => void;
}

// Frame protocol helpers
function encodeFrame(data: object): ArrayBuffer {
  const json = JSON.stringify(data);
  const jsonBytes = new TextEncoder().encode(json);
  const buf = new ArrayBuffer(4 + jsonBytes.length);
  const view = new DataView(buf);
  view.setUint32(0, jsonBytes.length, false); // big-endian
  new Uint8Array(buf, 4).set(jsonBytes);
  return buf;
}

export function createPtyClient(socketPath: string): Promise<PtyClient> {
  return new Promise((resolve, reject) => {
    // In browser/Tauri context, we'll use Tauri commands
    // This is a placeholder for the WebSocket-style interface
    // For dev, we'll expose this via a simple HTTP bridge or Tauri command

    const sessions = new Map<string, {
      dataCallbacks: DataCallback[];
      exitCallbacks: ExitCallback[];
    }>();

    let sessionCounter = 0;

    // This will be replaced with actual Tauri invoke calls
    const client: PtyClient = {
      spawn: async (cwd: string, cols: number, rows: number): Promise<PtySession> => {
        const id = `pty-${++sessionCounter}`;

        sessions.set(id, {
          dataCallbacks: [],
          exitCallbacks: [],
        });

        // Will call Tauri command: invoke('pty_spawn', { id, cwd, cols, rows })

        return {
          id,
          onData: (cb) => {
            sessions.get(id)?.dataCallbacks.push(cb);
          },
          onExit: (cb) => {
            sessions.get(id)?.exitCallbacks.push(cb);
          },
          write: (data) => {
            // Will call: invoke('pty_write', { id, data })
          },
          resize: (cols, rows) => {
            // Will call: invoke('pty_resize', { id, cols, rows })
          },
          kill: () => {
            // Will call: invoke('pty_kill', { id })
            sessions.delete(id);
          },
        };
      },
      close: () => {
        sessions.clear();
      },
    };

    resolve(client);
  });
}

// Export types for use elsewhere
export type { PtySession, PtyClient };
