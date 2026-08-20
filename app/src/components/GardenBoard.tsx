// app/src/components/GardenBoard.tsx
//
// The garden, as flow. The list answers "what is here"; the board answers "how
// is the work moving" without the reader opening anything.
//
// It is a PROJECTION, never a second state machine. A card's column is read off
// the seed the daemon already pushed — computed readiness, a live tender,
// dormancy, a closed status — so the board cannot disagree with `attn seed
// ready` or with the list beside it. Nothing here is stored.
//
// Drag is allowed only where a human owns the transition, and the legal moves
// are the garden's own five verbs, not a table invented here: hovering a column
// mid-drag splits it into one labeled zone per verb that would actually be
// accepted from the state the card is in. A pair with no legal verb grows no
// zone and the drop bounces. Every zone has a key path — Enter on a card opens
// the same verbs as a menu.
//
// The drag is pointer events, not HTML5 drag-and-drop. WebKit's dnd hands the
// gesture to a native drag session: it draws its own snapshot of the card, so
// the thing under the cursor is a screenshot rather than a designed object, and
// the gesture cannot be driven by anything but a human hand. Pointer events keep
// both — the carried chip is ours, and the same path a person walks is the one
// the harness walks.
//
// Prototype. See docs/plans/2026-08-20-garden-kanban-board-prototype.md.
import {
  Fragment,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type { PointerEvent as ReactPointerEvent, ReactNode } from 'react';
import type { Seed } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { crewHolderName } from '../utils/crewName';
import './GardenBoard.css';

export type ColumnKey = 'ready' | 'growing' | 'parked' | 'closed';

export interface GardenBoardProps {
  seeds: Seed[];
  seedsTotal: number;
  /** Sessions the daemon still knows, so a card can say its tender walked away. */
  liveSessions: Set<string>;
  /** False until the first daemon push lands — an empty board is not an empty garden. */
  loaded: boolean;
  /** Perform one real lifecycle move. Rejects with the daemon's own sentence. */
  onTransition: (seedId: string, verb: Verb, reason?: string, confirm?: boolean) => Promise<unknown>;
  /** Write on a seed's log — where park and replant put their sentence. */
  onNote: (seedId: string, body: string) => Promise<unknown>;
  /** The list/board switch, owned by the surface so both views show the same one. */
  viewToggle?: ReactNode;
  onClose: () => void;
}

export type Verb = 'park' | 'harvest' | 'wither' | 'replant' | 'dispatch';

// ---------------------------------------------------------------------------
// The projection
// ---------------------------------------------------------------------------

const CLOSED = new Set(['harvested', 'withered']);

// The chip sits below-right of the cursor, and flips to its left near the right
// edge so the last column — where Closed lives — can still be read while
// something is being dropped on it.
function carryTransform(point: { x: number; y: number }): string {
  const flip = point.x > window.innerWidth - 360;
  const dx = flip ? 'calc(-100% - 16px)' : '16px';
  return `translate3d(${point.x}px, ${point.y}px, 0) translate(${dx}, 12px)`;
}

// What is under the cursor mid-drag. The zones and the columns say who they are
// in the DOM, so this asks the rendered board rather than deciding legality a
// second time: a zone that is on screen is a zone this card may use.
function hitTest(x: number, y: number): { column: ColumnKey | null; verb: Verb | null } {
  const el = document.elementFromPoint(x, y);
  const zone = el?.closest('[data-zone]');
  const column = el?.closest('[data-column]');
  return {
    column: ((column as HTMLElement | null)?.dataset.column as ColumnKey) ?? null,
    verb: ((zone as HTMLElement | null)?.dataset.zone as Verb) ?? null,
  };
}

function crownOf(seed: Seed): string {
  return (seed.edges ?? []).find((edge) => edge.kind === 'part-of')?.to ?? '';
}

function tenderOf(seed: Seed): string {
  return crewHolderName(seed.tender_member, seed.tender_session);
}

// heldByOther names who still holds this seed, or '' when nobody does. It is
// garden.Tender.Holds read from the board's side: a claim signed by a session
// holds while that session is alive, and a crew member with no session always
// does, because attn has no signal that a person walked away.
//
// The board's own actor is unnamed, so anybody it names here is somebody else,
// and the move is a takeover the daemon refuses until the composer confirms it.
export function heldByOther(seed: Seed, liveSessions: Set<string>): string {
  if (!seed.tender_session && !seed.tender_member) return '';
  if (seed.tender_session && !liveSessions.has(seed.tender_session)) return '';
  return tenderOf(seed);
}

// columnOf reads a card's column off the seed. Four states, and every open seed
// lands somewhere: what is neither growing, parked nor closed is waiting to be
// picked up, whether or not anything is holding it back.
export function columnOf(seed: Seed): ColumnKey {
  if (CLOSED.has(seed.status)) return 'closed';
  if (seed.status === 'dormant') return 'parked';
  if (seed.status === 'growing') return 'growing';
  return 'ready';
}

// legalVerbs is the garden's own lifecycle table (internal/garden/lifecycle.go)
// read from the board's side: which verbs the target column owns, and which of
// them the seed's current state would accept.
//
// `replant` is the one move that lands on `planted`, so it is the only way back
// to Ready — from Closed, from Parked, and from Growing, which is how a seat is
// handed back without closing the work. The single absence left is Ready →
// Ready: a seed already in the pool has nowhere to be put back to.
export function legalVerbs(seed: Seed, target: ColumnKey): Verb[] {
  const status = seed.status;
  const open = status === 'planted' || status === 'growing' || status === 'dormant';
  switch (target) {
    case 'closed':
      return open ? ['harvest', 'wither'] : [];
    case 'parked':
      return status === 'planted' || status === 'growing' ? ['park'] : [];
    case 'ready':
      return status === 'planted' ? [] : ['replant'];
    case 'growing':
      // Dispatching is an intent, not a state: the agent claims the seed when it
      // starts. A seed already being worked has nobody to dispatch to.
      return status === 'planted' || status === 'dormant' ? ['dispatch'] : [];
  }
}

// The verbs a card offers on its own, which is every zone on the board unioned.
// The keyboard path and the drag path must never disagree, so they read the
// same table.
export function verbsFor(seed: Seed): Verb[] {
  const columns: ColumnKey[] = ['growing', 'parked', 'closed', 'ready'];
  return columns.flatMap((column) => legalVerbs(seed, column));
}

interface VerbSpec {
  label: string;
  // What the composer asks for, and whether it may be skipped.
  prompt: string;
  required: boolean;
  // A reason is stored on the seed by harvest and wither alone; the other moves
  // refuse one, so their sentence goes on the log instead.
  reasonOnSeed: boolean;
}

export const VERBS: Record<Verb, VerbSpec> = {
  harvest: {
    label: 'Harvest',
    prompt: 'what got done',
    required: true,
    reasonOnSeed: true,
  },
  wither: {
    label: 'Wither',
    prompt: 'why nobody should pick this up',
    required: false,
    reasonOnSeed: true,
  },
  park: {
    label: 'Park',
    prompt: 'what you are leaving it at',
    required: false,
    reasonOnSeed: false,
  },
  replant: {
    label: 'Replant',
    prompt: 'why it is open again',
    required: false,
    reasonOnSeed: false,
  },
  dispatch: {
    label: 'Dispatch an agent',
    prompt: '',
    required: false,
    reasonOnSeed: false,
  },
};

const COLUMNS: Array<{ key: ColumnKey; label: string }> = [
  { key: 'ready', label: 'Ready' },
  { key: 'growing', label: 'Growing' },
  { key: 'parked', label: 'Parked' },
  { key: 'closed', label: 'Closed' },
];

function ageOf(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const seconds = Math.round((Date.now() - t) / 1000);
  if (seconds < 60) return 'now';
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

// A crown's children, counted where they stand. The same phrasing the list view
// uses, so one plot reads the same in both views.
function plotCounts(seed: Seed): string {
  const p = seed.plot_progress;
  if (!p) return '';
  const parts: string[] = [];
  if (p.growing) parts.push(`${p.growing} growing`);
  if (p.ready) parts.push(`${p.ready} ready`);
  if (p.blocked) parts.push(`${p.blocked} blocked`);
  if (p.dormant) parts.push(`${p.dormant} parked`);
  parts.push(`${p.done}/${p.total} done`);
  return parts.join(' · ');
}

export function GardenBoard({
  seeds,
  seedsTotal,
  liveSessions,
  loaded,
  onTransition,
  onNote,
  viewToggle,
  onClose,
}: GardenBoardProps) {
  const [trail, setTrail] = useState<string[]>([]);
  const [closedOpen, setClosedOpen] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [dragging, setDragging] = useState<Seed | null>(null);
  const [dragPoint, setDragPoint] = useState<{ x: number; y: number } | null>(null);
  const [hover, setHover] = useState<ColumnKey | null>(null);
  const [zoneHover, setZoneHover] = useState<Verb | null>(null);
  const [compose, setCompose] = useState<{ seed: Seed; verb: Verb; column: ColumnKey } | null>(null);
  const [dispatchFor, setDispatchFor] = useState<Seed | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const composeInput = useRef<HTMLInputElement | null>(null);

  const byID = useMemo(() => {
    const map = new Map<string, Seed>();
    for (const seed of seeds) map.set(seed.id, seed);
    return map;
  }, [seeds]);

  // Blockers, counted over the whole garden: what holds a seed back can live in
  // another plot entirely.
  const blockers = useMemo(() => {
    const counts = new Map<string, number>();
    for (const seed of seeds) {
      if (CLOSED.has(seed.status)) continue;
      for (const edge of seed.edges ?? []) {
        if (edge.kind === 'blocks') counts.set(edge.to, (counts.get(edge.to) ?? 0) + 1);
      }
    }
    return counts;
  }, [seeds]);

  // A crown that left the garden takes the trail below it with it.
  const livingTrail = useMemo(() => {
    const alive: string[] = [];
    for (const id of trail) {
      if (!byID.has(id)) break;
      alive.push(id);
    }
    return alive;
  }, [trail, byID]);
  const plotId = livingTrail.length > 0 ? livingTrail[livingTrail.length - 1] : null;

  // What this level holds. At root that is the crowns and the seeds nothing
  // claims; inside a plot it is that crown's children — the same rule the list
  // view drills by, so the two views are never looking at different gardens.
  const scoped = useMemo(() => {
    if (!plotId) {
      return seeds.filter((seed) => {
        const parent = crownOf(seed);
        return !parent || !byID.has(parent);
      });
    }
    return seeds.filter((seed) => crownOf(seed) === plotId);
  }, [seeds, plotId, byID]);

  // Ready is ordered the way `attn seed ready` answers: what has waited longest
  // leads, and what is not ready yet follows it. Everywhere else the freshest
  // movement is on top.
  const columns = useMemo(() => {
    const out: Record<ColumnKey, Seed[]> = { ready: [], growing: [], parked: [], closed: [] };
    for (const seed of scoped) out[columnOf(seed)].push(seed);
    const newestFirst = (a: Seed, b: Seed) => Date.parse(b.updated_at) - Date.parse(a.updated_at);
    out.ready.sort((a, b) => {
      const pickable = (seed: Seed) => (seed.ready ? 0 : 1);
      if (pickable(a) !== pickable(b)) return pickable(a) - pickable(b);
      return Date.parse(a.created_at) - Date.parse(b.created_at);
    });
    out.growing.sort(newestFirst);
    out.parked.sort(newestFirst);
    out.closed.sort(newestFirst);
    return out;
  }, [scoped]);

  const readyCount = columns.ready.filter((seed) => seed.ready).length;
  const counts: Record<ColumnKey, number> = {
    ready: readyCount,
    growing: columns.growing.length,
    parked: columns.parked.length,
    closed: columns.closed.length,
  };

  // Every card on screen, column-major: what the arrows walk.
  const walkOrder = useMemo(() => {
    const order: Array<{ id: string; column: ColumnKey; row: number }> = [];
    for (const { key } of COLUMNS) {
      if (key === 'closed' && !closedOpen) continue;
      columns[key].forEach((seed, row) => order.push({ id: seed.id, column: key, row }));
    }
    return order;
  }, [columns, closedOpen]);

  useEffect(() => {
    if (selected && !byID.has(selected)) setSelected(null);
  }, [selected, byID]);

  useEffect(() => {
    if (compose) composeInput.current?.focus();
  }, [compose]);

  // Escape walks back out one layer at a time, above the surface's own close.
  useEscapeStack(() => setMenuFor(null), menuFor !== null);
  useEscapeStack(() => {
    setCompose(null);
    setError(null);
  }, compose !== null);
  useEscapeStack(() => setDispatchFor(null), dispatchFor !== null);

  const drillInto = useCallback((id: string) => {
    setSelected(null);
    setMenuFor(null);
    setTrail((prev) => [...prev, id]);
  }, []);
  const climbTo = useCallback((depth: number) => {
    setSelected(null);
    setMenuFor(null);
    setTrail((prev) => prev.slice(0, depth));
  }, []);

  // Escape climbs the trail one plot at a time. It registers only once you are
  // inside a plot, so at the top level the stack falls through to the surface's
  // own close and Escape leaves the garden.
  useEscapeStack(() => climbTo(livingTrail.length - 1), livingTrail.length > 0);

  const endDrag = useCallback(() => {
    setDragging(null);
    setDragPoint(null);
    setHover(null);
    setZoneHover(null);
  }, []);

  // One path for both the drag and the menu: a verb either opens its composer
  // or, for dispatch, the sheet that says what would happen.
  const beginVerb = useCallback((seed: Seed, verb: Verb, column: ColumnKey) => {
    setMenuFor(null);
    setError(null);
    endDrag();
    if (verb === 'dispatch') {
      setDispatchFor(seed);
      return;
    }
    setCompose({ seed, verb, column });
  }, [endDrag]);

  // Where the cursor is, in the board's own words. The zones carry their verb on
  // a data attribute so this reads the rendered truth rather than recomputing
  // the legality a second time — a zone that exists is a zone the card may use.
  const drag = useRef<{ seed: Seed; x: number; y: number; armed: boolean } | null>(null);
  const dragEndedAt = useRef(0);

  const beginPointerDrag = useCallback((seed: Seed, event: ReactPointerEvent<HTMLElement>) => {
    if (event.button !== 0) return;
    // WebKit leaves a clicked button unfocused; without this the focus ring and
    // the selection would disagree about where the reader is.
    (event.target as HTMLElement | null)?.closest?.('button')?.focus();
    drag.current = { seed, x: event.clientX, y: event.clientY, armed: false };

    const move = (ev: PointerEvent) => {
      const held = drag.current;
      if (!held) return;
      if (!held.armed) {
        // A press is a click until the hand means it. 5px is the smallest
        // movement that is not a tremor on a trackpad.
        if (Math.abs(ev.clientX - held.x) + Math.abs(ev.clientY - held.y) < 5) return;
        held.armed = true;
        setDragging(held.seed);
        setSelected(held.seed.id);
        setMenuFor(null);
      }
      setDragPoint({ x: ev.clientX, y: ev.clientY });
      const under = hitTest(ev.clientX, ev.clientY);
      setHover(under.column);
      setZoneHover(under.verb);
    };

    const release = (ev: PointerEvent) => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', release);
      window.removeEventListener('pointercancel', release);
      const held = drag.current;
      drag.current = null;
      if (!held?.armed) return;
      dragEndedAt.current = ev.timeStamp;
      const under = hitTest(ev.clientX, ev.clientY);
      if (under.column && under.verb) beginVerb(held.seed, under.verb, under.column);
      else endDrag();
    };

    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', release);
    window.addEventListener('pointercancel', release);
  }, [beginVerb, endDrag]);

  // Escape puts the card back down. The native drag had this for free.
  useEscapeStack(() => {
    drag.current = null;
    endDrag();
  }, dragging !== null);

  const commit = useCallback(async () => {
    if (!compose || busy) return;
    const text = (composeInput.current?.value ?? '').trim();
    const spec = VERBS[compose.verb];
    if (spec.required && !text) {
      setError(`${spec.label.toLowerCase()}ing ${compose.seed.id} records ${spec.prompt}`);
      composeInput.current?.focus();
      return;
    }
    setBusy(true);
    try {
      // park and replant refuse a reason — the daemon says so itself — so their
      // sentence is written on the log, and the move carries none.
      if (!spec.reasonOnSeed && text) await onNote(compose.seed.id, text);
      // A card somebody else still holds is taken, not moved, and the daemon
      // refuses it until the caller says so. The composer already said whose
      // work this is, so pressing commit is that answer.
      await onTransition(
        compose.seed.id,
        compose.verb,
        spec.reasonOnSeed ? text : undefined,
        heldByOther(compose.seed, liveSessions) !== '',
      );
      setCompose(null);
      setSelected(compose.seed.id);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, [compose, busy, liveSessions, onNote, onTransition]);

  const onKeyDown = (event: KeyboardEvent) => {
    if (compose || dispatchFor) return;
    // An open menu owns the arrows; the menu walks its own verbs.
    if (menuFor) return;
    const target = event.target as HTMLElement | null;
    if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) return;
    const key = event.key;
    if (walkOrder.length === 0) return;
    const at = walkOrder.findIndex((entry) => entry.id === selected);
    const here = at >= 0 ? walkOrder[at] : null;

    if (key === 'ArrowDown' || key === 'ArrowUp') {
      event.preventDefault();
      if (!here) {
        setSelected(walkOrder[0].id);
        return;
      }
      const inColumn = walkOrder.filter((entry) => entry.column === here.column);
      const next = inColumn[Math.min(inColumn.length - 1, Math.max(0, here.row + (key === 'ArrowDown' ? 1 : -1)))];
      setSelected(next.id);
      return;
    }
    if (key === 'ArrowRight' || key === 'ArrowLeft') {
      const seed = selected ? byID.get(selected) : null;
      // Right on a crown is the drill, exactly as in the list; left climbs out.
      if (key === 'ArrowRight' && seed?.plot_progress) {
        event.preventDefault();
        drillInto(seed.id);
        return;
      }
      if (key === 'ArrowLeft' && !here) {
        if (livingTrail.length > 0) {
          event.preventDefault();
          climbTo(livingTrail.length - 1);
        }
        return;
      }
      event.preventDefault();
      if (!here) {
        setSelected(walkOrder[0].id);
        return;
      }
      const lane = COLUMNS.filter(({ key: k }) => k !== 'closed' || closedOpen).map(({ key: k }) => k);
      const step = key === 'ArrowRight' ? 1 : -1;
      for (let i = lane.indexOf(here.column) + step; i >= 0 && i < lane.length; i += step) {
        const target = columns[lane[i]];
        if (target.length > 0) {
          setSelected(target[Math.min(here.row, target.length - 1)].id);
          return;
        }
      }
      if (key === 'ArrowLeft' && livingTrail.length > 0) climbTo(livingTrail.length - 1);
      return;
    }
    if (key === 'Enter' && selected) {
      event.preventDefault();
      setMenuFor((cur) => (cur === selected ? null : selected));
    }
  };

  // WebKit does not focus a button when it is clicked, so a handler on the
  // board's own element would go deaf the moment the mouse touched a card. The
  // board is a whole surface: it reads keys from the document while it is up,
  // and steps aside for anything being typed into.
  const keys = useRef(onKeyDown);
  useLayoutEffect(() => {
    keys.current = onKeyDown;
  });
  useEffect(() => {
    const listener = (event: KeyboardEvent) => keys.current(event);
    document.addEventListener('keydown', listener);
    return () => document.removeEventListener('keydown', listener);
  }, []);

  const capped = seedsTotal > seeds.length;
  const crown = plotId ? byID.get(plotId) : undefined;

  return (
    <div className="garden-board" role="region" aria-label="The garden board">
      <div className="garden-board__header">
        <span className="garden-board__kicker">The garden</span>
        <div className="garden-board__header-actions">
          {viewToggle}
          <span className="garden-board__total">{scoped.length}</span>
          <button type="button" className="garden-board__close" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>
      </div>

      {livingTrail.length > 0 && (
        <nav className="garden-board__trail" aria-label="Where you are">
          <button type="button" className="garden-board__trail-step" onClick={() => climbTo(0)}>
            Garden
          </button>
          {livingTrail.map((id, depth) => (
            <span key={id} className="garden-board__trail-segment">
              <span className="garden-board__trail-sep" aria-hidden="true">›</span>
              <button
                type="button"
                className="garden-board__trail-step"
                onClick={() => climbTo(depth + 1)}
                disabled={depth === livingTrail.length - 1}
              >
                {byID.get(id)?.title ?? id}
              </button>
            </span>
          ))}
          {crown && <span className="garden-board__trail-id">{crown.id}</span>}
        </nav>
      )}

      {error && (
        <p className="garden-board__error" role="alert">
          {error}
          <button type="button" onClick={() => setError(null)} aria-label="Dismiss">×</button>
        </p>
      )}

      {!loaded ? (
        <p className="garden-board__state">Reading the garden…</p>
      ) : seeds.length === 0 ? (
        <p className="garden-board__state">
          The garden is empty. <code>attn seed plant "what this is"</code> puts something in it.
        </p>
      ) : (
        <div className={`garden-board__columns${dragging ? ' is-dragging' : ''}`}>
          {COLUMNS.map(({ key, label }) => {
            const zones = dragging ? legalVerbs(dragging, key) : [];
            // A composer in a 132px column cannot be typed in, so the column
            // that is being written into opens for as long as that lasts.
            const collapsed = key === 'closed' && !closedOpen && compose?.column !== 'closed';
            const composing = compose?.column === key ? compose : null;
            return (
              <section
                key={key}
                className={[
                  'garden-board__column',
                  `is-${key}`,
                  collapsed ? 'is-collapsed' : '',
                  dragging && zones.length === 0 ? 'is-inert' : '',
                  hover === key && zones.length > 0 ? 'is-hovered' : '',
                ].filter(Boolean).join(' ')}
                aria-label={`${label}, ${counts[key]}`}
                data-column={key}
              >
                <header className="garden-board__head">
                  <h2 className="garden-board__label">{label}</h2>
                  <span className="garden-board__count">{counts[key]}</span>
                  {key === 'closed' && columns.closed.length > 0 && (
                    <button
                      type="button"
                      className="garden-board__reveal"
                      onClick={() => setClosedOpen((cur) => !cur)}
                      aria-expanded={closedOpen}
                    >
                      {closedOpen ? 'hide' : 'show'}
                    </button>
                  )}
                </header>

                <div className="garden-board__body">
                  {composing && (
                    <Composer
                      compose={composing}
                      takenFrom={heldByOther(composing.seed, liveSessions)}
                      busy={busy}
                      inputRef={composeInput}
                      onCommit={commit}
                      onCancel={() => {
                        setCompose(null);
                        setError(null);
                      }}
                    />
                  )}

                  {collapsed ? (
                    <ClosedSummary seeds={columns.closed} />
                  ) : columns[key].length === 0 ? (
                    <p className="garden-board__empty">{emptyLine(key, columns, scoped.length)}</p>
                  ) : (
                    <ul className="garden-board__cards">
                      {columns[key].map((seed, row) => (
                        <Fragment key={seed.id}>
                          {/* Ready holds everything nobody is working on, and
                              the two halves of that are different news. The
                              count in the header is what can be picked up; the
                              rest are named here rather than hidden, because a
                              board that drops work is worse than a long
                              column. */}
                          {key === 'ready' && row === readyCount && readyCount < columns.ready.length && (
                            <li className="garden-board__split" aria-hidden="true">
                              {columns.ready.length - readyCount} not ready yet
                            </li>
                          )}
                        <li>
                          <Card
                            seed={seed}
                            column={key}
                            selected={selected === seed.id}
                            menuOpen={menuFor === seed.id}
                            blockers={blockers.get(seed.id) ?? 0}
                            tenderLive={
                              !seed.tender_session || liveSessions.has(seed.tender_session)
                            }
                            onSelect={() => setSelected(seed.id)}
                            onPrimary={() => {
                              setSelected(seed.id);
                              if (seed.plot_progress) drillInto(seed.id);
                              else setMenuFor((cur) => (cur === seed.id ? null : seed.id));
                            }}
                            onDrill={() => drillInto(seed.id)}
                            onMenu={() => setMenuFor((cur) => (cur === seed.id ? null : seed.id))}
                            onVerb={(verb) => beginVerb(seed, verb, targetColumn(verb))}
                            onDragPointerDown={(event) => beginPointerDrag(seed, event)}
                            wasDragged={() => performance.now() - dragEndedAt.current < 250}
                          />
                        </li>
                        </Fragment>
                      ))}
                    </ul>
                  )}

                  {/* The zones. They exist only while a card is over this
                      column and only for the verbs its state would accept, so
                      the board can never offer a move the daemon would refuse. */}
                  {hover === key && zones.length > 0 && dragging && (
                    <div className="garden-board__zones">
                      {zones.map((verb) => (
                        <button
                          key={verb}
                          type="button"
                          className={`garden-board__zone${zoneHover === verb ? ' is-over' : ''}`}
                          data-zone={verb}
                          onClick={() => beginVerb(dragging, verb, key)}
                        >
                          <span className="garden-board__zone-verb">{VERBS[verb].label}</span>
                          <span className="garden-board__zone-id">{dragging.id}</span>
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              </section>
            );
          })}
        </div>
      )}

      {/* What the hand is carrying. Ours, not WebKit's snapshot: the title to
          know what it is, the id to know which one. */}
      {dragging && dragPoint && (
        <div
          className="garden-board__carry"
          style={{ transform: carryTransform(dragPoint) }}
          aria-hidden="true"
        >
          <span className="garden-board__carry-title">{dragging.title}</span>
          <span className="garden-board__carry-id">{dragging.id}</span>
        </div>
      )}

      {capped && (
        <p className="garden-board__capped">
          The garden holds {seedsTotal} seeds; this board has the newest {seeds.length}.
        </p>
      )}

      {dispatchFor && <DispatchSheet seed={dispatchFor} crown={byID.get(crownOf(dispatchFor))} onClose={() => setDispatchFor(null)} />}
    </div>
  );
}

// targetColumn is where a verb lands a card, which is the column whose zone
// offered it.
function targetColumn(verb: Verb): ColumnKey {
  switch (verb) {
    case 'harvest':
    case 'wither':
      return 'closed';
    case 'park':
      return 'parked';
    case 'replant':
      return 'ready';
    case 'dispatch':
      return 'growing';
  }
}

// emptyLine says something true about why a column is empty. "No cards" is a
// tautology; a reader wants to know whether that is good news.
function emptyLine(key: ColumnKey, columns: Record<ColumnKey, Seed[]>, here: number): string {
  if (here === 0) return 'Nothing planted here yet.';
  switch (key) {
    case 'ready':
      if (columns.growing.length > 0) return 'Nothing to pick up — everything open is being worked.';
      if (columns.parked.length > 0) return `Nothing to pick up. ${columns.parked.length} parked.`;
      return 'Nothing to pick up here.';
    case 'growing':
      return 'Nobody is working on anything here.';
    case 'parked':
      return 'Nothing put down.';
    case 'closed':
      return 'Nothing has finished here yet.';
  }
}

function ClosedSummary({ seeds }: { seeds: Seed[] }) {
  const harvested = seeds.filter((seed) => seed.status === 'harvested').length;
  const withered = seeds.length - harvested;
  if (seeds.length === 0) return <p className="garden-board__empty">Nothing has finished here yet.</p>;
  return (
    <p className="garden-board__summary">
      <span>{harvested} harvested</span>
      {withered > 0 && <span className="is-withered">{withered} withered</span>}
    </p>
  );
}

interface CardProps {
  seed: Seed;
  column: ColumnKey;
  selected: boolean;
  menuOpen: boolean;
  blockers: number;
  tenderLive: boolean;
  onSelect: () => void;
  onPrimary: () => void;
  onDrill: () => void;
  onMenu: () => void;
  onVerb: (verb: Verb) => void;
  onDragPointerDown: (event: ReactPointerEvent<HTMLElement>) => void;
  /** True just after a drag ended on this card, so the trailing click is not a click. */
  wasDragged: () => boolean;
}

function Card({
  seed, column, selected, menuOpen, blockers, tenderLive,
  onSelect, onPrimary, onDrill, onMenu, onVerb, onDragPointerDown, wasDragged,
}: CardProps) {
  const verbs = verbsFor(seed);
  const plot = seed.plot_progress ? plotCounts(seed) : '';
  const menu = useRef<HTMLDivElement | null>(null);
  // An opened menu takes the focus, or Enter opened something the hand cannot
  // reach without the mouse it was meant to replace.
  useEffect(() => {
    if (!menuOpen) return;
    menu.current?.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus();
  }, [menuOpen]);
  return (
    <div
      className={[
        'garden-card',
        selected ? 'is-selected' : '',
        seed.plot_progress ? 'is-crown' : '',
        column === 'closed' ? `is-${seed.status}` : '',
      ].filter(Boolean).join(' ')}
      data-seed={seed.id}
      onPointerDown={onDragPointerDown}
    >
      <button
        type="button"
        className="garden-card__body"
        aria-expanded={menuOpen}
        onFocus={onSelect}
        onClick={() => {
          if (wasDragged()) return;
          onPrimary();
        }}
      >
        <span className="garden-card__title">{seed.title}</span>
        <span className="garden-card__meta">
          <CardMeta seed={seed} column={column} blockers={blockers} tenderLive={tenderLive} plot={plot} />
          <span className="garden-card__id">{seed.id}</span>
          <span className="garden-card__age">{ageOf(seed.updated_at || seed.created_at)}</span>
        </span>
      </button>
      {seed.plot_progress && (
        <button type="button" className="garden-card__drill" onClick={onDrill} aria-label={`Open the plot under ${seed.title}`}>
          ›
        </button>
      )}
      {menuOpen && (
        <div
          className="garden-card__menu"
          role="menu"
          ref={menu}
          onKeyDown={(event) => {
            if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
            // The board's own arrows walk cards; inside an open menu they walk
            // the verbs, so one key means one thing at a time.
            event.preventDefault();
            event.stopPropagation();
            const items = Array.from(
              event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'),
            );
            const at = items.indexOf(document.activeElement as HTMLButtonElement);
            const step = event.key === 'ArrowDown' ? 1 : -1;
            items[Math.min(items.length - 1, Math.max(0, at + step))]?.focus();
          }}
        >
          {verbs.length === 0 ? (
            <p className="garden-card__menu-empty">Nothing moves a {seed.status} seed from here.</p>
          ) : (
            verbs.map((verb) => (
              <button key={verb} type="button" role="menuitem" onClick={() => onVerb(verb)}>
                {VERBS[verb].label}
                {verb !== 'dispatch' && <span aria-hidden="true">…</span>}
              </button>
            ))
          )}
        </div>
      )}
      {menuOpen && verbs.length > 0 && <span className="garden-card__menu-hint">esc</span>}
      {!menuOpen && selected && verbs.length > 0 && (
        <button type="button" className="garden-card__more" onClick={onMenu} aria-label={`Move ${seed.title}`}>
          ⏎
        </button>
      )}
    </div>
  );
}

// What the card says under its title. One line, and it changes by column
// because a reader asks a different question of each one.
function CardMeta({
  seed, column, blockers, tenderLive, plot,
}: { seed: Seed; column: ColumnKey; blockers: number; tenderLive: boolean; plot: string }) {
  if (plot) return <span className="garden-card__plot">{plot}</span>;
  switch (column) {
    case 'ready':
      if (seed.ready) return <span className="garden-card__ready">ready</span>;
      if (blockers > 0) return <span className="garden-card__held">blocked by {blockers}</span>;
      if (seed.template) return <span className="garden-card__held">packet</span>;
      if (seed.gate) return <span className="garden-card__held">gate</span>;
      return <span className="garden-card__held">not ready</span>;
    case 'growing':
      return (
        <span className={`garden-card__tender${tenderLive ? '' : ' is-gone'}`}>
          {tenderOf(seed) || 'held'}
          {!tenderLive && <span className="garden-card__gone"> · session gone</span>}
        </span>
      );
    case 'parked':
      return <span className="garden-card__held">parked</span>;
    case 'closed':
      return (
        <span className={`garden-card__closed is-${seed.status}`}>
          {seed.reason ? seed.reason : seed.status}
        </span>
      );
  }
}

function Composer({
  compose, takenFrom, busy, inputRef, onCommit, onCancel,
}: {
  compose: { seed: Seed; verb: Verb; column: ColumnKey };
  // Who still holds this seed, when that is somebody. The line it draws is the
  // board's --confirm: there is no way to commit without having read it.
  takenFrom: string;
  busy: boolean;
  inputRef: React.RefObject<HTMLInputElement | null>;
  onCommit: () => void;
  onCancel: () => void;
}) {
  const spec = VERBS[compose.verb];
  return (
    <div className="garden-compose">
      <span className="garden-compose__verb">{spec.label.toLowerCase()}</span>
      <span className="garden-compose__id">{compose.seed.id}</span>
      <input
        ref={inputRef}
        className="garden-compose__input"
        type="text"
        autoComplete="off"
        spellCheck={false}
        disabled={busy}
        placeholder={spec.required ? spec.prompt : `${spec.prompt} — optional`}
        aria-label={`${spec.label} ${compose.seed.id}: ${spec.prompt}`}
        onKeyDown={(event) => {
          event.stopPropagation();
          if (event.key === 'Enter') {
            event.preventDefault();
            onCommit();
          }
          if (event.key === 'Escape') {
            event.preventDefault();
            onCancel();
          }
        }}
      />
      <span className="garden-compose__keys">
        <kbd>⏎</kbd>
        <kbd>esc</kbd>
      </span>
      {/* The one line that says where the words go. park and replant store no
          reason, so theirs lands on the log — which is what the daemon's own
          refusal tells a caller to do. */}
      {!spec.reasonOnSeed && <span className="garden-compose__where">goes on the log</span>}
      {takenFrom !== '' && (
        <span className="garden-compose__taking">takes it from {takenFrom}</span>
      )}
    </div>
  );
}

// The dispatch stub. Dragging onto Growing is an intent — an agent claims the
// seed itself when it starts — so the prototype shows exactly what would be
// handed over and moves nothing.
function DispatchSheet({ seed, crown, onClose }: { seed: Seed; crown?: Seed; onClose: () => void }) {
  return (
    <div className="garden-sheet" role="dialog" aria-modal="true" aria-label="Dispatch an agent">
      <div className="garden-sheet__panel">
        <header>
          <span className="garden-sheet__kicker">Would dispatch</span>
          <button type="button" onClick={onClose} aria-label="Close">×</button>
        </header>
        <p className="garden-sheet__title">{seed.title}</p>
        <dl className="garden-sheet__rows">
          <div>
            <dt>seed</dt>
            <dd className="is-mono">{seed.id}</dd>
          </div>
          <div>
            <dt>plot</dt>
            <dd>{crown ? `${crown.title} (${crown.id})` : 'the whole garden'}</dd>
          </div>
          <div>
            <dt>brief</dt>
            <dd>the seed's body, handed over as the agent's task</dd>
          </div>
          <div>
            <dt>claim</dt>
            <dd>the agent tends {seed.id} when it starts — the board never tends for it</dd>
          </div>
        </dl>
        <code className="garden-sheet__command">attn delegate --seed {seed.id}</code>
        <p className="garden-sheet__note">Nothing was dispatched. This is the prototype's stub.</p>
      </div>
    </div>
  );
}
