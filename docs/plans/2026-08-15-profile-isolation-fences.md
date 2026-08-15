# Plan: Profile isolation fences and legibility

## Goal

Make a non-default profile unmistakable in the app and incapable of reaching the default profile through inherited routing, copied crew records, global skill synchronization, or shared remote caches.

## Architecture Map

```text
App bundle startup
  profile::apply_build_profile_env
    scrub every path override, including default/prod bundles
  Sidebar
    render build profile only when non-default

Session spawn
  daemon spawn plan
    PTY manager / conversation host
      merge daemon env <- login shell <- plugin env
      re-apply daemon profile + exact data/db/socket/config/plugin paths last

Crew path use
  imported/stored Member
    validate HomeDir + CharterPath under <dataRoot>/crew
    validate cwd + awareness dirs are not in another ~/.attn*/crew
      wake / priming / handoff / turnover

User-global side effects
  skill synchronization
    default profile -> install/prune
    named profile -> skip and log
  remote artifact cache
    <active dataRoot>/remotes
```

## Boundaries

- The Tauri build profile owns app routing. Runtime parent environment cannot redirect a bundle.
- The daemon profile and exact socket own every child session's routing. Login-shell and plugin environment remain inputs, never authorities for these keys.
- The daemon validates stored crew paths immediately before filesystem or launch use. Project cwd and awareness directories remain unrestricted except for another profile's crew homes.
- User-global skill directories belong to the default profile. Remote artifacts are ordinary profile data.
- Harness crew identities are visibly synthetic and explicitly pinned to `claude-haiku-4-5` unless a scenario documents why it needs a stronger model.

## Implementation Steps

- [x] Add the fixture naming/model rules and correct profile-aware crew CLI prose.
- [x] Show a quiet persistent profile marker in non-default app sidebars; leave default rendering unchanged.
- [x] Scrub all app-shell routing overrides in every bundle.
- [x] Pin profile and socket after every PTY/host environment merge, with conflicting-login-shell tests.
- [x] Fence member filesystem paths and foreign-profile crew work directories at import and each use.
- [x] Skip global skill synchronization outside the default profile and move remote caches under `config.DataDir()`.
- [x] Add focused Go, Rust, and frontend tests; run `make test-quick` and the frontend suite.
- [x] Install an isolated profile, run preflight, prove the marker/profile env/synthetic crew behavior live, and record visible evidence.
- [x] Confirm the branch is based on current main before live verification.
- [ ] Commit, push, and open a ready PR without merging.

## Decisions

- Inject the complete routing block, not only `ATTN_PROFILE`. Direct CLI commands can open the database or plugin tree without using the socket, so every daemon-owned path must win over login-shell and plugin environment.
- Resolve paths through existing ancestors before containment checks so symlinked roots work and symlink escapes do not.
- Keep project directories open. Only another `~/.attn*/crew` tree is forbidden for cwd and awareness.
- Treat an invalid stored member path as a named operation refusal, not a silently omitted member or an automatic rewrite of copied state.
