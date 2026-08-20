// app/src/components/GardenPanel.tsx
//
// The garden, seen. Slice 1's whole contract is that a seed planted from a
// terminal appears here without the user touching anything. The rows render the
// pushed garden snapshot; an open row reads its document detail again whenever
// that snapshot changes so its ledger stays live without polling.
//
// The garden is one space (ruled 2026-08-13): the panel opens on all of it and
// scopes by plot, not by workspace. Drilling into a crown, climbing back, and
// crossing into another plot are all local — the push already carries the whole
// garden, so navigation costs no round trip.
//
// Search adds two rules on top of that:
//   browsing is a tree, searching is a list. Text or a tender flattens the
//   scope's whole subtree into one ranked result list, because a search that
//   only looked one level down would be a lie — while a bare `is:` value only
//   re-lenses the level the reader is standing on;
//   the query is the only filter state. The closed toggle and the scope hints
//   write tokens into it rather than keeping flags beside it.
// See docs/plans/2026-08-20-garden-search.md.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Seed } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { crewDisplayName, crewHolderName } from '../utils/crewName';
import {
  IS_VALUES,
  buildIndex,
  parseQuery,
  satisfiesLens,
  searchGarden,
  splitRanges,
  toggleToken,
  type SearchEntry,
  type SeedMatch,
} from './gardenSearch';
import { SeedDocumentView, type SeedDocument } from './SeedDocumentView';
import './GardenPanel.css';

interface GardenPanelProps {
  isOpen: boolean;
  onClose: () => void;
  seeds: Seed[];
  // How many seeds the garden holds. Larger than seeds.length only when the
  // garden outgrew one push, and then the panel says so: a list that ends at a
  // cap without saying it reads as the whole garden.
  seedsTotal: number;
  /** Read the seed body and its whole ledger for the expanded drill. */
  fetchSeedDocument?: (seedId: string) => Promise<SeedDocument>;
  /** Dock the seed's annotated reading surface. */
  onOpenAsTile?: (seedId: string) => void;
  /** Open an attached markdown document as its own file tile. */
  onOpenMarkdownArtifact?: (path: string) => void;
  /** Reopen the session tending a seed — the way back to a delegate whose
   *  session is gone. Absent hides the affordance entirely. */
  onResumeSeed?: (seedId: string) => void;
}

// formatPlantedAt renders an RFC3339 created_at as a short relative phrase.
// Returns '' for an unparseable value rather than printing a broken date.
function formatPlantedAt(iso: string): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const deltaSec = Math.round((Date.now() - t) / 1000);
  if (deltaSec < 60) return 'just now';
  if (deltaSec < 3600) return `${Math.round(deltaSec / 60)}m ago`;
  if (deltaSec < 86400) return `${Math.round(deltaSec / 3600)}h ago`;
  return `${Math.round(deltaSec / 86400)}d ago`;
}

// statusClass maps a seed's status onto its row modifier. An unknown status —
// a daemon a version ahead — still gets a styled row rather than a bare one.
function statusClass(status: string): string {
  switch (status) {
    case 'planted':
    case 'growing':
    case 'harvested':
    case 'withered':
    case 'dormant':
      return `is-${status}`;
    default:
      return 'is-unknown';
  }
}

// closedStatus is the seed statuses that stop holding anything back. A blocker
// stops blocking the moment it is harvested or withered; a dormant one still
// blocks, because parking is a pause, not an answer.
function closedStatus(status: string): boolean {
  return status === 'harvested' || status === 'withered';
}

interface Relation {
  label: string;
  seed: Seed;
}

interface GardenIndex {
  byID: Map<string, Seed>;
  // Edges pointing at a seed, already phrased from that seed's side.
  inbound: Map<string, Relation[]>;
  // How many open seeds block a seed.
  blockers: Map<string, number>;
  // The seeds under each crown, for the subtree a search covers.
  children: Map<string, string[]>;
}

