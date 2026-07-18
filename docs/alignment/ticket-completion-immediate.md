# Ticket completion immediate delivery

## Why

A ticket moving to `done` is a handoff boundary: the chief or another subscribed
observer needs to know that the assigned work has finished so the next owner can be
dispatched. This chunk is done when that event enters the existing immediate attention
path without weakening PR 594's batching for ordinary activity.

## Aligned on

- A `status_changed` event whose destination is the canonical `done` status bypasses
  the observer's 30-minute buffer. It still uses the normal short, visible nudge
  countdown and the same watch/nudge delivery arbitration.
- The immediate path consumes the observer's whole unread queue across tickets, so
  every pending buffered update piggybacks with full ticket and event provenance.
- `working`, `blocked`, `in_review`, comments, and attachments remain buffered for a
  non-assignee observer. Other terminal outcomes retain their existing semantics.
- Event rows and per-observer cursors remain the durable batch and acknowledgement;
  no new delivery state or protocol shape is introduced.

## In scope / deferred

This chunk changes only ticket notification eligibility, focused tests, and the
user-facing changelog wording. New urgency levels, settings, notification channels,
or broader terminal-status policy are deferred.
