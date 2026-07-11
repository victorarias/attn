# Ticket attach rename

## Why

`attach` names the mechanical operation more accurately: copy one or more files into a ticket's canonical artifact directory and record that action. `handover` remains useful language for the broader agent workflow, but should not be the command or protocol vocabulary.

This chunk is done when the current durable artifact operation is consistently named `attach` across the CLI, protocol, daemon, store, UI, tests, changelog, and agent guidance without changing its behavior.

## Aligned on

- Rename the operation to `ticket attach` throughout the active product surface.
- Preserve multi-file attachment, optional state and comment updates, retry-safe receipts, collision handling, and filesystem-canonical artifact listing.
- Keep “handover” only where prose describes the broader transfer-of-work workflow.
- Make this a clean rename with no compatibility alias for `ticket handover`.

## In scope / deferred

This chunk includes the product vocabulary rename, generated protocol types, tests, and live verification in a non-production profile. Changes to artifact formats, allowed file types, or ticket workflow semantics are deferred.

## Vision

This sharpens the artifact mechanics supporting [chief delegation awareness](../vision/chief-delegation-awareness.md) while preserving its durable continuation model.
