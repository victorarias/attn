# Plan: Continue automated PR reviews after pushes

## Goal

An enabled `github_review_requested` automation starts once when Victor's review
is requested, then sends each later relevant PR head to the same automation
ticket/session. Each automation definition remains an independent reviewer.

## Recommendation

Make the PR head part of the durable occurrence identity, without adding a new
poller or database column:

```text
review_requested:<host/owner/repo#number>:<request-cycle>:<head-sha>
```

The normal PR poll already carries the last detail-refreshed `HeadSHA`. A focused
PR GET remains the authority before claim, so pushes that race or bunch together
collapse to the newest provider snapshot.

This detects a push on the next normal list poll after detail refresh has learned
the SHA. It deliberately adds no faster timer or provider call.

Keep the existing worktree ownership rule: once the reviewer owns a worktree,
attn fetches the new exact SHA but does not change its checkout. The reviewer may
have commits, a branch switch, dirty notes, or an in-flight command; advancing
HEAD underneath it would destroy evidence or race the agent. The durable ticket
event points at the new immutable occurrence input, and the fetched commit is
available for the reviewer to diff or adopt deliberately.

## Current and proposed flow

```mermaid
sequenceDiagram
    participant Poll as doPRPoll<br/>daemon.go
    participant Observe as observeGitHubReviewRequests<br/>automations_github.go
    participant Store as automation store<br/>automations.go
    participant GH as GitHub PR GET
    participant Deliver as deliverAutomationRun<br/>automations_deliver.go
    participant Git as repository/worktree
    participant Reviewer as existing ticket/session

    Poll->>Poll: FetchAll + SetPRs preserves refreshed HeadSHA
    Poll->>Observe: current review-request demand
    Observe->>Store: reconcile durable edge/cycle
    Note over Store: Current: one occurrence per request cycle
    Store-->>Observe: candidate only for new/re-requested edge or pending retry
    Observe->>GH: focused immutable PR snapshot
    Observe->>Store: claim occurrence
    Store->>Deliver: pending run
    Note over Deliver: Current: changed origin/current HeadSHA is refused

    rect rgb(225, 245, 235)
      Note over Observe,Store: Proposed: observed HeadSHA participates in candidacy
      Note over Store: occurrence identity = cycle + authoritative head SHA
      Store-->>Deliver: later head reuses definition+subject binding
      Deliver->>Git: fetch exact SHA; preserve owned checkout
      Deliver->>Reviewer: append occurrence event + ordinary ticket doorbell
    end
```

Current-code receipts:

