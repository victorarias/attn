import { describe, expect, it } from 'vitest';
import type { Seed } from '../hooks/useDaemonSocket';
import {
  buildIndex,
  matchEntry,
  parseQuery,
  satisfiesLens,
  searchGarden,
  splitRanges,
  toggleToken,
} from './gardenSearch';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'planted',
    step_slug: overrides.title,
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    ready: false,
    template: false,
    gate: false,
    vars: [],
    rev: 1,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  };
}

const blockers = new Map<string, number>();

function index(seeds: Seed[]) {
  return buildIndex(seeds, {
    tenderOf: (s) => s.tender_member || s.tender_session || '',
    blockersOf: (s) => blockers.get(s.id) ?? 0,
  });
}

function titles(seeds: Seed[], raw: string): string[] {
  return searchGarden(index(seeds), parseQuery(raw)).map((match) => match.seed.title);
}

describe('parseQuery', () => {
  it('separates operators from the words that are just words', () => {
    const q = parseQuery('reconnect is:ready tender:hazel socket');

    expect(q.terms).toEqual(['reconnect', 'socket']);
    expect(q.is).toEqual(['ready']);
    expect(q.tenders).toEqual(['hazel']);
    expect(q.unknown).toEqual([]);
  });

  // A query is not a language, so a value nobody implemented has to say so
  // rather than quietly returning nothing.
  it('names a value its operator does not have instead of matching in silence', () => {
    const q = parseQuery('is:done');

    expect(q.unknown).toEqual(['is:done']);
    expect(q.terms).toEqual([]);
  });

  // `GardenPanel.tsx:250` and `https://…` are ordinary things to search for in
  // this garden, so only the two operator names are operators and every other
  // colon is a character.
  it('leaves every other colon alone', () => {
    const q = parseQuery('GardenPanel.tsx:250');

    expect(q.terms).toEqual(['gardenpanel.tsx:250']);
    expect(q.unknown).toEqual([]);
  });

  // Half-typed is a state the reader passes through on the way to every query.
  // Treating it as an error would make the panel flash red at correct typing.
  it('treats the operator being typed as a request for the values, not a mistake', () => {
    expect(parseQuery('is:').partial).toBe('is');
    expect(parseQuery('is:re').partial).toBe('is');
    expect(parseQuery('tender:').partial).toBe('tender');
    expect(parseQuery('is:').unknown).toEqual([]);
  });

  // ...but only where the cursor is. A half-typed token with something after it
  // is finished, and wrong.
  it('stops forgiving a half-typed value once the reader has moved past it', () => {
    expect(parseQuery('is:re socket').unknown).toEqual(['is:re']);
    expect(parseQuery('is:re socket').partial).toBeNull();
  });

  // The panel runs two different lists off this: text searches the subtree,
  // a bare lens re-filters the level you are standing on.
  it('separates a search from a lens', () => {
    expect(parseQuery('is:any').active).toBe(true);
    expect(parseQuery('is:any').searches).toBe(false);
    expect(parseQuery('socket').searches).toBe(true);
    expect(parseQuery('tender:hazel').searches).toBe(true);
    expect(parseQuery('   ').active).toBe(false);
  });
});

describe('toggleToken', () => {
  it('adds a token, removes the same token, and leaves the rest of the query alone', () => {
    expect(toggleToken('socket', 'is:any')).toBe('socket is:any');
    expect(toggleToken('socket is:any', 'is:any')).toBe('socket');
    expect(toggleToken('', 'is:any')).toBe('is:any');
    expect(toggleToken('socket IS:ANY reconnect', 'is:any')).toBe('socket reconnect');
  });
});

describe('the status lens', () => {
  const open = seed({ id: 's-open01', title: 'open work' });
  const done = seed({ id: 's-done01', title: 'finished work', status: 'harvested' });
  const dropped = seed({ id: 's-drop01', title: 'abandoned work', status: 'withered' });
  const ready = seed({ id: 's-redy01', title: 'ready work', ready: true });
  const sleeping = seed({ id: 's-dorm01', title: 'sleeping work', status: 'dormant' });
  const all = [open, done, dropped, ready, sleeping];

  // The garden keeps everything ever harvested, so most of it is done. Closed
  // work is out of the way until something asks for it.
  it('hides closed seeds until a token asks for them', () => {
    expect(titles(all, 'work')).toEqual(['open work', 'ready work', 'sleeping work']);
    expect(titles(all, 'work is:any')).toHaveLength(5);
    expect(titles(all, 'work is:closed')).toEqual(['finished work', 'abandoned work']);
  });

  it('unions several lenses rather than intersecting them', () => {
    expect(titles(all, 'work is:ready is:dormant')).toEqual(['ready work', 'sleeping work']);
  });

  it('counts a seed blocked only while something open blocks it', () => {
    const blocked = seed({ id: 's-blok01', title: 'blocked work' });
    blockers.set('s-blok01', 1);
    try {
      expect(titles([...all, blocked], 'work is:blocked')).toEqual(['blocked work']);
    } finally {
      blockers.delete('s-blok01');
    }
  });

  it('is the same predicate the panel filters a plain list with', () => {
    const [entry] = index([done]);
    expect(satisfiesLens(entry, [])).toBe(false);
    expect(satisfiesLens(entry, ['any'])).toBe(true);
    expect(satisfiesLens(entry, ['open'])).toBe(false);
  });
});

