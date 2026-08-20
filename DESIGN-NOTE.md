# A kanban board for the garden — design note

Prototype, on branch `delegate/proto-kanban-7ebfbf30`, seed `s-xp0zwj`.
Installed for clicking as profile `kanban`
(`/Users/victor/Applications/attn-kanban.app`, port 22696). Nothing here is
merge-ready and none of it is tested; it exists to be judged.

## What it is

A second view over the same garden the list already shows. The list answers
"what is here". The board answers "how is it moving", without opening
anything.

Four columns, all of them computed from the seed the daemon already pushed:

| column  | who is in it                                     |
| ------- | ------------------------------------------------ |
| Ready   | everything open that nobody holds                |
| Growing | tended — the card names the tender and says if that session is gone |
| Parked  | dormant                                          |
| Closed  | harvested + withered, counted and collapsed      |

Nothing is stored. There is no board state, no ordering, no column field on a
seed. Drag a card and the daemon performs a real `attn seed` verb; the card
lands in its new column because the projection now reads differently. The board
cannot disagree with `attn seed ready` because it is the same answer, laid out
sideways.

## What I changed my mind about while building it

**Ready is not "ready".** The ruled model says Ready is "computed ready,
unclaimed". Taken literally that hides every open seed that is blocked, gated,
or a packet — in the fixture garden that was 4 of 6 cards, invisible. A board
that drops work is worse than a long column, so Ready holds everything open
that nobody holds, sorted ready-first, with a counted hairline where the two
halves meet: `4 not ready yet`. The column header count stays the *truly ready*
count, so it still matches `attn seed ready`. Two numbers, both true, one line
of type apart.

**Drag is pointer events, not HTML5 drag-and-drop.** WebKit's dnd hands the
gesture to a native drag session, which draws its own bitmap snapshot of the
card: the thing under the cursor is a screenshot with a shadow, not a designed
object, and nothing about it is ours. It is also undriveable by anything but a
human hand — CGEvents cannot feed a native drag session, so the harness could
never walk this path either. The pointer implementation is ~40 lines, gives us
the small chip the hand carries, and is exactly the same code path a person and
a test walk. If the board ships, keep this.

**WebKit does not focus a button you click.** The board's keys were originally
read from a handler on its own element, which went deaf the instant the mouse
touched a card — clicking selected a card and then the arrows did nothing. The
board now reads keys from the document while it is up and steps aside for
anything being typed into, and a press moves the DOM focus to what was pressed.
Any keyboard-first surface in this app has the same trap waiting.

## The drag-zone model, and what it taught me about the garden

Dragging a card over a column splits that column into one labelled zone per verb
that the garden would actually accept from the state the card is in. Zones are
built from the same table the verb menu uses, so the drag path and the key path
cannot disagree, and a pair with no legal verb grows no zone at all — the drop
bounces. Dropping opens a one-line composer in the target column asking for the
note the CLI would demand.

Building that meant enumerating every legal pair, and three holes fell out:

1. **There is no `unpark`.** The ruled model asks for "from Parked to Ready —
   one zone, Unpark". `internal/garden/lifecycle.go` has no dormant→planted
   move. The only way out of Parked today is to tend it, harvest it, or wither
   it. So the board grows no zone there, and Ready's only zone is `Replant`,
   offered to a card dragged out of Closed. This is a real gap in the garden,
   not a prototype shortcut: parking is a one-way door, and the way in shipped
   without the way out. Worth its own seed regardless of what happens to this
   board.

2. **`park` and `replant` refuse a reason.** The daemon rejects a reason on
   those two and tells the caller to write a note instead. The composer honours
   that literally: it says `goes on the log` under the field, writes the
   sentence with `seed_note`, and performs the move with no reason. Same box,
   two destinations, and the box says which.

3. **Nothing releases a claim.** Growing → Ready has no verb either: a tender
   can only finish, park, or hold. That is why the Growing column's only zone is
   `Dispatch an agent…` — an intent, not a state change (it opens a stub sheet
   in this prototype). It is also why a Growing card whose session is gone reads
   `session gone` in the accent colour and can still only be closed or parked.
   If dead-tender recovery is meant to be a one-gesture thing, it needs a verb
   that does not exist yet.

There is a fourth, smaller one. A board-issued move has **no actor**. Every CLI
verb carries the session that ran it; a drag carries a hand. `seed_transition`
over the WebSocket passes an empty tender, which is correct for the four verbs
the board offers (none of them claims anything) and is exactly why `tend` is not
reachable from a drag — a claim with nobody in it would be a lie. If the board
ever grows a "take this" gesture, that is the question to answer first.

## Placement

The prototype puts a `list | board` switch in the garden surface's header, and
the board is the default so it opens on the thing being judged. That is a
prototype convenience, not a proposal.

