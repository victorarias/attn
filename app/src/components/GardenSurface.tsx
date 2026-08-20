import FocusTrap from 'focus-trap-react';
import type { Seed } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { GardenPanel } from './GardenPanel';
import type { SeedDocument } from './SeedDocumentView';
import './GardenSurface.css';

export interface GardenSurfaceProps {
  isOpen: boolean;
  seeds: Seed[];
  seedsTotal: number;
  strandedTickets?: number;
  fetchSeedDocument?: (seedId: string) => Promise<SeedDocument>;
  onOpenAsTile?: (seedId: string) => void;
  onOpenMarkdownArtifact?: (path: string) => void;
  onResumeSeed?: (seedId: string) => void;
  onClose: () => void;
}

/**
 * Fullscreen shell for the garden. The garden is a primary surface (opened from
 * the sidebar / ⌘K / ⌘⇧T), not a right-dock peek, so it gets the whole window:
 * room for a plot's whole crown and for a drilled seed's body beside its log,
 * without the dock's ~34vw squeeze. Same panel as the dock — one reader, two
 * sizes — over the notebook surface pattern: fixed inset-0, FocusTrap, shared
 * Escape stack.
 */
export function GardenSurface({
  isOpen,
  seeds,
  seedsTotal,
  strandedTickets,
  fetchSeedDocument,
  onOpenAsTile,
  onOpenMarkdownArtifact,
  onResumeSeed,
  onClose,
}: GardenSurfaceProps) {
  useEscapeStack(onClose, isOpen);
  if (!isOpen) return null;
  return (
    <div className="garden-surface-shell" data-testid="garden-surface">
      <FocusTrap
        focusTrapOptions={{
          escapeDeactivates: false,
          clickOutsideDeactivates: false,
          initialFocus: false,
          fallbackFocus: '.garden-surface',
        }}
      >
        <div className="garden-surface" role="dialog" aria-modal="true" aria-label="The garden" tabIndex={-1}>
          <GardenPanel
            isOpen
            seeds={seeds}
            seedsTotal={seedsTotal}
            strandedTickets={strandedTickets}
            fetchSeedDocument={fetchSeedDocument}
            onOpenAsTile={onOpenAsTile}
            onOpenMarkdownArtifact={onOpenMarkdownArtifact}
            onResumeSeed={onResumeSeed}
            onClose={onClose}
          />
        </div>
      </FocusTrap>
    </div>
  );
}
