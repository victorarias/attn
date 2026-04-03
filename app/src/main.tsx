declare global { interface Window { __dbg?: (msg: string) => void } }
const dbg = (msg: string) => window.__dbg?.(msg);

dbg('main.tsx: imports resolved');

async function boot() {
  dbg('boot: loading ReactDOM');
  const ReactDOM = await import("react-dom/client");
  dbg('boot: loading App');
  const { default: App } = await import("./App");
  dbg('boot: createRoot');
  // Note: StrictMode disabled because it causes double-mounting which breaks
  // the PTY connection (terminal gets disposed and recreated)
  ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
    <App />,
  );
  dbg('boot: render called');
}

boot().catch((err) => dbg('boot FAILED: ' + (err?.stack || err)));