// indexEdges reads the whole pushed garden once. Edges are stored on the seed
// they point from, so the reverse direction is only knowable by walking every
// seed — per row that is a scan per row, and this panel renders the garden.
//
// The index is built over the unscoped push on purpose: a blocker can live in
// another workspace, and a panel that only looked at the workspace it shows
// would tell a blocked seed it is blocked by nothing.
function indexEdges(seeds: Seed[]): GardenIndex {
  const index: GardenIndex = {
    byID: new Map(),
    inbound: new Map(),
    blockers: new Map(),
    children: new Map(),
  };
  for (const seed of seeds) index.byID.set(seed.id, seed);
  for (const seed of seeds) {
    for (const edge of seed.edges ?? []) {
      const label = edge.kind === 'blocks' ? 'blocked by' : edge.kind === 'part-of' ? 'has part' : '';
      if (!label) continue;
      index.inbound.set(edge.to, [...(index.inbound.get(edge.to) ?? []), { label, seed }]);
      if (edge.kind === 'blocks' && !closedStatus(seed.status)) {
        index.blockers.set(edge.to, (index.blockers.get(edge.to) ?? 0) + 1);
      }
      if (edge.kind === 'part-of') {
        index.children.set(edge.to, [...(index.children.get(edge.to) ?? []), seed.id]);
      }
    }
  }
  return index;
}

// relationsOf lists a seed's edges in both directions.
function relationsOf(index: GardenIndex, id: string): Relation[] {
  const rows: Relation[] = [];
  for (const edge of index.byID.get(id)?.edges ?? []) {
    const other = index.byID.get(edge.to);
    if (!other) continue;
    if (edge.kind === 'blocks') rows.push({ label: 'blocks', seed: other });
    if (edge.kind === 'part-of') rows.push({ label: 'part of', seed: other });
  }
  return rows.concat(index.inbound.get(id) ?? []);
}

// tenderOf names whoever holds the seed: the crew member if there is one,
// because that is the name a person says, and the claiming session otherwise. A
// session id is not pretty, but "somebody holds this" is the fact the panel owes
// the reader — showing nothing would read as unclaimed.
function tenderOf(seed: Seed): string {
  return crewHolderName(seed.tender_member, seed.tender_session);
}

// crownOf is the crown a seed is part of, or '' when it stands on its own.
function crownOf(seed: Seed): string {
  return (seed.edges ?? []).find((edge) => edge.kind === 'part-of')?.to ?? '';
}

// progressOf renders a crown's plot as the counts a reader acts on. Zeroes are
// dropped: "0 growing" on a plot nobody has started is noise between the two
// numbers that matter.
function progressOf(seed: Seed): string {
  const p = seed.plot_progress;
  if (!p) return '';
  const parts = [`${p.done}/${p.total} done`];
  if (p.growing) parts.push(`${p.growing} growing`);
  if (p.ready) parts.push(`${p.ready} ready`);
  if (p.blocked) parts.push(`${p.blocked} blocked`);
  return parts.join(' · ');
}

// subtreeOf collects a crown's plot and every plot under it. A search from
// inside a plot covers all of it — matching only direct children would answer
// "not here" about a seed that is very much here.
function subtreeOf(index: GardenIndex, rootId: string): Set<string> {
  const wanted = new Set<string>();
  const queue = [rootId];
  while (queue.length > 0) {
    for (const child of index.children.get(queue.pop() as string) ?? []) {
      if (wanted.has(child)) continue;
      wanted.add(child);
      queue.push(child);
    }
  }
  return wanted;
}

// Marked runs of a match, rendered in place. Highlighting is color and weight
// rather than a filled box: a row of boxes reads as decoration at list density.
function Marked({ text, ranges }: { text: string; ranges: Array<[number, number]> }) {
  if (ranges.length === 0) return <>{text}</>;
  return (
    <>
      {splitRanges(text, ranges).map((part, i) =>
        part.hit ? (
          <mark key={i} className="garden-hit">
            {part.text}
          </mark>
        ) : (
          <span key={i}>{part.text}</span>
        ),
      )}
    </>
  );
}

