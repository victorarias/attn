# Plan: Automation session provenance

## Goal

An automation-launched agent must know its exact target before it starts, and Victor must be able to distinguish automation sessions without opening each one. GitHub PR provenance should remain visible in Home, the sidebar, the active pane, tickets, and automation run history.

## Architecture Map

```text
Current:
GitHub review observation
  -> automation occurrence payload
  -> automation delivery
     -> repository-basename session label
     -> configured prompt + occurrence-file path
  -> session/ticket/run UI renders unrelated strings

Target:
GitHub review observation
  -> validated PullRequestInput
  -> automation provenance builder
     -> trusted target block in launch prompt
     -> latest run + occurrence + definition store join
        -> Session.automation / Ticket.automation / AutomationRunSummary.automation
           -> AutomationProvenance UI
              -> pane header, Home, sidebar, ticket detail, run history
```

## Data Model / Interfaces

The daemon derives this read model from existing automation tables. It is not persisted on sessions or tickets.

```ts
type AutomationProvenance = {
  run_id: string
  definition_id: string
  definition_name: string
  trigger_type: string
  pull_request?: {
    repository: string       // host/owner/repository
    number: number
    url: string
    title?: string           // rendered as data, never prompt instructions
    head_sha: string
  }
}
```

The optional provenance appears on `Session`, full `Ticket`, board `TicketRow`, and `AutomationRunSummary`. Ordinary sessions, tickets, and non-PR automation runs omit it.

## Boundaries

- `internal/automation` validates provider input and formats the trusted PR identity.
- `internal/store` joins definitions, runs, and occurrences in bulk; it does not format UI labels.
- `internal/daemon` decorates wire snapshots and builds the agent target block.
- The frontend renders one compact provenance vocabulary at different densities. It does not parse occurrence keys or payload JSON.
- PR title/body remain untrusted provider data. Only validated repository, number, URL, and SHA enter the instruction block.

## Implementation Steps

- [x] Add the provenance protocol model, generate Go/TypeScript types, and bump the protocol version.
- [x] Add store queries that resolve the latest provenance for session, ticket, and run surfaces.
- [x] Inject an authoritative PR target block into automation launch and continuation prompts while preserving payload separation.
- [x] Decorate session, ticket, and automation-run wire shapes with provenance.
- [x] Render compact provenance in the pane header, Home, sidebar, ticket board/detail, and automation run history.
- [x] Add daemon/store/frontend tests, including paired review sessions and ordinary-surface fallbacks.
- [x] Run focused tests and live verification in an isolated profile.

## Decisions

- Keep session identity stable at repository + PR number; SHA is detail that can change during continuity.
- Keep the current two automation definitions unchanged. Platform injection resolves their ambiguous “this pull request” wording without rotating continuity contracts.
- Derive provenance from existing rows instead of adding session or ticket columns. Indexed session/ticket lookups keep frequent single-entity broadcasts off a retained-history scan.
- Render PR title in UI only. Injecting provider-authored title into the agent instruction block would weaken the existing untrusted-payload boundary.

## Follow-ups

- The separate push-continuity work should reuse the same provenance formatter for later-head ticket activity and prompt nudges.
