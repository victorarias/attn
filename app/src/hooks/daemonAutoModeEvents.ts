// pi auto mode: the four results the auto mode settings section waits on.
//
// Its own module rather than a case in the hook's switch, for the same reason
// daemonBusEvents.ts is one — the domain is self-contained. Reached from the
// switch's `default` chain.
//
// Design: docs/plans/2026-08-16-pi-auto-mode.md.

import type {
  AutoModeConfigInfo,
  AutoModeDenialInfo,
  AutoModeProposalInfo,
} from '../types/generated';
import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';

export type { AutoModeConfigInfo, AutoModeDenialInfo, AutoModeProposalInfo };

/** The promoted policy plus everything waiting on a human. */
export interface AutoModeState {
  config: AutoModeConfigInfo;
  /** Pending only — the daemon resolves promoted and discarded ones away. */
  proposals: AutoModeProposalInfo[];
  denials: AutoModeDenialInfo[];
}

/** What a direct list edit answers with: the config it produced. */
export interface AutoModePatternEdit {
  config: AutoModeConfigInfo;
}

/** What a promote answers with: the resolved proposal and the config it produced. */
export interface AutoModePromotion {
  proposal: AutoModeProposalInfo | null;
  config: AutoModeConfigInfo | null;
}

// A loosely typed view of the wire event: these arrive as parsed JSON, so every
// field is checked rather than asserted.
interface AutoModeDaemonEvent {
  event?: string;
  success?: boolean;
  error?: string;
  [key: string]: unknown;
}

const list = <T,>(value: unknown): T[] => (Array.isArray(value) ? (value as T[]) : []);

const emptyConfig = (): AutoModeConfigInfo => ({
  enabled_default: false,
  environment: [],
  allow: [],
  hard_deny: [],
  shipped_hard_deny: [],
  classifier_models: [],
  escalation_models: [],
});

const toConfig = (value: unknown): AutoModeConfigInfo => {
  if (typeof value !== 'object' || value === null) return emptyConfig();
  const raw = value as Record<string, unknown>;
  return {
    enabled_default: raw.enabled_default === true,
    environment: list<string>(raw.environment),
    allow: list<string>(raw.allow),
    hard_deny: list<string>(raw.hard_deny),
    shipped_hard_deny: list<string>(raw.shipped_hard_deny),
    classifier_models: list<string>(raw.classifier_models),
    escalation_models: list<string>(raw.escalation_models),
  };
};

const toState = (event: AutoModeDaemonEvent): AutoModeState => ({
  config: toConfig(event.config),
  proposals: list<AutoModeProposalInfo>(event.proposals),
  denials: list<AutoModeDenialInfo>(event.denials),
});

const toPatternEdit = (event: AutoModeDaemonEvent): AutoModePatternEdit | undefined =>
  event.config === undefined ? undefined : { config: toConfig(event.config) };

const toPromotion = (event: AutoModeDaemonEvent): AutoModePromotion => ({
  proposal: (event.proposal as AutoModeProposalInfo | undefined) ?? null,
  config: event.config === undefined ? null : toConfig(event.config),
});

/**
 * Settles an auto mode result, or returns false for an event this module does
 * not own so the caller can keep looking.
 */
export function handleAutoModeDaemonEvent(
  event: AutoModeDaemonEvent,
  pending: PendingRequests,
): boolean {
  switch (event.event) {
    case 'automode_state_result':
      settlePendingRequest(
        pending,
        'automode_get',
        event,
        toState,
        'Reading auto mode failed',
      );
      return true;
    case 'automode_promote_result':
      settlePendingRequest(
        pending,
        'automode_promote',
        event,
        toPromotion,
        'Promoting the proposal failed',
      );
      return true;
    // One event answers both edits, so the settle is tried under each command's
    // key; only the one actually in flight has a waiter, and settlePendingRequest
    // reports a miss rather than treating it as a failure.
    case 'automode_pattern_result': {
      const settled = settlePendingRequest(
        pending,
        'automode_pattern_add',
        event,
        toPatternEdit,
        'Adding the pattern failed',
      );
      if (!settled) {
        settlePendingRequest(
          pending,
          'automode_pattern_remove',
          event,
          toPatternEdit,
          'Removing the pattern failed',
        );
      }
      return true;
    }
    case 'automode_discard_result':
      settlePendingRequest(
        pending,
        'automode_discard',
        event,
        toPromotion,
        'Discarding the proposal failed',
      );
      return true;
    default:
      return false;
  }
}
