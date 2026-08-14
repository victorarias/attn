import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';

interface AppViewBoundaryProps {
  /** Remounting the child after a Reload means a fresh boundary; this key resets it. */
  resetKey: string;
  onError: (error: Error, componentStack: string) => void;
  fallback: (error: Error) => ReactNode;
  children: ReactNode;
}

interface AppViewBoundaryState {
  error: Error | null;
  resetKey: string;
}

/**
 * Catches a render error from one app's view.
 *
 * Per app rather than per surface: a view that throws must cost its own tile and
 * nothing else — not the workspace it sits in, not another app's tile beside it,
 * and not attn's own chrome. React unmounts the whole tree above an uncaught
 * error, so without this the first broken view takes the window down.
 */
export class AppViewBoundary extends Component<AppViewBoundaryProps, AppViewBoundaryState> {
  state: AppViewBoundaryState = { error: null, resetKey: this.props.resetKey };

  static getDerivedStateFromProps(
    props: AppViewBoundaryProps,
    state: AppViewBoundaryState,
  ): AppViewBoundaryState | null {
    if (props.resetKey !== state.resetKey) {
      return { error: null, resetKey: props.resetKey };
    }
    return null;
  }

  static getDerivedStateFromError(error: Error): Partial<AppViewBoundaryState> {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    this.props.onError(error, info.componentStack ?? '');
  }

  render() {
    if (this.state.error) {
      return this.props.fallback(this.state.error);
    }
    return this.props.children;
  }
}
