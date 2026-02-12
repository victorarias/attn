// app/src/components/StateIndicator.tsx
import './StateIndicator.css';
import { pickSessionEmoji } from '../utils/sessionEmoji';
import type { UISessionState } from '../types/sessionState';

type StateIndicatorState = UISessionState;
type StateIndicatorSize = 'sm' | 'md' | 'lg';
type StateIndicatorKind = 'session' | 'pr';

interface StateIndicatorProps {
  state: StateIndicatorState;
  size?: StateIndicatorSize;
  kind?: StateIndicatorKind;
  seed?: string;
  className?: string;
}

export function StateIndicator({
  state,
  size = 'md',
  kind = 'session',
  seed,
  className = '',
}: StateIndicatorProps) {
  // Normalize state for CSS class (waiting_input -> waiting-input)
  const stateClass = state.replace('_', '-');
  const launchingEmoji = state === 'launching' ? pickSessionEmoji(seed ?? '') : null;

  return (
    <span
      className={`state-indicator state-indicator--${size} state-indicator--${stateClass} state-indicator--${kind} ${className}`.trim()}
      data-testid="state-indicator"
      aria-label={state === 'unknown' ? 'state unknown' : undefined}
    >
      {launchingEmoji}
    </span>
  );
}
