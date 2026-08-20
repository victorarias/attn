// app/src/components/GardenPanel.tsx
//
// The garden, walked. The panel is a stack of places rather than a list that
// swaps: the root is the garden, and every seed you open is a place of its own
// with the same shape — title, document, what is under it, what happened to it.
// Drilling pushes a place, escape pops one, and each place keeps its own scroll
// offset, so climbing out puts the reader back exactly where they were.
//
// The trail is the way back and nothing else: it names the places behind you,
// never the one you are in. The place you are in is the big title at the top of
// the page — until you scroll past it, at which point the trail picks it up.
//
// One model, two renderers. The trail is the model. The dock draws only the
// place you are in (`layout="stack"`); fullscreen draws as many levels beside
// the document as its width holds (`layout="columns"`), which is the same walk
// with more of it on screen. Both read the same trail and the same scroll
// memory, so moving between the two sizes keeps your depth and your place
// instead of restarting the walk.
//
// Search rules the panel from one line of type, and two rules hold it together
// (see docs/plans/2026-08-20-garden-search.md):
//   browsing is a tree, searching is a list. Text or a tender flattens the
//   scope's whole subtree into one ranked result list, because a search that
//   only looked one level down would be a lie — while a bare `is:` value only
//   re-lenses the level the reader is standing on;
//   the query is the only filter state. The closed toggle and the scope hints
//   write tokens into it rather than keeping flags beside it.
// Picking an answer ends the question: the query clears and the walk lands
// where that seed actually lives, so a result is a way into the tree rather
// than a place of its own.
//
// Data is the pushed garden snapshot; navigating never fetches. A seed's log and
// artifacts are read on arrival and re-read on every push, so the page paints
// instantly and its ledger stays live without polling.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { gardenScrollMemory, useGardenWalk } from '../store/gardenWalk';
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
  type Range,
  type SearchEntry,
  type SeedMatch,
} from './gardenSearch';
import { Markdown } from './Markdown';
import { MarkdownReader } from './MarkdownReader';
import { seedMarkdownSource } from './MarkdownReader/documentSource';
import { SeedArtifactRows } from './SeedArtifactRows';
import type { SeedDocument } from './SeedDocumentView';
import type { SeedDocumentNote } from './seedArtifacts';
import './GardenPanel.css';

interface GardenPanelProps {
  isOpen: boolean;
  onClose: () => void;
  seeds: Seed[];
  seedsTotal: number;
  fetchSeedDocument?: (seedId: string) => Promise<SeedDocument>;
  onOpenAsTile?: (seedId: string) => void;
  onOpenMarkdownArtifact?: (path: string) => void;
  onResumeSeed?: (seedId: string) => void;
  /** Answers whether an artifact's path is really on disk. */
  checkArtifactPath?: (path: string) => Promise<boolean>;
  /** The list/board switch. Owned by the frame: both views show the same one,
   *  in the same place, so switching moves nothing. */
  viewToggle?: React.ReactNode;
  /** Which frame the garden is being read in, for the header control's direction. */
  frame?: 'dock' | 'full';
  /** Promote to the window, or hand it back to the dock. Absent hides the
   *  control — the panel still renders anywhere it did before. */
  onToggleFrame?: () => void;
  /** The bottom of the Escape ladder, below climbing: called when there is no
   *  place left to climb out of. */
  onEscapeFloor?: () => void;
}

// How much of the walk the panel draws is decided by how much room it has, not
// by who rendered it. Below the first threshold there is no space for a list
// beside a readable document, so the walk stacks one place at a time; above it
// the walk sits beside what you are reading, and a second list joins at the
// next. The panel growing across a threshold IS the promotion from the dock to
// the window — see GardenFrame.
const COLUMNS_MIN = 1160;
const THIRD_COLUMN_MIN = 1460;

