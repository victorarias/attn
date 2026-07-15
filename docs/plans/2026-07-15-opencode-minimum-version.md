# Plan: Use a minimum OpenCode version

## Why / Alignment

OpenCode upgrades should not require an attn release merely because a stable
version is absent from an exact allowlist. This chunk replaces the allowlist
with a stable `>= 1.17.16` policy while retaining exact server identity for each
run and preventing resume downgrades.

We are aligned on the existing canonical design: newer stable releases are
attempted against the real server contract, prerelease and malformed versions
remain unsupported, and concrete endpoint failures surface through degraded
plugin health. The other OpenCode roadmap plans are deferred to their own PRs.

## Runtime Map

```text
opencode --version
  -> stable semver parser
    -> older than 1.17.16 / invalid / prerelease: unhealthy, no driver
    -> 1.17.16 or newer: register driver

resume
  -> current version must be at least the persisted version
  -> launched server must exactly match this run's current version
  -> verify native identity and persisted pins
```

## Implementation Steps

- [x] Replace the exact allowlist with strict stable-version parsing and
  comparison around a `1.17.16` minimum.
- [x] Distinguish unsupported-old and invalid/prerelease availability failures.
- [x] Preserve exact health-version equality for each launched run.
- [x] Allow same-version and forward-version resume while rejecting downgrade.
- [x] Persist the current version on a successfully upgraded run.
- [x] Update compatibility documentation and the changelog.
- [x] Complete automated and isolated live-app verification.
- [ ] Open the PR, address Figgyster's review, and merge after approval.

## Decisions

- Stable releases have no maximum version; runtime APIs remain the contract
  probe.
- An optional leading `v` is normalized, but prerelease/build suffixes and
  non-canonical numeric components are rejected.
- Exact equality remains at the per-run server boundary because it proves the
  adapter reached the process it launched.
