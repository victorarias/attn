import { Component, type ErrorInfo, type ReactNode } from 'react';
import { recordReactError } from '../utils/uiDiagnosticsLog';

export class AppErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    recordReactError(error, info.componentStack ?? undefined);
  }

  render() {
    if (this.state.error) {
      return (
        <main className="app-fatal-error" role="alert">
          <h1>attn hit a UI error</h1>
          <p>The error was saved to the UI diagnostics log. Restart attn to continue.</p>
          <code>{this.state.error.message}</code>
        </main>
      );
    }
    return this.props.children;
  }
}