// The frame control: two corners pulling apart, or back together.
function FrameGlyph({ direction }: { direction: 'out' | 'in' }) {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
      <path
        d={direction === 'out'
          ? 'M4.5 1.5H1.5V4.5M7.5 10.5H10.5V7.5'
          : 'M1.5 4.5H4.5V1.5M10.5 7.5H7.5V10.5'}
        stroke="currentColor"
        strokeWidth="1.25"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

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

function formatTimestamp(iso: string): string {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString();
}

// An attach or a detach is bookkeeping: the artifact section above IS its
// outcome, so the log reads as what people said, and the churn folds away
// behind a counted disclosure.
function isSpoken(note: SeedDocumentNote): boolean {
  return note.kind !== 'attach' && note.kind !== 'detach';
}

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

// A closed seed is one nobody will pick up again. Parking is a pause, not an
// answer, so a dormant seed is still open work.
function isClosed(seed: Seed): boolean {
  return seed.status === 'harvested' || seed.status === 'withered';
}

function crownOf(seed: Seed): string {
  return (seed.edges ?? []).find((edge) => edge.kind === 'part-of')?.to ?? '';
}

// The daemon sends plot_progress for a seed that is a plot, and only for one. An
// empty plot is still a plot: it has to be able to say how to fill it.
function isPlot(seed: Seed): boolean {
  return Boolean(seed.plot_progress);
}

function tenderOf(seed: Seed): string {
  return crewHolderName(seed.tender_member, seed.tender_session);
}

interface Relation {
  label: string;
  seed: Seed;
}

interface GardenIndex {
  byID: Map<string, Seed>;
  children: Map<string, Seed[]>;
  inbound: Map<string, Relation[]>;
  blockers: Map<string, number>;
  /** Crowns and loose seeds — everything that is not part of something else. */
  roots: Seed[];
}

// The whole pushed garden, read once. Edges are stored on the seed they point
// from, so the reverse direction is only knowable by walking every seed.
function indexGarden(seeds: Seed[]): GardenIndex {
  const index: GardenIndex = {
    byID: new Map(),
    children: new Map(),
    inbound: new Map(),
    blockers: new Map(),
    roots: [],
  };
  for (const seed of seeds) index.byID.set(seed.id, seed);
  for (const seed of seeds) {
    const crown = crownOf(seed);
    // A child whose crown missed the push (the cap dropped it) is a root here:
    // filing it under a crown nobody can walk into would hide it everywhere.
    if (crown && index.byID.has(crown)) {
      index.children.set(crown, [...(index.children.get(crown) ?? []), seed]);
    } else {
      index.roots.push(seed);
    }
    for (const edge of seed.edges ?? []) {
      const label = edge.kind === 'blocks' ? 'blocked by' : edge.kind === 'part-of' ? 'has part' : '';
      if (!label) continue;
      index.inbound.set(edge.to, [...(index.inbound.get(edge.to) ?? []), { label, seed }]);
      if (edge.kind === 'blocks' && !isClosed(seed)) {
        index.blockers.set(edge.to, (index.blockers.get(edge.to) ?? 0) + 1);
      }
    }
  }
  return index;
}

// What this seed is tied to, minus what the page already shows: its children are
// the plot section, so `has part` is dropped. `part of` stays even though the
// trail usually says it — crossing a blocks edge lands the reader on a seed
// whose trail names the plot they came from, not the one it belongs to.
function relationsOf(index: GardenIndex, id: string): Relation[] {
  const rows: Relation[] = [];
  for (const edge of index.byID.get(id)?.edges ?? []) {
    const other = index.byID.get(edge.to);
    if (!other) continue;
    if (edge.kind === 'blocks') rows.push({ label: 'blocks', seed: other });
    if (edge.kind === 'part-of') rows.push({ label: 'part of', seed: other });
  }
  return rows.concat((index.inbound.get(id) ?? []).filter((relation) => relation.label === 'blocked by'));
}

// A crown's plot and every plot under it. A search from inside a plot covers all
// of it — matching only direct children would answer "not here" about a seed
// that is very much here.
function subtreeOf(index: GardenIndex, rootId: string): Set<string> {
  const wanted = new Set<string>();
  const queue = [rootId];
  while (queue.length > 0) {
    for (const child of index.children.get(queue.pop() as string) ?? []) {
      if (wanted.has(child.id)) continue;
      wanted.add(child.id);
      queue.push(child.id);
    }
  }
  return wanted;
}

// Where a seed actually lives, root first. Opening an answer walks there, so the
// trail after a search says what it would have said had the reader found the
// seed by hand.
function pathTo(index: GardenIndex, id: string): string[] {
  const path: string[] = [];
  const guard = new Set<string>();
  let cursor = index.byID.get(id);
  while (cursor && !guard.has(cursor.id)) {
    guard.add(cursor.id);
    path.unshift(cursor.id);
    const parent = crownOf(cursor);
    cursor = parent ? index.byID.get(parent) : undefined;
  }
  return path;
}

// Only exceptions get words. Most open seeds are planted and unblocked; giving
// each of them a badge turns a scannable list into a wall, so the pip carries
// the resting state and the column stays empty until something is true about a
// seed that a reader would act on.
function signalOf(seed: Seed, blockers: number): { text: string; tone: string } | null {
  if (blockers > 0) return { text: `blocked by ${blockers}`, tone: 'blocked' };
  if (seed.status === 'growing') return { text: 'growing', tone: 'active' };
  if (seed.status === 'dormant') return { text: 'parked', tone: 'parked' };
  if (seed.status === 'withered') return { text: 'withered', tone: 'closed' };
  if (seed.status === 'harvested') return { text: 'done', tone: 'closed' };
  return null;
}

function progressWords(seed: Seed): string {
  const p = seed.plot_progress;
  if (!p) return '';
  const parts = [`${p.done}/${p.total} done`];
  if (p.growing) parts.push(`${p.growing} growing`);
  if (p.ready) parts.push(`${p.ready} ready`);
  if (p.blocked) parts.push(`${p.blocked} blocked`);
  if (p.dormant) parts.push(`${p.dormant} parked`);
  return parts.join(' · ');
}

// Marked runs of a match, rendered in place. Highlighting is color and weight
// rather than a filled box: a row of boxes reads as decoration at list density.
function Marked({ text, ranges }: { text: string; ranges: Range[] }) {
  if (ranges.length === 0) return <>{text}</>;
  return (
    <>
      {splitRanges(text, ranges).map((part, i) =>
        part.hit ? (
          <mark key={i} className="garden-hit">{part.text}</mark>
        ) : (
          <span key={i}>{part.text}</span>
        ),
      )}
    </>
  );
}

interface RowProps {
  seed: Seed;
  blockers: number;
  onOpen: (id: string) => void;
  /** The row this column was walked through, if any. */
  selected?: boolean;
  /** The row the arrows are on, while a search is what is on screen. */
  active?: boolean;
  match?: SeedMatch;
  /** Which plot an answer came out of. Only a flat result list needs it. */
  home?: Seed;
  /** This row is an answer in the search field's listbox, not a place to walk. */
  option?: boolean;
}

// One line per seed. The right side is empty for a plain open seed and fills in
// only when there is something to say; the id appears under the pointer or
// focus, in a slot that is always reserved, so revealing it never moves a row.
function SeedRow({ seed, blockers, onOpen, selected, active, match, home, option }: RowProps) {
  const progress = seed.plot_progress;
  const signal = signalOf(seed, blockers);
  const tender = tenderOf(seed);
  return (
    <li
      className={`garden-row ${statusClass(seed.status)}${isClosed(seed) ? ' is-closed' : ''}${selected ? ' is-selected' : ''}${active ? ' is-active' : ''}`}
    >
      <button
        type="button"
        id={`garden-row-${seed.id}`}
        role={option ? 'option' : undefined}
        aria-selected={option ? Boolean(active) : undefined}
        data-seed-row={seed.id}
        onClick={() => onOpen(seed.id)}
      >
        <span className="garden-row__pip" aria-hidden="true" />
        <span className="garden-row__title">
          <Marked text={seed.title} ranges={match?.titleRanges ?? []} />
        </span>
        {signal && <span className={`garden-row__signal is-${signal.tone}`}>{signal.text}</span>}
        {home && <span className="garden-row__home">in {home.title}</span>}
        {tender && <span className="garden-row__tender">tended by {tender}</span>}
        <span className={`garden-row__id${match?.idHit ? ' garden-hit' : ''}`}>{seed.id}</span>
        {progress && (
          <span className="garden-row__plot">
            {progress.done}/{progress.total}
            <span className="garden-row__chevron" aria-hidden="true">›</span>
          </span>
        )}
      </button>
      {/* Why this row is here, when its title does not say. */}
      {match?.snippet && (
        <p className="garden-row__snippet">
          <Marked text={match.snippet.text} ranges={match.snippet.ranges} />
        </p>
      )}
    </li>
  );
}

interface ListProps {
  seeds: Seed[];
  index: GardenIndex;
  onOpen: (id: string) => void;
  selectedId?: string;
  activeId?: string;
  matchByID?: Map<string, SeedMatch>;
  /**
   * Name each row's plot, except the one the reader is already standing in.
   * A flat result list needs it; a level list does not — and inside a plot,
   * where most answers come from that plot, repeating its name on every row is
   * a wall of the one fact the reader already has.
   */
  homes?: boolean;
  hereId?: string | null;
  emptyMessage?: React.ReactNode;
  listId?: string;
  /** This list is the search field's listbox. A level of the walk is not. */
  options?: boolean;
}

function SeedList({ seeds, index, onOpen, selectedId, activeId, matchByID, homes, hereId, emptyMessage, listId, options }: ListProps) {
  if (seeds.length === 0) return emptyMessage ? <p className="garden-empty">{emptyMessage}</p> : null;
  return (
    <ul className="garden-list" id={listId} role={options ? 'listbox' : undefined}>
      {seeds.map((seed) => (
        <SeedRow
          key={seed.id}
          seed={seed}
          blockers={index.blockers.get(seed.id) ?? 0}
          onOpen={onOpen}
          selected={seed.id === selectedId}
          active={seed.id === activeId}
          match={matchByID?.get(seed.id)}
          home={homes && crownOf(seed) !== hereId ? index.byID.get(crownOf(seed)) : undefined}
          option={options}
        />
      ))}
    </ul>
  );
}

// The log's bookkeeping, counted rather than listed. Attaching eight files
// wrote eight entries nobody reads; what they produced is the artifact section.
function BookkeepingDisclosure({ notes }: { notes: SeedDocumentNote[] }) {
  const [shown, setShown] = useState(false);
  return (
    <>
      <button
        type="button"
        className="garden-closed-toggle"
        aria-expanded={shown}
        onClick={() => setShown((open) => !open)}
      >
        <span className="garden-closed-toggle__caret" aria-hidden="true">{shown ? '⌄' : '›'}</span>
        {notes.length} attachment {notes.length === 1 ? 'change' : 'changes'}
      </button>
      {shown && (
        <ol className="garden-log garden-log--quiet">
          {notes.map((note) => (
            <li key={note.id} data-kind={note.kind}>
              <div className="garden-log__head">
                <span className="garden-log__kind">{note.kind}</span>
                <span className="garden-log__who">{note.body || '—'}</span>
                <time dateTime={note.created_at} title={formatTimestamp(note.created_at)}>
                  {formatPlantedAt(note.created_at)}
                </time>
              </div>
            </li>
          ))}
        </ol>
      )}
    </>
  );
}

// One list level of the walk, in the columns renderer. It owns its scroll
// offset the same way a place does in the stack: the level path is the key, so
// switching siblings inside a column never moves it, and climbing back into a
// column you have already read puts you where you were.
function ColumnList({
  levelKey,
  seeds,
  index,
  selectedId,
  memory,
  onOpen,
}: {
  levelKey: string;
  seeds: Seed[];
  index: GardenIndex;
  selectedId: string;
  memory: React.MutableRefObject<Map<string, number>>;
  onOpen: (id: string) => void;
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  // Restore after the rows are laid out: a shorter list clamps the offset to
  // nothing, so this cannot happen before the rows exist.
  useLayoutEffect(() => {
    const el = ref.current;
    if (el) el.scrollTop = memory.current.get(`col:${levelKey}`) ?? 0;
  }, [levelKey, memory, seeds]);
  return (
    <div
      className="garden-column"
      data-column={levelKey}
      ref={ref}
      onScroll={() => {
        const el = ref.current;
        if (el) memory.current.set(`col:${levelKey}`, el.scrollTop);
      }}
    >
      <SeedList
        seeds={seeds}
        index={index}
        onOpen={onOpen}
        selectedId={selectedId}
        emptyMessage={<>Nothing here yet.</>}
      />
    </div>
  );
}

// A plot's drain state, as proportion rather than arithmetic. The counts beside
// it carry the numbers; this carries how far along it is at a glance, which is
// the question a forty-five child plot is actually asked.
function ProgressBar({ seed }: { seed: Seed }) {
  const p = seed.plot_progress;
  if (!p || p.total === 0) return null;
  const rest = Math.max(0, p.total - p.done - p.growing - p.dormant - p.withered);
  const segments: Array<[string, number]> = [
    ['done', p.done - p.withered > 0 ? p.done - p.withered : p.done],
    ['growing', p.growing],
    ['parked', p.dormant],
    ['withered', p.withered],
    ['rest', rest],
  ];
  return (
    <div className="garden-progress" aria-hidden="true">
      {segments
        .filter(([, count]) => count > 0)
        .map(([name, count]) => (
          <span key={name} className={`garden-progress__seg is-${name}`} style={{ flexGrow: count }} />
        ))}
    </div>
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
  checkArtifactPath,
  viewToggle,
  frame,
  onToggleFrame,
  onEscapeFloor,
}: GardenPanelProps) {
  // The places walked into, root last. Empty is the garden itself.
  const trail = useGardenWalk((walk) => walk.trail);
  const setTrail = useGardenWalk((walk) => walk.setTrail);
  // The one filter state. Closed seeds stay out of it until a token asks for
  // them: the garden grows without bound and most of what it holds is done.
  const [query, setQuery] = useState('');
  // Where the search looks, when that is no longer where the reader stands.
  // Widening out of a plot is a property of the query, not a move — the trail
  // stays, so you can widen, look, and go back to what you were doing. Stored
  // as the plot it was asked for, so walking somewhere else drops it by
  // construction rather than by an effect that fires a frame late.
  const [wideIn, setWideIn] = useState<string | null>(null);
  // Where the arrows are, and which question they are walking. A new question
  // starts at its own best answer; keeping the answer's identity beside the
  // index is what stops one frame of the new results being drawn with the old
  // row highlighted.
  const [walk, setWalk] = useState<{ of: string; index: number }>({ of: '', index: 0 });
  const [seedDocument, setSeedDocument] = useState<SeedDocument | null>(null);
  const [documentError, setDocumentError] = useState<string | null>(null);
  const [titlePinned, setTitlePinned] = useState(false);
  const [trailOpen, setTrailOpen] = useState(false);
  const [panelWidth, setPanelWidth] = useState(0);
  const columnsRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  const index = useMemo(() => indexGarden(seeds), [seeds]);

  // Opening the trail is about the trail you are looking at. Moving gives you a
  // different one, so it folds again rather than staying open behind your back.
  useEffect(() => setTrailOpen(false), [trail.length]);

  // The panel sizes itself to its own box, not to the window: it is read in a
  // dock slot, in the window, and — while the two exchange places — at every
  // width in between. A callback ref rather than an effect, because the element
  // it has to observe is replaced when the renderer changes, and because the
  // measurement has to land in the same commit as the render that caused it or
  // the first paint is drawn at the wrong width.
  const measurePanel = useCallback((node: HTMLDivElement) => {
    setPanelWidth(node.clientWidth);
    const observer = new ResizeObserver(([entry]) => setPanelWidth(entry.contentRect.width));
    observer.observe(node);
    return () => observer.disconnect();
  }, []);
  const layout = panelWidth >= COLUMNS_MIN ? 'columns' : 'stack';

  // A crown can leave the garden while the reader stands inside its plot. Trim
  // the trail to what still exists rather than stranding them in a place that
  // is gone.
  const livingTrail = useMemo(() => {
    const alive: string[] = [];
    for (const id of trail) {
      if (!index.byID.has(id)) break;
      alive.push(id);
    }
    return alive;
  }, [trail, index]);

  const here = livingTrail.length > 0 ? index.byID.get(livingTrail[livingTrail.length - 1]) : undefined;
  const plotId = livingTrail.length > 0 ? livingTrail[livingTrail.length - 1] : null;
  const pageKey = livingTrail.length > 0 ? livingTrail.join('>') : 'root';

  const viewportRef = useRef<HTMLDivElement | null>(null);
  const headerRef = useRef<HTMLDivElement | null>(null);
  const pageKeyRef = useRef(pageKey);
  // Where the reader was on every place they have been. Losing this is losing
  // their place, which is the whole complaint this panel is answering. Shared
  // with the other size of this panel, for the same reason the trail is.
  const scrollMemory = useRef<Map<string, number>>(gardenScrollMemory);
  // How we arrived, and which row we came out of — the two facts the arrival
  // needs to feel like a movement rather than a swap.
  const arrival = useRef<{ direction: 'in' | 'out'; fromRow: string }>({ direction: 'in', fromRow: '' });

  const parsed = useMemo(() => parseQuery(query), [query]);
  // Two questions, not one. `filtering` is whether the query says anything at
  // all, and it drives the field's own state. `searching` is whether the query
  // is a search: text or a tender flattens the scope's whole subtree, while a
  // bare `is:` value only re-lenses the seeds already on screen.
  const filtering = parsed.active;
  const searching = parsed.searches;
  // Widening holds only while a search is running, and only in the plot it was
  // asked for: clearing the query or walking somewhere else puts the scope back
  // where the reader is, without anything having to notice and undo it.
  const wide = searching && wideIn !== null && wideIn === plotId;
  // What the arrows are walking. Two queries in two plots are two questions
  // even when the text is the same.
  const question = `${plotId ?? ''} ${query}`;
  const activeIndex = walk.of === question ? walk.index : 0;
  // The searchable garden, lowercased once per snapshot. Doing this per
  // keystroke is what makes client-side search feel slow; see the receipts in
  // gardenSearch.bench.ts.
  const entries = useMemo(
    () => buildIndex(seeds, { tenderOf, blockersOf: (seed: Seed) => index.blockers.get(seed.id) ?? 0 }),
    [seeds, index],
  );
  const entryByID = useMemo(() => {
    const map = new Map<string, SearchEntry>();
    for (const entry of entries) map.set(entry.seed.id, entry);
    return map;
  }, [entries]);

  // The lens the query names, applied to one level of the walk. The same
  // predicate the search uses, so `is:closed` means one thing in this panel and
  // not two.
  const lens = useCallback(
    (rows: Seed[]) =>
      rows.filter((seed) => {
        const entry = entryByID.get(seed.id);
        return entry ? satisfiesLens(entry, parsed.is) : !isClosed(seed);
      }),
    [entryByID, parsed],
  );

  // What a query searches: the plot's whole subtree, or the whole garden at the
  // root. Both are flat — searching is a list.
  const plotPool = useMemo(() => {
    if (!plotId) return entries;
    const wanted = subtreeOf(index, plotId);
    return entries.filter((entry) => wanted.has(entry.seed.id));
  }, [plotId, index, entries]);
  const pool = wide ? entries : plotPool;
  const results = useMemo(() => (searching ? searchGarden(pool, parsed) : []), [searching, pool, parsed]);
  const matchByID = useMemo(() => {
    const map = new Map<string, SeedMatch>();
    for (const match of results) map.set(match.seed.id, match);
    return map;
  }, [results]);
  // The two answers the panel owes a reader looking at fewer rows than they
  // expected: what a wider scope would find, and what the closed lens hides.
  // Both are counted, never implied.
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

  const rememberScroll = useCallback(() => {
    const el = viewportRef.current;
    if (el) scrollMemory.current.set(pageKeyRef.current, el.scrollTop);
  }, []);

  const drillInto = useCallback((id: string) => {
    rememberScroll();
    arrival.current = { direction: 'in', fromRow: '' };
    setTrail((prev) => [...prev, id]);
  }, [rememberScroll, setTrail]);

  const climbTo = useCallback((depth: number) => {
    rememberScroll();
    setTrail((prev) => {
      arrival.current = { direction: 'out', fromRow: prev[depth] ?? '' };
      return prev.slice(0, depth);
    });
  }, [rememberScroll, setTrail]);

  // Clicking in a column selects at that level: the trail is truncated to the
  // column you clicked in and your row becomes its new end. Drilling deeper and
  // switching siblings are the same gesture, which is what makes a column a
  // column rather than a list that happens to sit beside another one.
  const selectAtLevel = useCallback((level: number, id: string) => {
    setTrail((prev) => {
      arrival.current = { direction: prev.length > level ? 'out' : 'in', fromRow: '' };
      return [...prev.slice(0, level), id];
    });
  }, [setTrail]);

  // Picking an answer ends the question. The walk lands where the seed actually
  // lives, so the trail after a search reads the same as it would have read had
  // the reader found it by hand.
  const openResult = useCallback((id: string) => {
    rememberScroll();
    arrival.current = { direction: 'in', fromRow: '' };
    setTrail(pathTo(index, id));
    setQuery('');
    setWideIn(null);
  }, [index, rememberScroll, setTrail]);

  const climbOne = useCallback(() => {
    climbTo(Math.max(0, livingTrail.length - 1));
  }, [climbTo, livingTrail.length]);

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
  const toggleClosedLens = useCallback(() => {
    setQuery((cur) => toggleToken(cur, 'is:any'));
    focusSearch();
  }, [focusSearch]);

  // Escape goes down exactly one level: it clears the query, else climbs the
  // trail, else hands the garden down a frame. The stack is LIFO and each rung
  // pushes when its own condition turns on, so most of the time the ordering
  // comes for free — you type before you can clear, you walk in before you can
  // climb. Source order is what decides when two rungs arm in the SAME commit,
  // which is what reopening the garden onto a trail it already had does: the
  // floor is registered first so the climb lands above it.
  //
  // React runs child effects before parent ones, so the floor has to be
  // registered HERE rather than in GardenFrame: pushed from the frame it would
  // land above the climb and swallow it.
  useEscapeStack(onEscapeFloor ?? (() => {}), isOpen && !!onEscapeFloor);
  useEscapeStack(climbOne, isOpen && livingTrail.length > 0);
  useEscapeStack(() => setQuery(''), isOpen && query !== '');

  // Read the seed's log and artifact set on arrival, and again on every push:
  // notes and edges change the garden without changing the seed's revision.
  const hereId = here?.id ?? '';
  useEffect(() => {
    if (!isOpen || !hereId || !fetchSeedDocument) {
      setSeedDocument(null);
      setDocumentError(null);
      return;
    }
    let ignore = false;
    setDocumentError(null);
    fetchSeedDocument(hereId)
      .then((document) => {
        if (!ignore) setSeedDocument(document);
      })
      .catch((error) => {
        if (!ignore) setDocumentError(error instanceof Error ? error.message : `Could not read ${hereId}`);
      });
    return () => {
      ignore = true;
    };
  }, [hereId, fetchSeedDocument, isOpen, seeds]);

  // Arrow keys walk the rows of whichever place is open; enter opens the row
  // under focus (the button does that itself), left climbs. Focus is real DOM
  // focus so the ring is the browser's and screen readers follow it.
  const onPageKeyDown = useCallback((event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp' && event.key !== 'ArrowLeft') return;
    if (event.key === 'ArrowLeft') {
      if (livingTrail.length === 0) return;
      event.preventDefault();
      climbOne();
      return;
    }
    const el = viewportRef.current;
    if (!el) return;
    const rows = Array.from(el.querySelectorAll<HTMLElement>('[data-seed-row]'));
    if (rows.length === 0) return;
    event.preventDefault();
    const at = rows.indexOf(document.activeElement as HTMLElement);
    const next = event.key === 'ArrowDown'
      ? (at < 0 ? 0 : Math.min(rows.length - 1, at + 1))
      : (at < 0 ? rows.length - 1 : Math.max(0, at - 1));
    rows[next].focus({ preventScroll: true });
    rows[next].scrollIntoView({ block: 'nearest' });
  }, [climbOne, livingTrail.length]);

  // In columns the arrows mean what they look like: up and down walk the column
  // under focus, right goes into the row you are on, left comes back out. Enter
  // is the button's own click, so it needs no handler here.
  const onColumnsKeyDown = useCallback((event: React.KeyboardEvent<HTMLDivElement>) => {
    const keys = ['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight'];
    if (!keys.includes(event.key)) return;
    const active = document.activeElement as HTMLElement | null;
    // With nothing focused yet, the arrows walk the level the reader is
    // standing in — the deepest column — rather than doing nothing until
    // something is clicked.
    const walked = columnsRef.current?.querySelectorAll<HTMLElement>('[data-column]');
    const column =
      active?.closest<HTMLElement>('[data-column]') ?? (walked?.length ? walked[walked.length - 1] : null);

    if (event.key === 'ArrowLeft') {
      if (livingTrail.length === 0) return;
      event.preventDefault();
      climbOne();
      return;
    }
    if (event.key === 'ArrowRight') {
      if (!active?.dataset.seedRow) return;
      event.preventDefault();
      active.click();
      return;
    }
    if (!column) return;
    const rows = Array.from(column.querySelectorAll<HTMLElement>('[data-seed-row]'));
    if (rows.length === 0) return;
    event.preventDefault();
    const at = rows.indexOf(active as HTMLElement);
    const next = event.key === 'ArrowDown'
      ? (at < 0 ? 0 : Math.min(rows.length - 1, at + 1))
      : (at < 0 ? rows.length - 1 : Math.max(0, at - 1));
    rows[next].focus({ preventScroll: true });
    rows[next].scrollIntoView({ block: 'nearest' });
  }, [climbOne, livingTrail.length]);

  const resultSeeds = useMemo(() => results.map((match) => match.seed), [results]);
  const activeSeed = resultSeeds.length > 0 ? resultSeeds[Math.min(activeIndex, resultSeeds.length - 1)] : undefined;

  // The answers are walked from the search field itself — the reader never has
  // to leave it to pick one. With no answers to walk, the field is not holding
  // anything, so the arrows go where they would have gone anyway: into the
  // walk. Otherwise the keyboard is stranded in the field the moment a question
  // ends — which is exactly when picking an answer clears it.
  const onSearchKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      if (resultSeeds.length === 0) {
        if (layout === 'columns') onColumnsKeyDown(event as React.KeyboardEvent<HTMLDivElement>);
        else onPageKeyDown(event as React.KeyboardEvent<HTMLDivElement>);
        return;
      }
      event.preventDefault();
      const step = event.key === 'ArrowDown' ? 1 : -1;
      setWalk({ of: question, index: Math.min(resultSeeds.length - 1, Math.max(0, activeIndex + step)) });
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
      openResult(activeSeed.id);
    }
  };

  // The panel's whole keyboard, on the panel itself — it already carries the
  // role, and every row it walks is a descendant of it. `/` and ⌘F return to
  // the field from anywhere; `/` is only a shortcut where it is not a
  // character, so a note composer keeps it, and anything else typed into a
  // field belongs to that field.
  const onPanelKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement | null;
    const typing = target?.tagName === 'INPUT' || target?.tagName === 'TEXTAREA' || target?.isContentEditable;
    if ((event.key === 'f' && event.metaKey) || (event.key === '/' && !typing)) {
      event.preventDefault();
      event.stopPropagation();
      focusSearch();
      return;
    }
    if (typing) return;
    if (layout === 'columns') onColumnsKeyDown(event);
    else onPageKeyDown(event);
  };

  // Restore the reader's place, then hand focus back to the row they left. The
  // focus is deliberately scroll-free: the scroll offset above already put the
  // row where it was, and letting the browser scroll to it would undo that.
  useLayoutEffect(() => {
    pageKeyRef.current = pageKey;
    const el = viewportRef.current;
    if (!el) return;
    el.scrollTop = scrollMemory.current.get(pageKey) ?? 0;
    setTitlePinned(false);
    const { direction, fromRow } = arrival.current;
    if (direction === 'out' && fromRow) {
      const row = el.querySelector<HTMLElement>(`[data-seed-row="${fromRow}"]`);
      if (row) {
        row.focus({ preventScroll: true });
        return;
      }
    }
    el.focus({ preventScroll: true });
  }, [pageKey]);

  // One handler for both jobs the scroll does: hold the reader's place, and
  // hand the current title to the trail once the page has scrolled past it.
  // The state only ever changes on the crossing, so a scroll costs no renders.
  const onViewportScroll = useCallback(() => {
    const el = viewportRef.current;
    if (!el) return;
    scrollMemory.current.set(pageKeyRef.current, el.scrollTop);
    const header = headerRef.current;
    const pinned = Boolean(header) && el.scrollTop > header!.offsetTop + header!.clientHeight - 12;
    setTitlePinned((prev) => (prev === pinned ? prev : pinned));
  }, []);

  if (!isOpen) return null;

  const children = here ? index.children.get(here.id) ?? [] : [];
  // What the closed toggle would bring in, at the level the reader stands on.
  const levelSeeds = here ? children : index.roots;
  const closedHere = levelSeeds.reduce((n, seed) => (isClosed(seed) ? n + 1 : n), 0);
  // One closed control, in one place, in both renderers. Its count is what
  // pressing it does — how many rows it brings in, or how many it would take
  // away — and it stands down when the query names a lens of its own, because
  // then the query is already the statement and a toggle beside it would only
  // argue with it. It stays on screen while it is on, so the way in is also the
  // way out.
  const closedOn = parsed.is.includes('any');
  const otherLens = parsed.is.some((value) => value !== 'any');
  const closedCount = !searching
    ? closedHere
    : closedOn
      ? results.reduce((n, match) => (isClosed(match.seed) ? n + 1 : n), 0)
      : hiddenClosed;
  const closedToggle = otherLens || (!closedOn && closedCount === 0) ? null : { count: closedCount, on: closedOn };

  // The pushed snapshot paints the page; the fetched document adds the log and
  // the artifact set when it lands. Guard on identity so a stale answer for the
  // place behind never renders under the place in front.
  const seedDoc = seedDocument && here && seedDocument.seed.id === here.id ? seedDocument : null;
  const artifacts = seedDoc?.artifacts ?? [];
  const notes = seedDoc?.notes ?? [];
  const notesTotal = seedDoc?.notes_total ?? 0;
  const withheld = Math.max(0, notesTotal - notes.length);
  const spoken = notes.filter(isSpoken);
  const bookkeeping = notes.length - spoken.length;
  const relations = here ? relationsOf(index, here.id) : [];
  // The walk as list levels. Level 0 is the garden; level k is what is under
  // the seed selected at level k-1. The stack renderer draws the last one; the
  // columns renderer draws as many as fit beside the reader.
  const levels: { key: string; seeds: Seed[]; selectedId: string }[] = [
    { key: 'root', seeds: lens(index.roots), selectedId: livingTrail[0] ?? '' },
  ];
  for (let depth = 0; depth < livingTrail.length; depth++) {
    const parent = index.byID.get(livingTrail[depth]);
    const kids = parent ? index.children.get(parent.id) ?? [] : [];
    if (kids.length === 0) break;
    levels.push({
      key: livingTrail.slice(0, depth + 1).join('>'),
      seeds: lens(kids),
      selectedId: livingTrail[depth + 1] ?? '',
    });
  }
  // Width decides how many levels are on screen, not depth. The reader is the
  // point, so it keeps 560px before a column is allowed to exist and each column
  // costs 300 — which is why a five-deep walk looks like a two-deep one at the
  // same window size, and why widening the window shows you more of where you
  // came from instead of rearranging where you are.
  const maxColumns = panelWidth >= THIRD_COLUMN_MIN ? 3 : 2;
  const visibleLevels = levels.slice(Math.max(0, levels.length - maxColumns));
  const firstVisibleLevel = levels.length - visibleLevels.length;

  // What the trail carries is what the columns cannot. A visible column already
  // says which of its rows you picked, so repeating those steps in the trail
  // would state the same thing twice; the trail's steps start above the leftmost
  // column and stop before the place you are in. That is why the trail grows one
  // step per level of depth the window could not hold, and stays empty until it
  // has something to say. A search replaces the columns, so the trail goes back
  // to carrying the whole way in.
  const trailAncestors =
    layout === 'columns' && !searching
      ? livingTrail.slice(0, Math.min(firstVisibleLevel, livingTrail.length - 1))
      : livingTrail.slice(0, -1);
  // Past three steps the trail costs more room than it returns, so the middle
  // folds. Garden and the two steps nearest you are the ones a reader actually
  // aims at; the rest are one click away and never gone.
  const foldTrail = !trailOpen && trailAncestors.length > 3;
  const shownAncestors = foldTrail ? trailAncestors.slice(-2) : trailAncestors;
  const foldedCount = trailAncestors.length - shownAncestors.length;
  const where = plotId && !wide ? 'this plot' : 'the garden';

  const trailNav = (
    <div className="garden-chrome">
      <nav
        className={`garden-trail${wide ? ' is-wide' : ''}`}
        aria-label={wide ? 'Standing here, searching the whole garden' : 'The way back'}
      >
        {livingTrail.length === 0 ? (
          <span className="garden-trail__here">The garden</span>
        ) : (
          <>
            <button type="button" className="garden-trail__step" data-trail-depth="0" onClick={() => climbTo(0)}>
              Garden
            </button>
            {foldTrail && (
              <span className="garden-trail__seg">
                <span className="garden-trail__sep" aria-hidden="true">›</span>
                <button
                  type="button"
                  className="garden-trail__step garden-trail__fold"
                  onClick={() => setTrailOpen(true)}
                  aria-label={`Show ${foldedCount} more steps`}
                >
                  …
                </button>
              </span>
            )}
            {shownAncestors.map((id, offset) => {
              const depth = foldedCount + offset;
              return (
                <span key={id} className="garden-trail__seg">
                  <span className="garden-trail__sep" aria-hidden="true">›</span>
                  <button
                    type="button"
                    className="garden-trail__step"
                    data-trail-depth={depth + 1}
                    onClick={() => climbTo(depth + 1)}
                  >
                    {index.byID.get(id)?.title ?? id}
                  </button>
                </span>
              );
            })}
            {(titlePinned || searching) && here && (
              <span className="garden-trail__seg">
                <span className="garden-trail__sep" aria-hidden="true">›</span>
                <span className="garden-trail__here">{here.title}</span>
              </span>
            )}
          </>
        )}
      </nav>
      {viewToggle}
      {closedToggle && (
        // The way in and the way out are the same button. It writes `is:any`
        // into the query rather than keeping a flag beside it, so the line of
        // type stays the only filter state there is.
        <button
          type="button"
          className="garden-chrome__scope"
          aria-pressed={closedToggle.on}
          onClick={toggleClosedLens}
        >
          {closedToggle.on
            ? closedToggle.count > 0
              ? `hide ${closedToggle.count} closed`
              : 'hide closed'
            : `${closedToggle.count} closed`}
        </button>
      )}
      {/* The way between the two frames, quiet, beside the way out. */}
      {onToggleFrame && (
        <button
          type="button"
          className="garden-chrome__frame"
          onClick={onToggleFrame}
          aria-label={frame === 'full' ? 'Return the garden to the dock' : 'Expand the garden'}
          title={frame === 'full' ? 'Return to the dock (Esc)' : 'Expand (⌘⇧T)'}
        >
          <FrameGlyph direction={frame === 'full' ? 'in' : 'out'} />
        </button>
      )}
      <button type="button" className="garden-chrome__close" onClick={onClose} aria-label="Close">×</button>
    </div>
  );

  // Search and filters are one line and one state. The field is a line of type,
  // not a box: at list density a bordered control reads as chrome before it
  // reads as an invitation.
  const searchLine = (
    <div className={`garden-search${filtering ? ' is-active' : ''}`}>
      <span className="garden-search__glyph" aria-hidden="true">/</span>
      <input
        ref={inputRef}
        className="garden-search__input"
        type="text"
        role="combobox"
        aria-expanded={filtering}
        aria-controls="garden-results"
        aria-activedescendant={searching && activeSeed ? `garden-row-${activeSeed.id}` : undefined}
        aria-label={`Search ${where}`}
        placeholder="search — or is:ready, tender:…"
        spellCheck={false}
        autoComplete="off"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        onKeyDown={onSearchKeyDown}
      />
      <span className="garden-search__meta">
        {parsed.unknown.length > 0 ? (
          <span className="garden-search__unknown">no filter called {parsed.unknown.join(' ')}</span>
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
  );

  // A no-results state names the query and offers only the moves that would
  // actually find something.
  const nothingFound = (
    <div className="garden-nothing">
      <p className="garden-nothing__line">
        Nothing in {where} matches <span className="garden-nothing__echo">{query.trim()}</span>.
      </p>
      <ul className="garden-nothing__moves">
        {plotId && !wide && outside > 0 && (
          <li>
            <button type="button" onClick={toggleWide}>
              Search the whole garden <span className="garden-nothing__count">{outside}</span>
            </button>
            <kbd>⌥↵</kbd>
          </li>
        )}
        {plotId && wide && (
          <li>
            <button type="button" onClick={toggleWide}>
              Search this plot only <span className="garden-nothing__count">{inPlot}</span>
            </button>
            <kbd>⌥↵</kbd>
          </li>
        )}
        {hiddenClosed > 0 && (
          <li>
            <button type="button" onClick={toggleClosedLens}>
              Include closed seeds <span className="garden-nothing__count">{hiddenClosed}</span>
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
  );

  // The answer, as one flat ranked list. It replaces the walk's lists rather
  // than sitting beside them: browsing is a tree, searching is a list, and two
  // of them on screen at once would be two answers to one question.
  const resultsNode = resultSeeds.length === 0 ? nothingFound : (
    <SeedList
      seeds={resultSeeds}
      index={index}
      onOpen={openResult}
      activeId={activeSeed?.id}
      matchByID={matchByID}
      homes
      hereId={plotId}
      options
      listId="garden-results"
    />
  );

  // The seed itself. In columns its plot is already the column to the left, so
  // the reader does not repeat it — the same document either way, never the
  // same list twice.
  const readerNode = here ? (
    <>
      <div className="garden-head" ref={headerRef}>
        <div className="garden-head__row">
          <h2 className="garden-head__title">{here.title}</h2>
          <div className="garden-head__actions">
            {onOpenAsTile && (
              <button type="button" onClick={() => onOpenAsTile(here.id)}>Open as tile</button>
            )}
            {onResumeSeed && (here.tender_session || here.resume_session_id) && (
              <button type="button" data-testid={`seed-reopen-${here.id}`} onClick={() => onResumeSeed(here.id)}>
                Reopen agent
              </button>
            )}
          </div>
        </div>
        <div className="garden-head__meta">
          <span className={`garden-head__state ${statusClass(here.status)}`}>
            <span className="garden-row__pip" aria-hidden="true" />
            {here.status}
          </span>
          {tenderOf(here) && <span>tended by {tenderOf(here)}</span>}
          {here.planter_member && <span>by {crewDisplayName(here.planter_member)}</span>}
          <span>{formatPlantedAt(here.created_at)}</span>
          <span className="garden-head__id">{here.id}</span>
        </div>
        {/* Only a closed seed has one, so it is an exception by construction —
            and it is the one thing the reader of a closed seed came for. */}
        {here.reason && <p className="garden-head__reason">{here.reason}</p>}
        {isPlot(here) && (
          <div className="garden-head__progress">
            <ProgressBar seed={here} />
            <span>{progressWords(here)}</span>
          </div>
        )}
      </div>

      {here.body.trim() ? (
        <div className="garden-body">
          <MarkdownReader content={here.body} source={seedMarkdownSource(here.id)} allowLocalTargets={false} />
        </div>
      ) : (
        <p className="garden-note-empty">No body — the title is the whole seed.</p>
      )}

      {layout === 'stack' && isPlot(here) && (
        <section className="garden-section">
          <h3>Plot</h3>
          {lens(children).length === 0 && closedHere > 0 ? (
            // A blank list over a plot that holds finished work reads as lost
            // work, so it says where the finished work went.
            <p className="garden-empty">
              Nothing open here. {closedHere} closed {closedHere === 1 ? 'seed is' : 'seeds are'} behind the
              closed toggle above.
            </p>
          ) : (
            <SeedList
              seeds={lens(children)}
              index={index}
              onOpen={drillInto}
              emptyMessage={<>Nothing planted in this plot yet. <code>attn seed plant &quot;what this is&quot; --part-of {here.id}</code> puts something in it.</>}
            />
          )}
        </section>
      )}

      {relations.length > 0 && (
        <section className="garden-section">
          <h3>Related</h3>
          <ul className="garden-relations">
            {relations.map((relation) => (
              <li key={`${relation.label}:${relation.seed.id}`}>
                <span className="garden-relations__label">{relation.label}</span>
                <button type="button" onClick={() => drillInto(relation.seed.id)}>{relation.seed.title}</button>
                <span className="garden-relations__state">{relation.seed.status}</span>
              </li>
            ))}
          </ul>
        </section>
      )}

      {artifacts.length > 0 && (
        <section className="garden-section">
          <h3>Artifacts</h3>
          <SeedArtifactRows
            artifacts={artifacts}
            onOpenMarkdownArtifact={onOpenMarkdownArtifact}
            checkArtifactPath={checkArtifactPath}
          />
        </section>
      )}

      <section className="garden-section">
        <h3>Log</h3>
        {documentError ? (
          <p className="garden-note-empty garden-note-empty--error">{documentError}</p>
        ) : notes.length === 0 ? (
          <p className="garden-note-empty">Nothing on this seed’s log yet.</p>
        ) : (
          <>
            <ol className="garden-log">
              {spoken.map((note) => (
                <li key={note.id} data-kind={note.kind} className={note.kind === 'handoff' ? 'is-handoff' : ''}>
                  <div className="garden-log__head">
                    <span className="garden-log__who">{note.author_member || note.author_session || '—'}</span>
                    {note.kind !== 'note' && <span className="garden-log__kind">{note.kind}</span>}
                    <time dateTime={note.created_at} title={formatTimestamp(note.created_at)}>
                      {formatPlantedAt(note.created_at)}
                    </time>
                  </div>
                  {note.body && <Markdown className="garden-log__body" breaks>{note.body}</Markdown>}
                </li>
              ))}
            </ol>
            {bookkeeping > 0 && <BookkeepingDisclosure notes={notes.filter((note) => !isSpoken(note))} />}
          </>
        )}
        {withheld > 0 && (
          <p className="garden-note-empty">{withheld} more {withheld === 1 ? 'entry' : 'entries'} on the log.</p>
        )}
      </section>
    </>
  ) : null;

  const capped = seedsTotal > seeds.length && (
    <p className="garden-capped">
      {filtering
        ? `Search covers the newest ${seeds.length} of ${seedsTotal} seeds.`
        : `The garden holds ${seedsTotal} seeds; this panel has the newest ${seeds.length}.`}
    </p>
  );

  // ── Columns. The garden starts as one list and grows into Miller as you walk
  // into it: three empty panes would be a dead first frame, and at the root
  // there is nothing to put beside the list anyway.
  if (layout === 'columns') {
    const panes = searching ? 1 + (here ? 1 : 0) : visibleLevels.length + (here ? 1 : 0);
    return (
      <div ref={measurePanel} className="garden-panel is-columns" role="region" aria-label="The garden" onKeyDown={onPanelKeyDown}>
        {trailNav}
        {searchLine}
        <div
          className="garden-columns"
          data-panes={panes}
          data-mode={searching ? 'search' : 'walk'}
          ref={columnsRef}
        >
          {searching ? (
            <div className="garden-column garden-column--results">{resultsNode}</div>
          ) : (
            visibleLevels.map((level, offset) => (
              <ColumnList
                key={level.key}
                levelKey={level.key}
                seeds={level.seeds}
                index={index}
                selectedId={level.selectedId}
                memory={scrollMemory}
                onOpen={(id) => selectAtLevel(firstVisibleLevel + offset, id)}
              />
            ))
          )}
          {here && (
            <div
              className="garden-column garden-column--reader"
              ref={viewportRef}
              tabIndex={-1}
              onScroll={onViewportScroll}
            >
              <div className="garden-page" key={pageKey} data-arrival={arrival.current.direction}>
                {readerNode}
              </div>
            </div>
          )}
          {!here && capped}
        </div>
      </div>
    );
  }

  // ── Stack. One place at a time, which is all the dock has room for.
  return (
    <div ref={measurePanel} className="garden-panel" role="region" aria-label="The garden" onKeyDown={onPanelKeyDown}>
      {trailNav}
      {searchLine}
      <div
        className="garden-viewport"
        ref={viewportRef}
        tabIndex={-1}
        onScroll={onViewportScroll}
      >
        <div
          className="garden-page"
          key={searching ? 'results' : pageKey}
          data-arrival={arrival.current.direction}
        >
          {searching ? (
            <>
              {capped}
              {resultsNode}
            </>
          ) : !here ? (
            <>
              {capped}
              {lens(index.roots).length === 0 && closedHere > 0 ? (
                <p className="garden-empty">
                  Nothing open here. {closedHere} closed {closedHere === 1 ? 'seed is' : 'seeds are'} behind the
                  closed toggle above.
                </p>
              ) : (
                <SeedList
                  seeds={lens(index.roots)}
                  index={index}
                  onOpen={drillInto}
                  selectedId={livingTrail[0] ?? ''}
                  emptyMessage={<>The garden is empty. <code>attn seed plant &quot;what this is&quot;</code> puts something in it.</>}
                />
              )}
            </>
          ) : (
            readerNode
          )}
        </div>
      </div>
    </div>
  );
}
