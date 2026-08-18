import './PaneSeedChip.css';
import type { Seed } from '../hooks/useDaemonSocket';

// A clickable chip in the agent pane header (.workspace-pane-header) for the
// seed this session reports to. It carries a status-tinted rule (via
// data-status, reusing the garden panel's DEFINED-token tint pattern — no bare
// --color-accent/--color-error), the truncated seed title, and an unread dot
// when the session has activity the user has not seen. Clicking it opens the
// seed as a tile — the one annotated reading surface (epic ruling A) — rather
// than a second reader inside the pane.
//
// The title falls back to the id: the session names its seed on the wire, and
// the garden push it would be titled from can lag or be capped. A chip that
// vanished until the list caught up would read as "this agent reports to
// nothing", which is the one thing it must never say.
//
// Like the sibling rename/nudge buttons it guards the pane header's leaf-drag:
// onPointerDown stops propagation so a sloppy click that drifts >=4px cannot
// relocate the pane instead of opening the seed, and onClick stops the header
// pointer chain from re-selecting the pane.
export function PaneSeedChip({
  seedId,
  seed,
  unread,
  sessionId,
  onOpen,
}: {
  seedId: string;
  seed?: Seed;
  unread: boolean;
  sessionId: string;
  onOpen: () => void;
}) {
  const label = seed?.title || seedId;
  return (
    <button
      type="button"
      className="pane-seed-chip"
      data-status={seed?.status}
      data-testid={`seed-chip-${sessionId}`}
      onPointerDown={(event) => event.stopPropagation()}
      onClick={(event) => {
        event.stopPropagation();
        onOpen();
      }}
      title={`${label} (${seedId}) — click to open the seed`}
    >
      <span className="pane-seed-chip-rule" aria-hidden="true" />
      <span className="pane-seed-chip-title">{label}</span>
      {unread ? (
        <span
          className="pane-seed-chip-unread"
          data-testid={`seed-chip-unread-${sessionId}`}
          aria-label="Unread activity"
        />
      ) : null}
    </button>
  );
}
