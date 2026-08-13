# Desktops: untangling the workspace

Conversation capture, 2026-08-13 — direction with early rulings, not
yet a plan. Written so the eventual plan starts from what Victor ruled,
not from archaeology.

## The doubt

The workspace is one word doing five jobs:

```
workspace ─┬─ launch default    (directory sessions open in)
           ├─ sidebar grouping  (sessions listed under it)
           ├─ the screen        (layout panes; switching workspace
           │                     swaps the whole visible tile set)
           ├─ shared context    (the per-workspace context.md overlay)
           └─ scope stamp       (was: garden seeds; annotation keys)
```

Victor is not in love with two of them: workspace-as-screen (being in
the same workspace should not force sessions to share the screen, and
sessions from different workspaces should be able to) and the workspace
context. Grouping itself makes sense. The wish: freely organize what
shows together, independent of where sessions belong.

## The desktop

A **desktop** is a composition surface: created and removed freely,
holding whatever panes were dragged into it — agents with their
terminals, from any group. A session's group says where it *lives*; its
desktop says where it *shows*. macOS Spaces for agents.

Migration is incremental, not big-bang: day one, every workspace
projects a default desktop and behavior is identical to today; free
desktops grow beside them and the projected ones stop being special.
The drag mechanics have a donor (the drag-to-workspace drop zones and
rank keys).

If the workspace stops being the unit agents cohabit, the workspace
context loses its host. Its content would split along lines that
already exist — direction and decisions to garden crowns (plans live in
the garden), durable knowledge to the Notebook. Not ruled; consistent
with the doubt.

## Ruled (2026-08-13)

1. **One pane, one desktop.** A pane is never on two desktops; drag
   moves it. (Also sidesteps PTY geometry — one active client owns the
   grid, so one placement means one size that wins.)
2. **Sidebar, queue on.** The cross-desktop queue keeps top priority.
   Below the queue, below pinned agents, desktop names render as
   clickable rows. Vertical space is precious: leverage the sidebar we
   have, no new chrome.
3. **Attention navigation keeps its feel.** Following the next session
   switches to that session in its desktop. That does not change.

## Open

- **Sidebar with queue off.** Undecided; Victor accepts proposals.
  Standing proposal: desktops replace workspaces as the sidebar's
  sections — sessions listed under the desktop holding them, groups
  demoted to labels — so the structure and the vertical budget mirror
  today's sidebar exactly. Whatever wins must protect keyboard flow.
- **Where crew members sit** relative to pinned agents and desktop rows
  (the crew plan keeps members always visible in the sidebar).
- **Workspace context's fate**, per above.
- **What remains of the workspace** once screen and scope are gone:
  launch default + grouping label + status rollup, or less.
