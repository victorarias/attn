# The communication gateway — superseded

> **Superseded by `docs/plans/2026-06-26-work-tracker.md`.**
>
> This doc designed agent↔chief communication as a standalone *delivery gateway*.
> The design then pivoted: the chief delegating and tracking work **is a work
> tracker**, and a "delivery" is really two things on a **ticket** — a **status change**
> (the ticket moves column) and an **activity comment**. The gateway's settled mechanics
> — content dedup, bundle-by-sender, ack-on-output / unread, the hardcoded chief id,
> and the two-consumer (Claude `watch` / codex nudge) contract — are **kept**, re-homed
> as the work tracker's **activity + notification layer**.
>
> Read the work-tracker doc for the current model. The one substantive change: cards
> are a durable backlog, so the flat 30-day TTL no longer applies to open items — it
> moves to unread/notification state and closed-card archival.
