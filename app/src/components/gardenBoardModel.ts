// app/src/components/gardenBoardModel.ts
//
// The board's projection, with no React in it: which column a seed stands in,
// which of the garden's verbs a drop from one column to another may use, and
// who still holds a card. It is a reading of internal/garden/lifecycle.go, never
// a second state machine — nothing here is stored, and every answer is computed
// from the seed the daemon already pushed.
//
// It sits beside GardenBoard.tsx rather than inside it because these are the
// board's rules, and rules are what a test wants to read.
import type { Seed } from '../hooks/useDaemonSocket';
import { crewHolderName } from '../utils/crewName';

export type ColumnKey = 'ready' | 'growing' | 'parked' | 'closed';

export type Verb = 'park' | 'harvest' | 'wither' | 'replant' | 'dispatch';

const CLOSED = new Set(['harvested', 'withered']);

export function tenderOf(seed: Seed): string {
  return crewHolderName(seed.tender_member, seed.tender_session);
}

// heldByOther names who still holds this seed, or '' when nobody does. It is
// garden.Tender.Holds read from the board's side: a claim signed by a session
// holds while that session is alive, and a crew member with no session always
// does, because attn has no signal that a person walked away.
//
// The board's own actor is unnamed, so anybody it names here is somebody else,
// and the move is a takeover the daemon refuses until the composer confirms it.
export function heldByOther(seed: Seed, liveSessions: Set<string>): string {
  if (!seed.tender_session && !seed.tender_member) return '';
  if (seed.tender_session && !liveSessions.has(seed.tender_session)) return '';
  return tenderOf(seed);
}

// columnOf reads a card's column off the seed. Four states, and every open seed
// lands somewhere: what is neither growing, parked nor closed is waiting to be
// picked up, whether or not anything is holding it back.
export function columnOf(seed: Seed): ColumnKey {
  if (CLOSED.has(seed.status)) return 'closed';
  if (seed.status === 'dormant') return 'parked';
  if (seed.status === 'growing') return 'growing';
  return 'ready';
}

// legalVerbs is the garden's own lifecycle table (internal/garden/lifecycle.go)
// read from the board's side: which verbs the target column owns, and which of
// them the seed's current state would accept.
//
// `replant` is the one move that lands on `planted`, so it is the only way back
// to Ready — from Closed, from Parked, and from Growing, which is how a seat is
// handed back without closing the work. The single absence left is Ready →
// Ready: a seed already in the pool has nowhere to be put back to.
export function legalVerbs(seed: Seed, target: ColumnKey): Verb[] {
  const status = seed.status;
  const open = status === 'planted' || status === 'growing' || status === 'dormant';
  switch (target) {
    case 'closed':
      return open ? ['harvest', 'wither'] : [];
    case 'parked':
      return status === 'planted' || status === 'growing' ? ['park'] : [];
    case 'ready':
      return status === 'planted' ? [] : ['replant'];
    case 'growing':
      // Dispatching is an intent, not a state: the agent claims the seed when it
      // starts. A seed already being worked has nobody to dispatch to.
      return status === 'planted' || status === 'dormant' ? ['dispatch'] : [];
  }
}

// The verbs a card offers on its own, which is every zone on the board unioned.
// The keyboard path and the drag path must never disagree, so they read the
// same table.
export function verbsFor(seed: Seed): Verb[] {
  const columns: ColumnKey[] = ['growing', 'parked', 'closed', 'ready'];
  return columns.flatMap((column) => legalVerbs(seed, column));
}

interface VerbSpec {
  label: string;
  // What the composer asks for, and whether it may be skipped.
  prompt: string;
  required: boolean;
  // A reason is stored on the seed by harvest and wither alone; the other moves
  // refuse one, so their sentence goes on the log instead.
  reasonOnSeed: boolean;
}

export const VERBS: Record<Verb, VerbSpec> = {
  harvest: {
    label: 'Harvest',
    prompt: 'what got done',
    required: true,
    reasonOnSeed: true,
  },
  wither: {
    label: 'Wither',
    prompt: 'why nobody should pick this up',
    required: false,
    reasonOnSeed: true,
  },
  park: {
    label: 'Park',
    prompt: 'what you are leaving it at',
    required: false,
    reasonOnSeed: false,
  },
  replant: {
    label: 'Replant',
    prompt: 'why it is open again',
    required: false,
    reasonOnSeed: false,
  },
  dispatch: {
    label: 'Dispatch an agent',
    prompt: '',
    required: false,
    reasonOnSeed: false,
  },
};
