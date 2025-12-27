// app/src/components/StateIndicator.tsx
import './StateIndicator.css';

type StateIndicatorState = 'working' | 'waiting_input' | 'idle' | 'pending_approval';
type StateIndicatorSize = 'sm' | 'md' | 'lg';
type StateIndicatorKind = 'session' | 'pr';

interface StateIndicatorProps {
  state: StateIndicatorState;
  size?: StateIndicatorSize;
  kind?: StateIndicatorKind;
  className?: string;
}

export function StateIndicator({
  state,
  size = 'md',
  kind = 'session',
  className = '',
}: StateIndicatorProps) {
  // Normalize state for CSS class (waiting_input -> waiting-input)
  const stateClass = state.replace('_', '-');

  return (
    <span
      className={`state-indicator state-indicator--${size} state-indicator--${stateClass} state-indicator--${kind} ${className}`.trim()}
      data-testid="state-indicator"
    />
  );
}
