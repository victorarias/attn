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
  fetchSeedDocument?: (seedId: string) => Promise<SeedDocument>;
  onOpenAsTile?: (seedId: string) => void;
  onOpenMarkdownArtifact?: (path: string) => void;
  checkArtifactPath?: (path: string) => Promise<boolean>;
  onResumeSeed?: (seedId: string) => void;
  onClose: () => void;
}

/**
 * Fullscreen shell for the garden. The garden is a primary surface (opened from
 * the sidebar / ⌘K / ⌘⇧T), not a right-dock peek, so it gets the whole window —
 * and the whole window is what columns need. Same panel and the same trail as
 * the dock, drawn as Miller columns instead of a stack: at this width the two
 * list levels cost nothing the reader was using, and depth stops needing to be
 * remembered because it is on screen. Over the notebook surface pattern: fixed
 * inset-0, FocusTrap, shared Escape stack.
 */
export function GardenSurface({
  isOpen,
  seeds,
  seedsTotal,
  fetchSeedDocument,
  onOpenAsTile,
  onOpenMarkdownArtifact,
  checkArtifactPath,
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
            layout="columns"
            isOpen
            seeds={seeds}
            seedsTotal={seedsTotal}
            fetchSeedDocument={fetchSeedDocument}
            onOpenAsTile={onOpenAsTile}
            onOpenMarkdownArtifact={onOpenMarkdownArtifact}
            checkArtifactPath={checkArtifactPath}
            onResumeSeed={onResumeSeed}
            onClose={onClose}
          />
        </div>
      </FocusTrap>
    </div>
  );
}