The ruled placement — the app's old board button becomes the garden board, the
panel/sidebar stays the list — is a build-time decision I deliberately did not
make here, because it decides something this prototype cannot answer: whether
the two views are *one surface with a switch* or *two destinations*. If they are
two destinations, the switch should not exist and the trail should be shared
state, so drilling into a plot in one and switching to the other keeps your
place. If they are one surface, the switch belongs where it is and the default
should be the list. I lean toward two destinations with shared trail state, but
that is a guess about how you'd use it, not a finding.

## What I left out

- **No tests.** Prototype.
- **No plot swimlanes.** Crowns are cards with a per-state count badge and a
  `›`; clicking one re-boards its children and grows the trail, recursively,
  exactly like the list. That was the ruling and it held up — a plot reads as
  one row of progress until you want its inside.
- **No board-side ordering, no WIP limits, no filters.** The garden's own order
  (ready-first oldest-first in Ready, newest-touched-first elsewhere) was enough
  to read at a glance, and anything else would be board state.
- **The dispatch sheet is a stub.** It names the seed and what would be
  dispatched and stops.
- **Closed is capped by the garden's own page**, not by the board. If the daemon
  pushes a truncated garden, the board says so in one line at the bottom rather
  than silently showing fewer cards.

## Collision points with the three sibling prototypes

Kept deliberately small, because drill, search, and expand are all in flight on
`GardenPanel`:

- **`GardenPanel.tsx` — 3 lines.** One optional `viewToggle?: ReactNode` prop,
  rendered as the first child of `.garden-panel__header-actions`. This is the
  only edit to the list view and the only place a merge can conflict.
- **`GardenSurface.tsx` — rewritten** to own the `list | board` state and pick a
  view. Whoever lands the placement decision replaces this file anyway.
- **`App.tsx` — 14 lines**, wiring `sendSeedTransition` / `sendSeedNote` and a
  memoised set of live session ids (how a card knows its tender walked away).
- **Protocol — additive.** `seed_transition` and `seed_note` gained an optional
  `request_id`, and two result events were added so a WebSocket caller can be
  told whether its move worked; `ProtocolVersion` 262 → 263. The unix-socket
  callers are untouched. If two prototypes both bump the version, the later
  rebase takes the higher number — that field is the usual three-way lockstep
  (`constants.go`, `main.tsp`, `useDaemonSocket.ts`).
- **New files, no conflicts:** `app/src/components/GardenBoard.{tsx,css}`,
  `internal/daemon/garden_board.go`.

## Verified live

On profile `kanban`, against a garden seeded with three plots, loose seeds,
closed work, and one seed tended by a session that no longer exists:

- top-level board → drill into a crown → trail;
- drag to Closed → two zones → drop on Wither → note → the real transition
  (`attn seed show s-m34zar` reports `withered` with the reason), and the seed it
  was blocking flips to `ready` in the same repaint, because that is what the
  projection now says;
- the same verb by keyboard: arrows walk cards, `Enter` opens the menu, arrows
  walk the verbs, `Enter` opens the composer, `Enter` commits, `esc` unwinds one
  layer at a time;
- a drop with no legal verb bounces;
- both themes.

## The recording

Two clips, both driven through the same pointer/keyboard paths a hand walks —
no scripted state changes, every transition is a real `attn seed` verb.

**Dark** — top-level board → drill into `Snapshot restore on the remote leg` →
carry a card to Closed (two zones appear: Harvest, Wither) → drop on Wither →
type the closing note → commit. Watch `Bench the first-paint prefix on a 4k
grid` move from `blocked by 1` to `ready` in the same repaint: nothing told it
to, the projection just reads differently now. Then the same verb by keyboard —
`↑` to the card, `Enter` for its menu, `↓↓↓` to Wither, `Enter`, note, `Enter`.

![kanban-dark](https://raw.githubusercontent.com/victorarias/attn-pr-evidence/2b5897062f56f5556a20d2e83795a2d80a646f5b/delegate-proto-kanban-7ebfbf30/kanban-dark.gif)

[Full-quality recording (mp4)](https://raw.githubusercontent.com/victorarias/attn-pr-evidence/2b5897062f56f5556a20d2e83795a2d80a646f5b/delegate-proto-kanban-7ebfbf30/kanban-dark.mp4)

**Light** — the same board in the other theme, parking a ready seed. Parked
grows exactly one zone, because `park` is the only verb the garden accepts
there, and the composer says `goes on the log` because `park` refuses a reason.

![kanban-light](https://raw.githubusercontent.com/victorarias/attn-pr-evidence/2b5897062f56f5556a20d2e83795a2d80a646f5b/delegate-proto-kanban-7ebfbf30/kanban-light.gif)

[Full-quality recording (mp4)](https://raw.githubusercontent.com/victorarias/attn-pr-evidence/2b5897062f56f5556a20d2e83795a2d80a646f5b/delegate-proto-kanban-7ebfbf30/kanban-light.mp4)
