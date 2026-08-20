# Crown drilling and the seed reading surface

Why the garden panel drills the way it does, what the two renderers are for,
and which decisions are still open.

The complaint this answers: drilling was an instant list swap with a
breadcrumb, scroll position was lost on the way back out, depth was only
knowable by reading the trail, a 45-child crown was a wall, and attachments
rendered as small uniform tiles.

## The direction: the garden is a stack of places

Every seed is a place with the same shape — title, document, what is under it,
what happened to it. The root is that shape with the garden's own list in the
middle. Drilling **pushes** a place; escape **pops** one. A crown and a leaf
differ by whether the place has anything under it, not by which screen you are
on.

Three things fall out of that, and they are the whole design:

**Reading a seed is drilling into it.** The old panel had two targets on one
row — click the head to expand the body, click a separate chevron to enter the
plot — and the reader had to know which one they wanted before they knew what
was in there. Now a row has one target. You go in, and what is in there is
whatever is in there.

**Scroll is per place, and a place is its trail.** Each page keeps its own
offset in a map keyed by the trail, restored in a layout effect before paint,
so climbing out lands you exactly where you were — no flash of the top. Focus
comes back to the row you left through, so the keyboard keeps its place too,
not just the eye. This is what makes it affordable to put a seed's document
*above* its 45 children: you are not paying for that scroll twice.

**The trail is the way back and nothing else.** It names the places behind you,
never the one you are in. The place you are in is the 18px title at the top of
the page — until you scroll past it, at which point the trail picks it up
(the iOS large-title handoff). Nothing is stated twice, and "where am I"
survives scrolling.

Depth is carried by three quiet signals rather than by decoration: the trail
gets one step longer, the steps behind fade by depth, and the page slides in
from the right (160ms, `cubic-bezier(0.2,0,0,1)`) — left when you climb, so
the movement has a direction. All of it lives under
`@media (prefers-reduced-motion: no-preference)`; with motion reduced the page
simply is there.

Density: a row is one line, and only exceptions get words. Nothing says
"planted" — most seeds are planted. `growing`, `parked`, `blocked by 2`,
`withered` earn a word because they are the ones you would act on. The status
pip carries the rest, and the id appears in a reserved slot on hover or focus,
so it is there when you need to paste it and gone when you are reading. Closed
work is out of the way behind the query's own lens: 45 children read as 18
lines plus a "27 closed" control in the chrome, and pressing it writes
`is:any` into the search line. See "Meeting the search PR" below.

Artifacts are rows in a table, not chips. A fixed left gutter names the kind in
words — `markdown`, `pull request`, `issue`, `link`, `notebook`, `repo file` —
so the column is scannable and there is no icon vocabulary to learn. The
alignment does the work a box would have done. A GitHub URL is recognised by
shape and reads as `#960 victorarias/attn`, because that is what it is. And a
markdown artifact whose file is gone renders struck through with `not on disk`
and no action, instead of offering to open nothing.

The taste pass deleted: the expand/drill split, the "planted" word, the "Garden"
trail root, per-row chips, the artifact tiles, the 9px type tier, and the nine
`attach` entries that were burying the one real note in a seed's log (folded
behind a counted line — the artifact section above *is* their outcome).

## Two renderers, one walk

The dock and the fullscreen surface are the same panel at two sizes, and they
draw the walk differently: the dock keeps the stack of places above, fullscreen
draws it as **Miller columns**. This is not two designs. The trail is the model
in both — `drillInto` pushes, escape pops, the same scroll map, the same row
component. The stack renderer draws `trail[n]`; the columns renderer draws the
last few levels beside it. Depth carries across when you move between the two
sizes, because depth is in the model, not in either renderer.

Clicking a row in a column is the same verb as drilling: `selectAtLevel`
truncates the trail to that column's level and appends your row. "Go deeper" and
"switch siblings" are one gesture, which is what makes a column a column rather
than a list that happens to sit beside another one.

**Width decides how many columns, not depth.** The reader keeps 560px before a
column is allowed to exist; each column costs 300. So 1160px gets one list
column beside the document, 1460px gets two, 1780px gets three — measured off
the panel's own box, not the window, because the panel is not always the whole
screen. A five-deep walk and a two-deep walk look the same at the same size.
Widening the window shows you more of where you came from instead of
rearranging where you are.

**The trail carries what the columns cannot.** A visible column already says
which of its rows you picked, so repeating those steps in the trail would state
the same thing twice. The trail's steps start above the leftmost column and stop
before the place you are in — which means at three columns and two levels deep
it says only "Garden", and it grows one step for each level the window could not
hold. Past three steps the middle folds to `…`, which opens on click. Nothing
scrolls sideways at any depth; Finder's answer — slide the columns left and let
the oldest fall off — trades the trail for a horizontal scrollbar, and the trail
is cheaper.

Verified on a six-level nest (crown → panel → header controls → overflow menu →
fold threshold → leaves): the walk down and the climb back produce identical
column sets and trails at every level, at 1200px and at 1660px.

### What the deep walk found

