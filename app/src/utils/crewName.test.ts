import { describe, it, expect } from 'vitest';
import { crewDisplayName, crewHolderName } from './crewName';

describe('crewDisplayName', () => {
  it('writes a member id as a name', () => {
    expect(crewDisplayName('trellis')).toBe('Trellis');
    expect(crewDisplayName('keel')).toBe('Keel');
    expect(crewDisplayName('alder')).toBe('Alder');
  });

  it('capitalizes the first character only — a hyphenated id is one name', () => {
    expect(crewDisplayName('mary-jane')).toBe('Mary-jane');
    expect(crewDisplayName('a')).toBe('A');
  });

  it('has nothing to say about an empty name, and leaves a capital alone', () => {
    expect(crewDisplayName('')).toBe('');
    expect(crewDisplayName('   ')).toBe('');
    expect(crewDisplayName('Trellis')).toBe('Trellis');
  });

  it('matches the daemon rule on a non-ASCII first letter', () => {
    expect(crewDisplayName('ólafur')).toBe('Ólafur');
  });
});

describe('crewHolderName', () => {
  it('writes a member as a name and leaves a session id alone', () => {
    expect(crewHolderName('trellis', 'sess-a')).toBe('Trellis');
    expect(crewHolderName('', 'sess-a')).toBe('sess-a');
    expect(crewHolderName(undefined, 'sess-a')).toBe('sess-a');
  });

  it('has nothing to say when nobody holds it', () => {
    expect(crewHolderName(undefined, undefined)).toBe('');
    expect(crewHolderName('  ', '  ')).toBe('');
  });
});
