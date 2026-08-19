# Plan: Baseline requested reviews on activation

## Goal

A newly created, resurrected, or re-enabled `github_review_requested`
automation leaves the existing outstanding-review backlog alone. After its first
successful observation of each GitHub host, later review-request cycles launch as
they do today. Ordinary daemon downtime still catches up the latest active demand.

## Architecture Map

```diff
 apply / enable definition
   persist enabled definition
   fence observations started before activation
+  reset host observation cursors and deactivate prior edges

 first successful observation for host
   reconcile complete current review-request set
-  return every active cycle without a run
+  mark each current edge's cycle as the activation baseline
+  advance the host cursor and return no candidates

 later observation
   unchanged baseline edge -> silent
   absent edge -> deactivate
   inactive -> active -> increment cycle and launch
   daemon restart with unclaimed later cycle -> retry/catch up
```

## Data Model

`automation_review_request_edges.baseline_cycle` records the most recent cycle
that activation deliberately ignored. A cycle is eligible only when
`cycle > baseline_cycle`.

The existing provider cursors retain two separate jobs:

- scope `*` fences observations whose fetch began before apply/enable;
- each host scope says that host has completed its activation baseline.

Creation has no host cursors. Resurrection and disabled-to-enabled transitions
remove host cursors while preserving edge cycle history. Each host therefore
baselines independently on its first successful post-activation snapshot.

## Boundaries

- `internal/store/automations.go` owns activation reset, baseline reconciliation,
  cycle eligibility, and claim race checks.
- `internal/daemon/automations_github.go` continues to consume candidates and
  fetch immutable PR input; baseline observations produce no candidate and no
  focused PR request.
- GitHub `latest` catch-up remains the downtime policy. Activation baseline is a
  separate lifecycle rule, not a new configurable policy.
- No protocol or frontend shape changes are needed. `enabled` remains the config
  state; a host with no successful post-activation observation remains unarmed.

## Implementation Steps

- [x] Add and migrate the durable baseline cycle.
- [x] Reset host cursors on true activation without re-arming ordinary live edits.
- [x] Baseline each host's first successful snapshot and enforce eligibility at
      reconciliation, recheck, and claim.
- [x] Preserve review-edge cycle history across soft delete/resurrection.
- [x] Update store, daemon, migration, and policy regression tests.
- [x] Add a changelog fragment and run focused/full automated verification.
- [x] Live-verify quiet creation and re-enable baselines in an isolated profile;
      cover the later request cycle with the provider integration tests.

## Decisions

- Activation means the first successful complete snapshot for each host. A
  request present in that snapshot is existing backlog and is ignored.
- Ordinary enabled-definition edits only fence stale in-flight snapshots. They
  do not establish a new baseline or swallow newly observed demand.
- Soft delete deactivates edges instead of deleting their cycle history, so
  resurrection cannot reuse an old occurrence key.
- Repository filter expansion during a live edit keeps current behavior; defining
  edit-time catch-up semantics is outside this activation change.