A column unmounts when the walk goes deeper than the window can hold, so
anything a column owns itself is forgotten by the act of walking. A list that
comes back shorter than it left clamps the restored scroll offset to the top,
and the reader loses their place — the original complaint arriving through a
side door. Every piece of a column's state therefore lives outside it: the
scroll offsets in a place-keyed map, and which rows a lens admits in the query,
which the whole walk shares. Receipt: scroll a 45-row crown column to 518, walk
four levels down, climb back — 45 rows, top 518.

## The alternative that lost

**Inline expansion / an indented outline tree** (no navigation at all; a crown
expands in place, children indent under it). The cheapest to build and the
easiest to explain. It lost on 45 children: expanding one crown inside a list
of crowns pushes everything below it off-screen, and the reader's place is
destroyed by the very act of looking — the same complaint, arriving through a
different door. Indentation also stops meaning anything past two levels, and
the garden already nests three. It survives as a *detail*: the log's
attachment disclosure is inline expansion, used where the count is the
information and the content is a tail.

Miller columns were written up here as a loss, on the grounds that a
third-of-the-window column cannot hold a markdown document. **That measurement
was taken in a 1100px mock, not at fullscreen.** At 1710px, two 300px columns
leave 1110px for the document and the text still sets at a 65–75 character
measure. The objection was real for the dock and wrong for fullscreen, which is
why the dock keeps the stack.

## What is not done

No daemon changes and no protocol changes: every decision here is in the view.
What this leaves for later:

- **Virtualization** — not yet needed: 45 rows at one line each is nothing. It slots in one place, the `<ul className="garden-list">`
  inside `SeedList`, and the scroll-memory map already stores an offset per
  place, which is the state a windowed list needs. A plot past a few hundred
  children is when it earns its complexity.
- **Artifact existence should come from the daemon.** The view asks `fs_exists`
  per absolute path, which only works because the app is a trusted fs client —
  and it cannot answer for a repo-relative artifact at all, because the view
  does not know which worktree it belongs to. A repo-relative artifact
  therefore renders unflagged rather than wrongly struck through. The fix is a
  daemon-projected `present: bool` beside each artifact in the seed document:
  one resolution, correct for every kind.
- **Scroll restoration has no unit test.** happy-dom lays nothing out, so a
  restored `scrollTop` clamps to zero and the assertion would pass against a
  broken implementation. It is covered by the live walk instead — the receipt
  is in "What the deep walk found" above. The disclosure half of the same bug
  *is* unit-tested, because it does not depend on layout.
- **Deep-linking a place.** The trail is client state, so ⌘K cannot land you
  inside a plot. See open question 2.

## Meeting the search PR

`garden/list-scope-and-type-scale` landed as #967 while this was being built,
and it reworked the same file. Two of its decisions replace prototype
behaviour, and both replacements are smaller than what they replace.

**One closed control, not one per list.** The prototype gave every list its own
counted disclosure, remembered per place. #967 makes the query line the only
filter state the panel has, so the closed lens is the token `is:any` and the
control that writes it sits in the chrome. That is one statement about the
place the reader is standing in rather than one per column, and it deletes the
per-column state that the deep walk had just caught losing the reader's place.

**Browsing is a tree, searching is a list.** Text or a `tender:` flattens the
scope's whole subtree into one ranked list, which replaces the walk's lists
rather than sitting beside them — two of them on screen would be two answers to
one question. A bare `is:` value is not a search: it re-lenses the level the
reader is standing on, in both renderers. Picking an answer ends the question —
the query clears and the trail lands on where that seed actually lives, so a
result is a way into the tree rather than a place of its own.

Two keyboard consequences the two designs had to negotiate. Escape now clears
the query before it climbs, so a reader with a filter typed presses it once more
than they press levels — the right order, since escape never takes away more
than was asked. And the field keeps the arrows only while it has answers to
walk: with none, they go into the walk instead. Picking an answer clears the
query and leaves the caret in the field, which is precisely when a field that
kept the arrows would strand the keyboard.

## Open questions — yours

1. **Does the artifact kind set need a first-class `pull_request` kind?** The
   prototype recognises GitHub URLs by shape and shows `#960 victorarias/attn`.
   That is a view-side heuristic that will quietly mis-handle a GHE host or a
   shortened link. A real kind means a daemon change and a migration; the
   heuristic means the view keeps guessing. I lean heuristic until someone is
   annoyed by it, but it is your call which side owns the vocabulary.
2. **Should the trail be state or a URL?** Local state is why drilling never
   fetches and why it is this fast — but it also means nothing can link to a
   place. If you want ⌘K to open a seed *inside* its plot, the trail becomes
   routable state, and that is a bigger change than it looks.
3. ~~**What does the dock do with a document?**~~ **Answered:** the dock reads
   it, in the stack renderer, at its own measure. Fullscreen gets Miller
   columns. See "Two renderers, one walk" above.
4. **How much of a seed's log should the page show at all?** Folding
   attach/detach was clearly right. The next question — do handoffs deserve to
   be the log's primary content, with plain notes folded too — I could not
   answer without watching you use it.
