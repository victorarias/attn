# Terminal annotation alignment across rendered links

Status: complete.

## Goal

Keep a completed assistant message annotatable when an agent TUI renders its
Markdown links as labels, shortened paths, or OSC 8 hyperlinks. A rendering
substitution may leave the link itself unmatched, but it must not cut off the
ordinary prose before or after it.

## Architecture

```text
Current
  transcript Markdown + terminal rows
    -> one seed on one token-index diagonal
      -> greedy walk stops at a renderer substitution
        -> one annotatable fragment; the rest looks like TUI text

Target
  transcript Markdown
    -> prose tokens + link label/target alternatives with source offsets
  terminal rows
    -> visible tokens + optional OSC 8 target/path aliases
  both token streams
    -> monotone chain of high-confidence anchors
      -> locally fill the spans between anchors
        -> sparse row alignment across renderer substitutions
          -> selection containment gate

Tests
  real Codex-style Markdown/path rendering fixture
    -> alignMessage / offsetsForSelection
      -> TerminalAnnotationStore.anchorForSelection
```

The external interface remains Markdown offsets in a stable assistant message.
`terminalMessageAlign` absorbs rendering variation; `AnnotatedTerminal`, the
daemon message window, and persisted annotation records do not learn about
Codex link syntax.

## Interfaces

```ts
type MessageRowAccess = {
  rowText(row): string
  rowTextRange(row, startCol, endCol): string
  hyperlinkUri?(row, col): string | null
}

type AlignmentToken = {
  norm: string
  aliases: string[]       // URI and path identities, when present
  source or grid position // unchanged provenance
}
```

Hyperlink metadata is optional. Plain text still aligns, and a shortened path
falls back to conservative basename/suffix identities. URI matches are stronger
when the TUI emitted OSC 8.

## Implementation

- [x] Add a Codex regression where several local-file links render as shortened
      paths and prove selections before, on, and after them.
- [x] Parse Markdown links into label and destination tokens without changing
      their source-offset provenance.
- [x] Replace the single-diagonal seed with a monotone chain of unique token
      anchors, retaining bounded local resynchronization inside each span.
- [x] Expose optional Ghostty hyperlink targets through `MessageRowAccess` and
      use them as token aliases without scanning idle terminals.
- [x] Apply the existing containment predicate before accepting a new reverse
      selection, not only before painting an existing annotation.
- [x] Run focused alignment/store tests, the full frontend suite, TypeScript,
      and the packaged app scenario in an isolated profile.

## Decisions

- Keep the existing alignment interface deep: provider rendering knowledge
  stays inside tokenization and matching rather than leaking into callers.
- Long substitutions create sparse holes. Raising the four-token resync window
  would increase cross-turn false matches without representing links correctly.
- OSC 8 is evidence, not a requirement. Missing hyperlink metadata degrades to
  conservative text/path matching.
- The containment gate remains authoritative. Better recall must never turn a
  refusal into an annotation on the user's prompt or TUI chrome.
