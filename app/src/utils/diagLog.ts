/**
 * Diagnostic logger that writes to a file for debugging.
 * Logs are written to ~/Desktop/terminal-diag.log
 */

import { writeTextFile, BaseDirectory } from '@tauri-apps/plugin-fs';

// Store logs in memory as backup
const logBuffer: string[] = [];
let writePromise: Promise<void> | null = null;
const LOG_FILE = 'terminal-diag.log';

export async function diagLog(category: string, data: Record<string, unknown>) {
  const timestamp = new Date().toISOString();
  const line = `[${timestamp}] [${category}] ${JSON.stringify(data)}\n`;

  // Also log to console for dev tools
  console.log(`[DIAG] ${category}:`, data);

  // Add to buffer
  logBuffer.push(line);

  // Write to file (debounced)
  if (!writePromise) {
    writePromise = new Promise((resolve) => {
      setTimeout(async () => {
        try {
          const content = logBuffer.join('');
          await writeTextFile(LOG_FILE, content, { baseDir: BaseDirectory.Desktop });
        } catch (e) {
          console.error('[diagLog] Failed to write log file:', e);
        }
        writePromise = null;
        resolve();
      }, 100);
    });
  }
}

export async function clearDiagLog() {
  logBuffer.length = 0;
  try {
    await writeTextFile(LOG_FILE, '', { baseDir: BaseDirectory.Desktop });
    console.log('[diagLog] Log file cleared');
  } catch (e) {
    console.error('[diagLog] Failed to clear log file:', e);
  }
}

// Export buffer for manual inspection
export function getDiagLogBuffer(): string[] {
  return [...logBuffer];
}
