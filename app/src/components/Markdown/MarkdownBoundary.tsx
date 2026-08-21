import { Component, type ReactNode } from 'react';

/**
 * Keeps one unrenderable message from taking the surface down with it.
 *
 * A transcript is append-only and its markdown comes from a model, so a
 * construct that throws inside a plugin would otherwise blank the whole pane
 * and every message above it. The fallback shows the text as it arrived, which
 * is strictly more than nothing.
 */
export class MarkdownBoundary extends Component<
  { children: ReactNode; fallback: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  componentDidUpdate(previous: { children: ReactNode }) {
    // The next delta is a different document. A message that failed half-typed
    // is usually renderable once it is whole, so the boundary re-arms.
    if (this.state.failed && previous.children !== this.props.children) this.setState({ failed: false });
  }

  render() {
    return this.state.failed ? this.props.fallback : this.props.children;
  }
}