- [`doPRPoll`](../../internal/daemon/daemon.go#L3749) feeds the same successful
  `FetchAll` result to automation observation; [`Store.SetPRs`](../../internal/store/store.go#L1152)
  preserves the detail-refreshed `HeadSHA` on those PR objects.
- [`observeGitHubReviewRequests`](../../internal/daemon/automations_github.go#L96)
  consumes that snapshot, then performs one focused `FetchPullRequestSnapshot`
  only for a candidate.
- [`ReconcileAutomationReviewRequests`](../../internal/store/automations.go#L743)
  owns durable edge/cycle state; [`ClaimGitHubReviewAutomationRun`](../../internal/store/automations.go#L965)
  currently keys the occurrence only by subject and request cycle.
- [`validateAutomationContinuation`](../../internal/daemon/automations_deliver.go#L256)
  and [`prepareAutomationLocation`](../../internal/daemon/automations_deliver.go#L417)
  both reject a changed head today.
- `prepareAutomationLocation` already fetches the exact snapshotted SHA before
  [`EnsureAutomationSessionWorktree`](../../internal/git/worktree.go#L80)
  adopts a persisted worktree without changing its checkout.
- [`EnsureAutomationContinuationTicket`](../../internal/store/automations.go#L1048)
  appends one durable event with the occurrence input path;
  [`notifyTicketObservers`](../../internal/daemon/ticket_notify.go#L85) wakes the
  existing reviewer through the ordinary inbox/doorbell path.

## Ownership and state

```diff
 doPRPoll / doRefreshPRsWithResult
   FetchAll
   Store.SetPRs                    # retains last detail-refreshed HeadSHA
   observeGitHubReviewRequests
-    reconcile(definition, host, []subject)
+    reconcile(definition, host, []{subject, observedHeadSHA})
       durable review-request edge/cycle
-      candidate = no run for cycle | pending run
+      candidate = no run | pending retry | latest terminal head differs
     FetchPullRequestSnapshot      # authoritative open/draft/head check
-    claim occurrence(subject, cycle)
+    claim occurrence(subject, cycle, authoritative head)
       reuse binding(definition, subject)
     deliverAutomationRun
       validate same prompt/launch/location contract
-      reject originHead != currentHead
       EnsurePullRequestRevision(currentHead)
       preserve persisted worktree checkout
       append occurrence event to existing ticket
       notify/resume the same reviewer session
```

No schema or protocol migration is needed. Existing occurrence payloads already
store `head_sha`; reconciliation can recognize legacy cycle-only occurrence keys,
while new claims use cycle plus SHA. The existing unique occurrence constraint and
store transaction provide the idempotency fence.

## Behavior at the edges

| Case | Result |
|---|---|
| Same head observed repeatedly | No new run. |
| Several pushes before the focused GET | One occurrence for the newest GET result. |
| Older run is still pending | Retry that immutable run first; a later refresh catches up once, to the then-latest head. No overtaking. |
| Reviewer session is live | Append ticket activity and nudge it; never spawn a second session. |
| Reviewer session stopped with a valid transcript | Existing resume safety restarts the same logical session. |
| Owned worktree is dirty, committed, or on another branch | Fetch the new SHA, leave checkout and evidence untouched. |
| Request withdrawn, PR approved/closed, or absent from current demand | Deactivate the edge; cancel only undelivered work as today. No push continuation. |
| Focused snapshot is draft or closed | Ignore it before claim. |
| Two definitions watch the PR | Each gets its own occurrence and continuity binding; neither coalesces the other. |
| Daemon restart/retry | Durable edge, occurrence uniqueness, pending retry, and binding reuse converge without a duplicate. |

The pending-run choice is deliberate. Superseding an incompletely materialized run
would need a new cancellation state plus artifact/session cleanup semantics. Retrying
the immutable predecessor preserves today's recovery contract; `latest` still
coalesces all heads that arrive before the next accepted focused snapshot.

## Implementation

- [ ] Extend GitHub review observations/candidates with the refreshed head SHA.
- [ ] Reconcile the active cycle against its latest occurrence payload, including
      legacy cycle-only keys; make new occurrence keys SHA-specific.
- [ ] Keep claim transactional and idempotent for `(definition, subject, cycle, SHA)`.
- [ ] Permit changed-head continuity while retaining definition-contract, ticket,
      repository identity, transcript-resume, and unattended-launch checks.
- [ ] Keep the existing fetch-exact-SHA / preserve-owned-checkout boundary.
- [ ] Add a changelog fragment.
- [ ] Run focused store, daemon, and git tests; then live-verify both configured
      reviewer shapes in one isolated profile against a mock PR head change,
      including a second head and dirty reviewer evidence. Do not mutate GitHub.

## Focused tests

- Store: first head claims; duplicate head dedupes; a changed head in the same
  active cycle becomes one candidate/run; withdrawal/re-request increments the
  cycle; pending retry cannot be overtaken; two definitions remain independent;
  legacy occurrence keys do not replay.
- Observer: refreshed `HeadSHA` triggers one focused GET and one continuation;
  same-head polls do neither; a newer focused snapshot wins over a stale observed
  SHA; approved/withdrawn/draft/closed inputs do not continue.
- Delivery/git: changed-head continuation passes the contract gate, reuses stable
  IDs, fetches the exact commit, leaves dirty files/branch/HEAD untouched, records
  the new occurrence path, and nudges or resumes the existing reviewer safely.
- Recovery: restart with a pending GitHub run retries the immutable occurrence;
  the next provider observation catches up to only the latest head.

## Approval checkpoint

Awaiting Victor's approval of the recommendation, especially the worktree rule:
fetch the new commit but never move an agent-owned checkout automatically.
