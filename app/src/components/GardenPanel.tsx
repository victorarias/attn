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
  // The workspace the user is looking at; null on the dashboard, where the only
  // honest scope is the whole garden.
  workspaceId: string | null;
}

// formatPlantedAt renders an RFC3339 created_at as a short relative phrase.
// Returns '' for an unparseable value rather than printing a broken date.
export function formatPlantedAt(iso: string): string {
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

export function GardenPanel({ isOpen, onClose, seeds, workspaceId }: GardenPanelProps) {
  // Whole-garden view. Sticky across workspace switches on purpose: it is a
  // reading mode, not a property of the workspace.
  const [showAll, setShowAll] = useState(false);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const scoped = useMemo(() => {
    if (showAll || !workspaceId) return seeds;
    return seeds.filter((seed) => seed.workspace_id === workspaceId);
  }, [seeds, showAll, workspaceId]);

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
                  <span className="garden-seed__title">{seed.title}</span>
                  <span className="garden-seed__id">{seed.id}</span>
                  <span className="garden-seed__planted">{formatPlantedAt(seed.created_at)}</span>
                </button>
                {expanded && (
                  <div className="garden-seed__detail">
                    <div className="garden-seed__meta">
                      <span>{seed.status}</span>
                      {seed.planter_member && <span>planted by {seed.planter_member}</span>}
                      {seed.tender_member && <span>tended by {seed.tender_member}</span>}
                      {seed.template && <span>packet</span>}
                      {seed.gate && <span>gate</span>}
                    </div>
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
