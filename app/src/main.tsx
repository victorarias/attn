import ReactDOM from "react-dom/client";
import App from "./App";

// Note: StrictMode disabled because it causes double-mounting which breaks
// the PTY connection (terminal gets disposed and recreated)
ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <App />,
);