describe('matching', () => {
  const seeds = [
    seed({ id: 's-abc123', title: 'reconnect the socket', body: 'the daemon drops the socket' }),
    seed({ id: 's-def456', title: 'garden search', body: 'search over the pushed snapshot' }),
    seed({ id: 's-ghi789', title: 'unrelated', body: 'nothing to do with any of it', tender_member: 'hazel' }),
  ];

  it('looks in the title, the body, the id, and the tender', () => {
    expect(titles(seeds, 'socket')).toEqual(['reconnect the socket']);
    expect(titles(seeds, 'snapshot')).toEqual(['garden search']);
    expect(titles(seeds, 's-ghi789')).toEqual(['unrelated']);
    expect(titles(seeds, 'hazel')).toEqual(['unrelated']);
  });

  // Two words is the reader narrowing, not widening.
  it('requires every term to hit, though not all in the same place', () => {
    expect(titles(seeds, 'socket daemon')).toEqual(['reconnect the socket']);
    expect(titles(seeds, 'socket garden')).toEqual([]);
  });

  // The ranking is the whole reason a search feels like an answer: the row you
  // meant is the row your fingers are already on.
  it('ranks an id over a title, a title over a tender, and a tender over a body', () => {
    const pool = [
      seed({ id: 's-body01', title: 'only in the body', body: 'a passing mention of hazel' }),
      seed({ id: 's-tend01', title: 'nothing either', tender_member: 'hazel' }),
      seed({ id: 's-titl01', title: 'hazel plants a thing' }),
      seed({ id: 's-hazel1', title: 'nothing at all' }),
    ];

    expect(titles(pool, 'hazel')).toEqual([
      'nothing at all',
      'hazel plants a thing',
      'nothing either',
      'only in the body',
    ]);
  });

  it('breaks ties by keeping the order the garden pushed', () => {
    const pool = [
      seed({ id: 's-one001', title: 'socket one' }),
      seed({ id: 's-two002', title: 'socket two' }),
    ];
    expect(titles(pool, 'socket')).toEqual(['socket one', 'socket two']);
  });

  // A body hit has to say why it is on screen, and a title hit already does.
  it('gives a snippet only to a row whose title does not explain itself', () => {
    const pool = index(seeds);
    const bodyHit = matchEntry(pool[1], parseQuery('pushed'));
    const titleHit = matchEntry(pool[1], parseQuery('garden'));

    expect(bodyHit?.snippet?.text).toContain('pushed');
    expect(titleHit?.snippet).toBeNull();
    expect(titleHit?.titleRanges).toEqual([[0, 6]]);
  });

  it('collapses the whitespace of the snippet it cuts', () => {
    const wordy = seed({
      id: 's-wrap01',
      title: 'nothing',
      body: `a paragraph\n\n    that wraps around the\n    word beacon and keeps going`,
    });
    const match = matchEntry(index([wordy])[0], parseQuery('beacon'));

    expect(match?.snippet?.text).not.toMatch(/\s\s/);
    expect(match?.snippet?.text).toContain('word beacon and');
  });

  it('finds nothing at all when the query names a filter that does not exist', () => {
    expect(titles(seeds, 'socket is:done')).toEqual([]);
  });
});

describe('splitRanges', () => {
  it('splits a string into its hit and non-hit runs, in order', () => {
    expect(splitRanges('reconnect the socket', [[10, 13]])).toEqual([
      { text: 'reconnect ', hit: false },
      { text: 'the', hit: true },
      { text: ' socket', hit: false },
    ]);
  });

  it('merges overlapping hits so a highlight never doubles up', () => {
    const match = matchEntry(index([seed({ id: 's-ovl001', title: 'searching search' })])[0], parseQuery('sea search'));
    expect(match?.titleRanges).toEqual([
      [0, 6],
      [10, 16],
    ]);
  });

  it('returns the whole string when nothing matched', () => {
    expect(splitRanges('untouched', [])).toEqual([{ text: 'untouched', hit: false }]);
  });
});
