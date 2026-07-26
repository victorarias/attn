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
  /** The resolver clause behind this state, from the daemon's `state_reason`. */
  reason?: string;
}

export function StateIndicator({
  state,
  size = 'md',
  kind = 'session',
  seed,
  className = '',
  reason,
}: StateIndicatorProps) {
  // Normalize state for CSS class (waiting_input -> waiting-input)
  const stateClass = state.replace('_', '-');
  const launchingEmoji = state === 'launching' ? pickSessionEmoji(seed ?? '') : null;
  // Only `unknown` explains itself. Every other state says what it means by its
  // own name, and a tooltip repeating that is noise; `unknown` is the one badge
  // that otherwise tells the user something is wrong and nothing about what.
  const explanation = state === 'unknown' ? describeUnknownReason(reason) : undefined;

  return (
    <span
      className={`state-indicator state-indicator--${size} state-indicator--${stateClass} state-indicator--${kind} ${className}`.trim()}
      data-testid="state-indicator"
      title={explanation}
      aria-label={
        state === 'unknown'
          ? (explanation ?? 'state unknown')
          : state === 'scheduled'
            ? 'scheduled'
            : undefined
      }
    >
      {launchingEmoji}
    </span>
  );
}

// describeUnknownReason turns a resolver reason into something worth reading on
// hover. Only the reasons that can actually reach `unknown` are named; anything
// else falls back rather than inventing an explanation for a state the daemon
// reached some other way.
function describeUnknownReason(reason: string | undefined): string | undefined {
  switch (reason) {
    case 'stuck':
      return 'Stuck — the agent has stopped reporting anything at all';
    case 'no_evidence':
      return 'No signal from this agent yet';
    default:
      return undefined;
  }
}
