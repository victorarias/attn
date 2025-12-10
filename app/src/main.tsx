import ReactDOM from "react-dom/client";
import App from "./App";
import { TestTerminal } from "./test-terminal";

// Check for test mode via query param: ?test=terminal
const params = new URLSearchParams(window.location.search);
const testMode = params.get('test');

// Note: StrictMode disabled because it causes double-mounting which breaks
// the PTY connection (terminal gets disposed and recreated)
ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  testMode === 'terminal' ? <TestTerminal /> : <App />,
);
