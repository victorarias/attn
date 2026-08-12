// app/src/components/GardenPanel.tsx
//
// The garden, seen. Slice 1's whole contract is that a seed planted from a
// terminal appears here without the user touching anything, so this panel holds
// no fetch and no refresh: it renders the seeds the daemon pushed
// (garden_seeds_updated, and initial_state on connect) and re-renders when they
// change.
//
// Scope is the workspace being looked at, because that is the question the panel
// answers — what is planted here. The push carries the whole garden, so the
// scope toggle costs no round trip.
import { useMemo, useState } from 'react';
import type { Seed } from '../hooks/useDaemonSocket';
import './GardenPanel.css';

interface GardenPanelProps {
  isOpen: boolean;
  onClose: () => void;
  seeds: Seed[];
  // How many seeds the garden holds. Larger than seeds.length only when the
  // garden outgrew one push, and then the panel says so: a list that ends at a
  // cap without saying it reads as the whole garden.
  seedsTotal: number;
  // The workspace the user is looking at; null on the dashboard, where the only
  // honest scope is the whole garden.
  workspaceId: string | null;
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
}

// indexEdges reads the whole pushed garden once. Edges are stored on the seed
// they point from, so the reverse direction is only knowable by walking every
// seed — per row that is a scan per row, and this panel renders the garden.
//
// The index is built over the unscoped push on purpose: a blocker can live in
// another workspace, and a panel that only looked at the workspace it shows
// would tell a blocked seed it is blocked by nothing.
function indexEdges(seeds: Seed[]): GardenIndex {
  const index: GardenIndex = { byID: new Map(), inbound: new Map(), blockers: new Map() };
  for (const seed of seeds) index.byID.set(seed.id, seed);
  for (const seed of seeds) {
    for (const edge of seed.edges ?? []) {
      const label = edge.kind === 'blocks' ? 'blocked by' : edge.kind === 'part-of' ? 'has part' : '';
      if (!label) continue;
      index.inbound.set(edge.to, [...(index.inbound.get(edge.to) ?? []), { label, seed }]);
      if (edge.kind === 'blocks' && !closedStatus(seed.status)) {
        index.blockers.set(edge.to, (index.blockers.get(edge.to) ?? 0) + 1);
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
  return seed.tender_member || seed.tender_session || '';
}

export function GardenPanel({ isOpen, onClose, seeds, seedsTotal, workspaceId }: GardenPanelProps) {
  // Whole-garden view. Sticky across workspace switches on purpose: it is a
  // reading mode, not a property of the workspace.
  const [showAll, setShowAll] = useState(false);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const scoped = useMemo(() => {
    if (showAll || !workspaceId) return seeds;
    return seeds.filter((seed) => seed.workspace_id === workspaceId);
  }, [seeds, showAll, workspaceId]);

  const index = useMemo(() => indexEdges(seeds), [seeds]);

  if (!isOpen) return null;

  const scopedToWorkspace = !showAll && !!workspaceId;

  return (
    <div className="garden-panel" role="region" aria-label="The garden">
      <div className="garden-panel__header">
        <span className="garden-panel__kicker">The garden</span>
        <div className="garden-panel__header-actions">
          {workspaceId && (
            <button
              type="button"
              className="garden-panel__scope"
              onClick={() => setShowAll((prev) => !prev)}
            >
              {scopedToWorkspace ? 'This workspace' : 'Whole garden'}
            </button>
          )}
          <span className="garden-panel__count">{scoped.length}</span>
          <button type="button" className="garden-panel__close" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>
      </div>

      {seedsTotal > seeds.length && (
        <p className="garden-panel__capped">
          The garden holds {seedsTotal} seeds; this panel has the newest {seeds.length}.
        </p>
      )}

      {scoped.length === 0 ? (
        <p className="garden-panel__empty">
          {scopedToWorkspace
            ? 'Nothing planted in this workspace yet.'
            : 'The garden is empty.'}
          {' '}
          <code>attn seed plant "what this is"</code> puts something in it.
        </p>
      ) : (
        <ul className="garden-panel__list">
          {scoped.map((seed) => {
            const expanded = expandedId === seed.id;
            const blockers = index.blockers.get(seed.id) ?? 0;
            const relations = expanded ? relationsOf(index, seed.id) : [];
            return (
              <li
                key={seed.id}
                className={`garden-seed ${statusClass(seed.status)}${expanded ? ' is-expanded' : ''}`}
              >
                <button
                  type="button"
                  className="garden-seed__head"
                  onClick={() => setExpandedId((cur) => (cur === seed.id ? null : seed.id))}
                >
                  <span className="garden-seed__status" aria-hidden="true" />
                  <span className="garden-seed__main">
                    <span className="garden-seed__title">{seed.title}</span>
                    <span className="garden-seed__line">
                      <span className="garden-seed__state">{seed.status}</span>
                      {seed.ready && <span className="garden-seed__ready">ready</span>}
                      {blockers > 0 && (
                        <span className="garden-seed__blocked">
                          blocked by {blockers}
                        </span>
                      )}
                      {tenderOf(seed) && (
                        <span className="garden-seed__tender">tended by {tenderOf(seed)}</span>
                      )}
                      <span className="garden-seed__id">{seed.id}</span>
                      <span className="garden-seed__planted">{formatPlantedAt(seed.created_at)}</span>
                    </span>
                  </span>
                </button>
                {expanded && (
                  <div className="garden-seed__detail">
                    <div className="garden-seed__meta">
                      <span>{seed.status}</span>
                      {seed.planter_member && <span>planted by {seed.planter_member}</span>}
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
                            <span className="garden-seed__relation-title">{relation.seed.title}</span>
                            <span className="garden-seed__id">{relation.seed.id}</span>
                            <span className="garden-seed__relation-state">{relation.seed.status}</span>
                          </li>
                        ))}
                      </ul>
                    )}
                    {seed.body ? (
                      <pre className="garden-seed__body">{seed.body}</pre>
                    ) : (
                      <p className="garden-seed__nobody">No body — the title is the whole seed.</p>
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
