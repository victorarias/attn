---
title: a wall of prose
status: fixture
---

# The subsystem lifecycle coordination approach

There is a consideration that must be given to the fact that the subsystem
lifecycle coordination layer configuration state machine transition handler
performs an evaluation of every candidate before the point at which any
determination about eventual disposition can be arrived at, and it is the case
that this evaluation, which is performed once per candidate and which may be
repeated in the event that a subsequent revision of the underlying record is
observed by the watcher that is registered against the store at the moment the
coordination layer is brought up, must be understood as being separate from the
disposition itself, because the disposition is a thing that is decided later by
a different component entirely, which means that any reader attempting to
follow the flow of control through this paragraph will have lost the thread
long before reaching the point where it is finally explained that none of this
happens at all when the feature flag is off.

There are three cases that should be considered. It is important that the
determination be made about which of them applies. The configuration must be
validated before the operation is undertaken.