export function GardenPanel({
  isOpen,
  onClose,
  seeds,
  seedsTotal,
  fetchSeedDocument,
  onOpenAsTile,
  onOpenMarkdownArtifact,
  onResumeSeed,
}: GardenPanelProps) {
  // The trail of crowns walked into, root last. Empty is the whole garden, which
  // is where the panel opens.
  const [trail, setTrail] = useState<string[]>([]);
  // The one filter state. Closed seeds stay out of it until a token asks for
  // them: the garden grows without bound and most of what it holds is done.
  const [query, setQuery] = useState('');
  // Where the search looks, when that is no longer where the reader stands.
  // Widening out of a plot is a property of the query, not a move — the trail
  // stays, so you can widen, look, and go back to what you were doing. It is
  // stored as the plot it was asked for rather than a bare flag, so walking
  // somewhere else drops it by construction instead of by an effect that fires
  // a frame late.
  const [wideIn, setWideIn] = useState<string | null>(null);
  // Where the arrows are, and which question they are walking. A new question
  // starts at its own best answer; keeping the answer's identity beside the
  // index is what stops one frame of the new results being drawn with the old
  // row highlighted.
  const [walk, setWalk] = useState<{ of: string; index: number }>({ of: '', index: 0 });
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [seedDocument, setSeedDocument] = useState<SeedDocument | null>(null);
  const [documentLoading, setDocumentLoading] = useState(false);
  const [documentError, setDocumentError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const listRef = useRef<HTMLUListElement | null>(null);

  const index = useMemo(() => indexEdges(seeds), [seeds]);

  // A crown can leave the garden while the panel sits inside its plot — a
  // harvest that removes it, a push that no longer carries it. The trail is
  // trimmed to what still exists rather than rendering an empty plot nobody can
  // climb out of.
  const livingTrail = useMemo(() => {
    const alive: string[] = [];
    for (const id of trail) {
      if (!index.byID.has(id)) break;
      alive.push(id);
    }
    return alive;
  }, [trail, index]);
  const plotId = livingTrail.length > 0 ? livingTrail[livingTrail.length - 1] : null;

  // A seed inside a crown lives in its plot, not at the root: listing it in
  // both places reads as two seeds. A child whose crown missed the push (the
  // cap dropped it) still shows at root — hiding it under an absent crown
  // would hide it everywhere.
  const scoped = useMemo(() => {
    if (!plotId) {
      return seeds.filter((seed) => {
        const parent = crownOf(seed);
        return !parent || !index.byID.has(parent);
      });
    }
    return seeds.filter((seed) => crownOf(seed) === plotId);
  }, [seeds, plotId, index]);

  // --- search -----------------------------------------------------------
  const parsed = useMemo(() => parseQuery(query), [query]);
  // Two questions, not one. `filtering` is whether the query says anything at
  // all, and it drives the field's own state. `searching` is whether the query
  // is a search: text or a tender flattens the scope's whole subtree, while a
  // bare `is:` value only re-lenses the seeds already on screen. Asking to see
  // closed work is not a search, and flattening the tree for it would answer a
  // question nobody asked.
  const filtering = parsed.active;
  const searching = parsed.searches;
  // Widening holds only while a search is running, and only in the plot it was
  // asked for: clearing the query or walking somewhere else puts the scope back
  // where the reader is, without anything having to notice and undo it.
  const wide = searching && wideIn !== null && wideIn === plotId;
  // What the arrows are walking. Two queries in two plots are two questions
  // even when the text is the same.
  const question = `${plotId ?? ''}\u0000${query}`;
  const activeIndex = walk.of === question ? walk.index : 0;
  // The searchable garden, lowercased once per snapshot. Doing this per
  // keystroke is what makes client-side search feel slow; see the receipts in
  // gardenSearch.bench.test.ts.
  const entries = useMemo(
    () =>
      buildIndex(seeds, {
        tenderOf,
        blockersOf: (seed: Seed) => index.blockers.get(seed.id) ?? 0,
      }),
    [seeds, index],
  );
  const entryByID = useMemo(() => {
    const map = new Map<string, SearchEntry>();
    for (const entry of entries) map.set(entry.seed.id, entry);
    return map;
  }, [entries]);

  const closedCount = useMemo(
    () => scoped.reduce((n, seed) => (closedStatus(seed.status) ? n + 1 : n), 0),
    [scoped],
  );
  // The list with no search running: this level, through whatever lens the
  // query names. The same predicate the search uses, so `is:closed` means one
  // thing in the panel and not two.
  const visible = useMemo(
    () =>
      scoped.filter((seed) => {
        const entry = entryByID.get(seed.id);
        return entry ? satisfiesLens(entry, parsed.is) : !closedStatus(seed.status);
      }),
    [scoped, entryByID, parsed],
  );

  // What a query searches: the plot's whole subtree, or the whole garden at
  // root. Both are flat — filtering is a list.
  const plotPool = useMemo(() => {
    if (!plotId) return entries;
    const wanted = subtreeOf(index, plotId);
    return entries.filter((entry) => wanted.has(entry.seed.id));
  }, [plotId, index, entries]);
  const pool = wide ? entries : plotPool;
  const results = useMemo(
    () => (searching ? searchGarden(pool, parsed) : []),
    [searching, pool, parsed],
  );
  // The two answers the panel owes a reader who is looking at fewer rows than
  // they expected: what a wider scope would find, and what the closed lens
  // hides. Both are counted, never implied.
  const gardenWide = useMemo(
    () => (searching && plotId && !wide ? searchGarden(entries, parsed).length : 0),
    [searching, plotId, wide, entries, parsed],
  );
  // While widened, the count the reader needs is the other one: how much of
  // this answer is in the plot they are standing in.
  const inPlot = useMemo(
    () => (searching && wide ? searchGarden(plotPool, parsed).length : 0),
    [searching, wide, plotPool, parsed],
  );
  const withClosed = useMemo(() => {
    if (!searching || parsed.is.length > 0) return 0;
    return searchGarden(pool, parseQuery(`${query} is:any`)).length;
  }, [searching, parsed, pool, query]);
  const outside = Math.max(0, gardenWide - results.length);
  const hiddenClosed = Math.max(0, withClosed - results.length);

  // One closed control, in one place, in both modes. Its count is what pressing
  // it does — how many rows it brings in, or how many it takes away — and it
  // stands down when the query names a lens of its own, because then the query
  // is already the statement and a toggle beside it would only argue with it.
  const closedToggle = useMemo(() => {
    if (parsed.is.some((value) => value !== 'any')) return null;
    const on = parsed.is.includes('any');
    const count = !searching
      ? closedCount
      : on
        ? results.reduce((n, match) => (closedStatus(match.seed.status) ? n + 1 : n), 0)
        : hiddenClosed;
    return count > 0 ? { count, on } : null;
  }, [parsed, searching, closedCount, results, hiddenClosed]);

  const rows = searching ? results.map((match) => match.seed) : visible;
  const matchByID = useMemo(() => {
    const map = new Map<string, SeedMatch>();
    for (const match of results) map.set(match.seed.id, match);
    return map;
  }, [results]);

  const activeSeed = rows[Math.min(activeIndex, rows.length - 1)];

  // Escape clears the query before it closes the panel. Registering only while
  // the query is non-empty puts this above the surface's own close handler
  // exactly when it should be, and out of the way when it should not.
  useEscapeStack(() => setQuery(''), isOpen && query !== '');

  useEffect(() => {
    if (!isOpen) return;
    listRef.current?.querySelector('[data-active="true"]')?.scrollIntoView({ block: 'nearest' });
  }, [activeIndex, isOpen, rows.length]);

  // Opening the garden is already the ask; the field is where the answer gets
  // typed, so focus starts there rather than one keystroke away.
  useEffect(() => {
    if (isOpen) inputRef.current?.focus();
  }, [isOpen]);

  // Every garden push is relevant while a drill is open: notes and edge
  // changes re-push the snapshot without necessarily changing the seed's own
  // revision. Re-read the detail on each new seeds array so the ledger stays
  // live without polling.
  useEffect(() => {
    if (!isOpen || !expandedId || !fetchSeedDocument) {
      setSeedDocument(null);
      setDocumentLoading(false);
      setDocumentError(null);
      return;
    }
    let ignore = false;
    setDocumentLoading(true);
    setDocumentError(null);
    fetchSeedDocument(expandedId)
      .then((document) => {
        if (ignore) return;
        setSeedDocument(document);
        setDocumentLoading(false);
      })
      .catch((error) => {
        if (ignore) return;
        setDocumentError(error instanceof Error ? error.message : `Could not read ${expandedId}`);
        setDocumentLoading(false);
      });
    return () => {
      ignore = true;
    };
  }, [expandedId, fetchSeedDocument, isOpen, seeds]);

  const drillInto = (id: string) => {
    setExpandedId(null);
    setTrail((prev) => [...prev, id]);
  };
  const climbTo = (depth: number) => {
    setExpandedId(null);
    setTrail((prev) => prev.slice(0, depth));
  };
  const focusSearch = useCallback(() => {
    const input = inputRef.current;
    if (!input) return;
    input.focus();
    input.select();
  }, []);
  const toggleWide = useCallback(() => {
    setWideIn((cur) => (cur === null ? plotId : null));
    focusSearch();
  }, [focusSearch, plotId]);
  // The closed lens is a token in the query, so the way in and the way out are
  // one call.
  const toggleClosed = useCallback(() => {
    setQuery((cur) => toggleToken(cur, 'is:any'));
    focusSearch();
  }, [focusSearch]);

  // Every pointer path has a key path. Rows are walked from the search field
  // itself — the reader never has to leave it to pick an answer.
  const onKeyDown = (event: React.KeyboardEvent) => {
    const inInput = event.target === inputRef.current;
    // `/` is only a shortcut where it is not a character: a note composer or any
    // other field inside a drilled seed keeps it.
    const target = event.target as HTMLElement | null;
    const typing =
      target?.tagName === 'INPUT' || target?.tagName === 'TEXTAREA' || target?.isContentEditable;
    if ((event.key === 'f' && event.metaKey) || (event.key === '/' && !typing)) {
      event.preventDefault();
      event.stopPropagation();
      focusSearch();
      return;
    }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      if (rows.length === 0) return;
      event.preventDefault();
      const step = event.key === 'ArrowDown' ? 1 : -1;
      setWalk({ of: question, index: Math.min(rows.length - 1, Math.max(0, activeIndex + step)) });
      return;
    }
    if (event.key === 'Enter' && event.altKey) {
      if (plotId && searching && (wide || outside > 0)) {
        event.preventDefault();
        toggleWide();
      }
      return;
    }
    if (event.key === 'Enter' && activeSeed) {
      event.preventDefault();
      if (event.metaKey && activeSeed.plot_progress) drillInto(activeSeed.id);
      else setExpandedId((cur) => (cur === activeSeed.id ? null : activeSeed.id));
      return;
    }
    if (event.key === 'ArrowRight' && activeSeed?.plot_progress && inInput === false) {
      event.preventDefault();
      drillInto(activeSeed.id);
      return;
    }
    if (event.key === 'ArrowLeft' && livingTrail.length > 0 && !inInput) {
      event.preventDefault();
      climbTo(livingTrail.length - 1);
    }
  };

  if (!isOpen) return null;

  const crown = plotId ? index.byID.get(plotId) : undefined;
  const where = plotId && !wide ? 'this plot' : 'the garden';
  // How many rows out of how many the scope holds. One shape in both modes,
  // and never `22` sitting beside a `22 closed` toggle meaning something else.
  const shown = rows.length;
  const total = searching ? pool.length : visible.length;

  return (
    <div className="garden-panel" role="region" aria-label="The garden" onKeyDown={onKeyDown}>
      <div className="garden-panel__header">
        <span className="garden-panel__kicker">The garden</span>
        <div className="garden-panel__header-actions">
          {/* The way in and the way out are the same button. It writes `is:any`
              into the query rather than keeping a flag beside it, so the line
              of type stays the only filter state there is. */}
          {closedToggle && (
            <button
              type="button"
              className="garden-panel__scope"
              aria-pressed={closedToggle.on}
              onClick={toggleClosed}
            >
              {closedToggle.on ? `hide ${closedToggle.count} closed` : `${closedToggle.count} closed`}
            </button>
          )}
          <span className="garden-panel__count">
            {shown === total ? shown : `${shown} of ${total}`}
          </span>
          <button type="button" className="garden-panel__close" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>
      </div>

      {/* Search and filters are one line and one state. The field is a line of
          type, not a box: at list density a bordered control reads as chrome
          before it reads as an invitation. */}
      <div className={`garden-search${filtering ? ' is-active' : ''}`}>
        <span className="garden-search__glyph" aria-hidden="true">
          /
        </span>
        <input
          ref={inputRef}
          className="garden-search__input"
          type="text"
          role="combobox"
          aria-expanded={filtering}
          aria-controls="garden-results"
          aria-activedescendant={activeSeed ? `garden-row-${activeSeed.id}` : undefined}
          aria-label={`Search ${where}`}
          placeholder="search — or is:ready, tender:…"
          spellCheck={false}
          autoComplete="off"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <span className="garden-search__meta">
          {parsed.unknown.length > 0 ? (
            <span className="garden-search__unknown">
              no filter called {parsed.unknown.join(' ')}
            </span>
          ) : parsed.partial ? (
            <span className="garden-search__values">
              {parsed.partial === 'is' ? (
                IS_VALUES.map((value) => (
                  <button
                    key={value}
                    type="button"
                    className="garden-search__value"
                    onClick={() => {
                      setQuery((cur) => `${cur.replace(/is:\S*$/i, '')}is:${value} `);
                      focusSearch();
                    }}
                  >
                    {value}
                  </button>
                ))
              ) : (
                <span className="garden-search__value-hint">a crew member or session</span>
              )}
            </span>
          ) : (
            filtering && (
              <>
                {plotId && !wide && outside > 0 && (
                  <button type="button" className="garden-search__widen" onClick={toggleWide}>
                    +{outside} in the whole garden <kbd>⌥↵</kbd>
                  </button>
                )}
                {plotId && wide && (
                  <button type="button" className="garden-search__widen" onClick={toggleWide}>
                    {inPlot} in this plot <kbd>⌥↵</kbd>
                  </button>
                )}
              </>
            )
          )}
        </span>
      </div>

      {/* The way back out. Every level is its own target, so climbing two plots
          is one click rather than two. At root there is nowhere back to, so the
          trail only appears once a plot is entered. */}
      {livingTrail.length > 0 && (
        <nav
          className={`garden-panel__trail${wide ? ' is-wide' : ''}`}
          aria-label={wide ? 'Standing here, searching the whole garden' : 'Where you are'}
        >
          <button type="button" className="garden-panel__trail-step" onClick={() => climbTo(0)}>
            Garden
          </button>
          {livingTrail.map((id, depth) => (
            <span key={id} className="garden-panel__trail-segment">
              <span className="garden-panel__trail-sep" aria-hidden="true">›</span>
              <button
                type="button"
                className="garden-panel__trail-step"
                onClick={() => climbTo(depth + 1)}
                disabled={depth === livingTrail.length - 1}
              >
                {index.byID.get(id)?.title ?? id}
              </button>
            </span>
          ))}
        </nav>
      )}

      {crown && (
        <div className="garden-panel__crown">
          <span className="garden-panel__crown-progress">{progressOf(crown) || 'nothing planted in it yet'}</span>
          <span className="garden-seed__id">{crown.id}</span>
        </div>
      )}

      {seedsTotal > seeds.length && (
        <p className="garden-panel__capped">
          {filtering
            ? `Search covers the newest ${seeds.length} of ${seedsTotal} seeds.`
            : `The garden holds ${seedsTotal} seeds; this panel has the newest ${seeds.length}.`}
        </p>
      )}

      {rows.length === 0 ? (
        filtering ? (
          // A no-results state names the query and offers only the moves that
          // would actually find something.
          <div className="garden-panel__nothing">
            <p className="garden-panel__nothing-line">
              Nothing in {where} matches <span className="garden-panel__echo">{query.trim()}</span>.
            </p>
            <ul className="garden-panel__moves">
              {plotId && !wide && outside > 0 && (
                <li>
                  <button type="button" onClick={toggleWide}>
                    Search the whole garden <span className="garden-panel__move-count">{outside}</span>
                  </button>
                  <kbd>⌥↵</kbd>
                </li>
              )}
              {plotId && wide && (
                <li>
                  <button type="button" onClick={toggleWide}>
                    Search this plot only <span className="garden-panel__move-count">{inPlot}</span>
                  </button>
                  <kbd>⌥↵</kbd>
                </li>
              )}
              {hiddenClosed > 0 && (
                <li>
                  <button type="button" onClick={toggleClosed}>
                    Include closed seeds <span className="garden-panel__move-count">{hiddenClosed}</span>
                  </button>
                </li>
              )}
              <li>
                <button
                  type="button"
                  onClick={() => {
                    setQuery('');
                    focusSearch();
                  }}
                >
                  Clear the search
                </button>
                <kbd>esc</kbd>
              </li>
            </ul>
          </div>
        ) : scoped.length > 0 ? (
          // Everything here is closed and closed is hidden: say so, because a
          // blank list over a non-empty plot reads as lost work.
          <p className="garden-panel__empty">
            Nothing open here. {closedCount} closed {closedCount === 1 ? 'seed is' : 'seeds are'}{' '}
            behind the closed toggle above.
          </p>
        ) : (
          <p className="garden-panel__empty">
            {plotId ? 'Nothing planted in this plot yet.' : 'The garden is empty.'}{' '}
            <code>
              {plotId ? `attn seed plant "what this is" --part-of ${plotId}` : 'attn seed plant "what this is"'}
            </code>{' '}
            puts something in it.
          </p>
        )
      ) : (
        <ul ref={listRef} className="garden-panel__list" id="garden-results" role="listbox">
            {rows.map((seed, rowIndex) => {
              const expanded = expandedId === seed.id;
              const active = rowIndex === Math.min(activeIndex, rows.length - 1);
              const match = matchByID.get(seed.id);
              const blockers = index.blockers.get(seed.id) ?? 0;
              const relations = expanded ? relationsOf(index, seed.id) : [];
              const homeCrown = searching ? index.byID.get(crownOf(seed)) : undefined;
              const localDocument: SeedDocument = {
                seed,
                tender_holds: Boolean(seed.tender_session || seed.tender_member),
                children: seeds.filter((candidate) => crownOf(candidate) === seed.id),
                notes: [],
                notes_total: 0,
                artifacts: [],
              };
              const displayedDocument = seedDocument?.seed.id === seed.id ? seedDocument : localDocument;
              return (
                <li
                  key={seed.id}
                  id={`garden-row-${seed.id}`}
                  role="option"
                  aria-selected={active}
                  data-active={active}
                  className={`garden-seed ${statusClass(seed.status)}${expanded ? ' is-expanded' : ''}${active ? ' is-active' : ''}`}
                >
                  <button
                    type="button"
                    className="garden-seed__head"
                    onClick={() => {
                      setWalk({ of: question, index: rowIndex });
                      setExpandedId((cur) => (cur === seed.id ? null : seed.id));
                    }}
                  >
                    <span className="garden-seed__status" aria-hidden="true" />
                    <span className="garden-seed__main">
                      <span className="garden-seed__title">
                        <Marked text={seed.title} ranges={match?.titleRanges ?? []} />
                      </span>
                      <span className="garden-seed__line">
                        <span className="garden-seed__state">{seed.status}</span>
                        {seed.ready && <span className="garden-seed__ready">ready</span>}
                        {blockers > 0 && (
                          <span className="garden-seed__blocked">
                            blocked by {blockers}
                          </span>
                        )}
                        {/* Where a result lives. A flat list without provenance
                            makes the reader open rows to find out. */}
                        {homeCrown && (
                          <span className="garden-seed__home">in {homeCrown.title}</span>
                        )}
                        {tenderOf(seed) && (
                          <span className="garden-seed__tender">tended by {tenderOf(seed)}</span>
                        )}
                        <span className={`garden-seed__id${match?.idHit ? ' garden-hit' : ''}`}>{seed.id}</span>
                        <span className="garden-seed__planted">{formatPlantedAt(seed.created_at)}</span>
                      </span>
                      {/* Why this row is here, when the title does not say. */}
                      {match?.snippet && (
                        <span className="garden-seed__snippet">
                          <Marked text={match.snippet.text} ranges={match.snippet.ranges} />
                        </span>
                      )}
                    </span>
                  </button>
                  {seed.plot_progress && (
                    <button
                      type="button"
                      className="garden-seed__plot"
                      onClick={() => drillInto(seed.id)}
                      aria-label={`Open the plot under ${seed.title}`}
                    >
                      <span className="garden-seed__plot-counts">{progressOf(seed)}</span>
                      <span className="garden-seed__plot-arrow" aria-hidden="true">›</span>
                    </button>
                  )}
                  {expanded && (
                    <div className="garden-seed__detail">
                      <div className="garden-seed__actions">
                        {onOpenAsTile && (
                          <button
                            type="button"
                            className="garden-seed__open-tile"
                            onClick={() => onOpenAsTile(seed.id)}
                          >
                            Open as tile
                          </button>
                        )}
                        {/* The way back to a delegate: reopen the session tending
                            this seed. Shown only when one holds it — with nobody
                            tending, there is nothing to reopen, and the daemon
                            would say exactly that. A running tender is focused
                            rather than spawned, so the button reads the same
                            either way. */}
                        {onResumeSeed && seed.tender_session && (
                          <button
                            type="button"
                            className="garden-seed__reopen"
                            data-testid={`seed-reopen-${seed.id}`}
                            onClick={() => onResumeSeed(seed.id)}
                          >
                            Reopen agent
                          </button>
                        )}
                      </div>
                      <div className="garden-seed__meta">
                        <span>{seed.status}</span>
                        {seed.planter_member && <span>planted by {crewDisplayName(seed.planter_member)}</span>}
                        {tenderOf(seed) && <span>tended by {tenderOf(seed)}</span>}
                        {seed.reason && <span>{seed.reason}</span>}
                        {seed.template && <span>packet</span>}
                        {seed.gate && <span>gate</span>}
                      </div>
                      {relations.length > 0 && (
                        <ul className="garden-seed__relations">
                          {relations.map((relation) => (
                            <li key={`${relation.label}:${relation.seed.id}`}>
                              <span className="garden-seed__relation-label">{relation.label}</span>
                              {/* Crossing plots: a related crown is one click away,
                                  so following work sideways never goes through the
                                  whole garden. */}
                              {relation.seed.plot_progress ? (
                                <button
                                  type="button"
                                  className="garden-seed__relation-title garden-seed__relation-link"
                                  onClick={() => drillInto(relation.seed.id)}
                                >
                                  {relation.seed.title}
                                </button>
                              ) : (
                                <span className="garden-seed__relation-title">{relation.seed.title}</span>
                              )}
                              <span className="garden-seed__id">{relation.seed.id}</span>
                              <span className="garden-seed__relation-state">{relation.seed.status}</span>
                            </li>
                          ))}
                        </ul>
                      )}
                      {documentLoading && seedDocument?.seed.id !== seed.id ? (
                        <p className="garden-seed__document-state">Loading seed…</p>
                      ) : documentError ? (
                        <p className="garden-seed__document-state garden-seed__document-state--error">{documentError}</p>
                      ) : (
                        <SeedDocumentView
                          document={displayedDocument}
                          compact
                          onOpenMarkdownArtifact={onOpenMarkdownArtifact}
                        />
                      )}
                    </div>
                  )}
                </li>
              );
            })}
        </ul>
      )}
    </div>
  );
}
