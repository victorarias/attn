import { describe, expect, it } from 'vitest';
import type { Seed } from '../hooks/useDaemonSocket';
import { heldByOther, legalVerbs } from './gardenBoardModel';

function seed(status: string, tender: { session?: string; member?: string } = {}): Seed {
  return {
    body: '',
    created_at: '',
    edges: [],
    gate: false,
    id: 's-7k3f9m',
    planter_member: '',
    planter_session: '',
    ready: false,
    rev: 1,
    status,
    step_slug: '',
    template: false,
    tender_member: tender.member ?? '',
    tender_session: tender.session ?? '',
    title: 'a seed',
    updated_at: '',
    vars: [],
  } as Seed;
}

// `replant` is the one move that lands on `planted`, so the board's Ready column
// is reachable from everywhere except Ready itself. These are the two arrows the
// board used to be missing, and the one it still legitimately has no zone for.
describe('a seed goes back to the pool from anywhere but the pool', () => {
  it('hands back a seed being grown, without closing it', () => {
    expect(legalVerbs(seed('growing', { session: 'sess-a' }), 'ready')).toEqual(['replant']);
  });

  it('un-parks a dormant seed', () => {
    expect(legalVerbs(seed('dormant'), 'ready')).toEqual(['replant']);
  });

  it('still reopens a closed one', () => {
    expect(legalVerbs(seed('harvested'), 'ready')).toEqual(['replant']);
    expect(legalVerbs(seed('withered'), 'ready')).toEqual(['replant']);
  });

  it('grows no zone for a seed already in the pool', () => {
    expect(legalVerbs(seed('planted'), 'ready')).toEqual([]);
  });
});

// heldByOther is garden.Tender.Holds read from the board's side. It decides
// whether the composer draws its takeover line, which is the board's --confirm.
describe('who still holds a card', () => {
  const live = new Set(['sess-a']);

  it('names a tender whose session is still alive', () => {
    expect(heldByOther(seed('growing', { session: 'sess-a', member: 'alder' }), live)).toBe('Alder');
  });

  it('names nobody once that session has ended', () => {
    expect(heldByOther(seed('growing', { session: 'sess-gone', member: 'alder' }), live)).toBe('');
  });

  it('names a member with no session, because attn cannot see a person leave', () => {
    expect(heldByOther(seed('growing', { member: 'alder' }), live)).toBe('Alder');
  });

  it('names nobody when nothing holds the seed', () => {
    expect(heldByOther(seed('planted'), live)).toBe('');
  });
});
