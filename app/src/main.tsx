import { installVerbatimTextEntryGuard } from './utils/verbatimTextEntry';
import { AppErrorBoundary } from './components/AppErrorBoundary';
import { installUiDiagnostics, recordUiDiag } from './utils/uiDiagnosticsLog';

declare global { interface Window { __dbg?: (msg: string) => void } }
const dbg = (msg: string) => window.__dbg?.(msg);

dbg('main.tsx: imports resolved');
installVerbatimTextEntryGuard(document);
installUiDiagnostics();

// The Present window is a separate Tauri window (opened via
// open_presentation_window) that loads this same bundle with
// ?window=present&presentation=<id>. It renders a slim, self-contained root
// instead of the main App shell.
const isPresentWindow = new URLSearchParams(window.location.search).get('window') === 'present';

async function boot() {
  dbg('boot: loading ReactDOM');
  const ReactDOM = await import("react-dom/client");
  const root = ReactDOM.createRoot(document.getElementById("root") as HTMLElement);

  if (isPresentWindow) {
    dbg('boot: loading PresentRoot');
    const { PresentRoot } = await import("./components/PresentRoot");
    dbg('boot: createRoot (present)');
    root.render(<PresentRoot />);
    dbg('boot: render called (present)');
    return;
  }

  dbg('boot: loading App');
  const { default: App } = await import("./App");
  dbg('boot: createRoot');
  // Note: StrictMode disabled because it causes double-mounting which breaks
  // the PTY connection (terminal gets disposed and recreated)
  root.render(<AppErrorBoundary><App /></AppErrorBoundary>);
  dbg('boot: render called');
}

boot().catch((err) => {
  recordUiDiag({ kind: 'boot_failed', message: String(err), stack: err instanceof Error ? err.stack : undefined });
  dbg('boot FAILED: ' + (err?.stack || err));
});
