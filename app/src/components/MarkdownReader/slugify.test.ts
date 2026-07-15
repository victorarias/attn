import { describe, expect, it } from 'vitest';
import { createSlugger, slugifyHeading } from './slugify';

describe('slugifyHeading', () => {
  it('lowercases and hyphenates word runs', () => {
    expect(slugifyHeading('Getting Started Guide')).toBe('getting-started-guide');
  });

  it('collapses punctuation runs into single hyphens and trims ends', () => {
    expect(slugifyHeading('  What?! — Really...  ')).toBe('what-really');
  });

  it('preserves unicode letters and numbers', () => {
    expect(slugifyHeading('Configuração 2 do ambiente')).toBe('configuração-2-do-ambiente');
    expect(slugifyHeading('日本語 見出し')).toBe('日本語-見出し');
  });

  it('unwraps wiki links and markdown links', () => {
    expect(slugifyHeading('See [[Setup Notes]]')).toBe('see-setup-notes');
    expect(slugifyHeading('Read [the guide](https://example.test/g)')).toBe('read-the-guide');
  });

  it('strips emphasis and code markers', () => {
    expect(slugifyHeading('**Bold** `code` _em_ ~strike~')).toBe('bold-code-em-strike');
  });

  it('returns an empty slug for symbol-only headings', () => {
    expect(slugifyHeading('!!! ???')).toBe('');
  });
});

describe('createSlugger', () => {
  it('keeps the first occurrence bare and suffixes later duplicates', () => {
    const slug = createSlugger();
    expect(slug('Setup')).toBe('setup');
    expect(slug('Setup')).toBe('setup-1');
    expect(slug('Setup')).toBe('setup-2');
    expect(slug('Other')).toBe('other');
  });

  it('yields no id for empty slugs', () => {
    const slug = createSlugger();
    expect(slug('***')).toBeUndefined();
    expect(slug('***')).toBeUndefined();
  });

  it('is independent per document (fresh slugger has a fresh map)', () => {
    expect(createSlugger()('Setup')).toBe('setup');
    expect(createSlugger()('Setup')).toBe('setup');
  });
});
