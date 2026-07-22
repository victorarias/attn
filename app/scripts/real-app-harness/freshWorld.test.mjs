import { describe, expect, it } from 'vitest';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';

describe('assertFreshWorldTargetSafe', () => {
  it('throws when profile is empty (the prod profile string)', () => {
    // profile: '' is both "falsy" and *literally* the production profile —
    // a missing guard here would let the matrix scrub the production daemon.
    expect(() => assertFreshWorldTargetSafe({ profile: '', appPath: '/Users/victor/Applications/attn-dev.app' }))
      .toThrow(/profile/i);
  });

  it("throws when profile is 'default' even with a non-prod appPath", () => {
    // isProductionHarnessTarget only special-cases profile === '' — a guard
    // that forwarded the raw 'default' string to it unnormalized would let
    // this call through (appPath here is a harmless named-profile bundle) and
    // scrub the production daemon on the next call. Pin the profile-only path.
    expect(() => assertFreshWorldTargetSafe({ profile: 'default', appPath: '/Users/victor/Applications/attn-fxm1.app' }))
      .toThrow(/default/i);
  });

  it('throws when appPath is missing', () => {
    // A missing appPath means the preflight cannot key its process matching
    // on anything — without this guard it could fall through to a bare
    // "pty-worker" match and kill unrelated processes.
    expect(() => assertFreshWorldTargetSafe({ profile: 'fxm1', appPath: '' }))
      .toThrow(/appPath/i);
    expect(() => assertFreshWorldTargetSafe({ profile: 'fxm1' }))
      .toThrow(/appPath/i);
  });

  it('throws for a production-shaped target on a named profile (defense in depth)', () => {
    // A named profile pointed at the real prod app path/bundle is exactly the
    // case isProductionHarnessTarget's defense-in-depth checks exist for; a
    // swapped condition here would let it slip through as "safe".
    expect(() => assertFreshWorldTargetSafe({ profile: 'fxm1', appPath: '/Users/victor/Applications/attn.app' }))
      .toThrow();
  });

  it('does not throw for a realistic named-profile target', () => {
    // The common case: a throwaway named profile pointed at its own isolated
    // app bundle must be allowed through, or the preflight can never run.
    expect(() => assertFreshWorldTargetSafe({ profile: 'fxm1', appPath: '/Users/victor/Applications/attn-fxm1.app' }))
      .not.toThrow();
  });

  it("does not throw for the 'dev' profile", () => {
    // dev is the harness's default safe target — a regression that flagged
    // it as production would break every ordinary matrix run.
    expect(() => assertFreshWorldTargetSafe({ profile: 'dev', appPath: '/Users/victor/Applications/attn-dev.app' }))
      .not.toThrow();
  });
});
