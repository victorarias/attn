import { useState } from 'react';
import FocusTrap from 'focus-trap-react';
import type { Seed } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { GardenBoard, type Verb } from './GardenBoard';
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
  /** Sessions the daemon still knows — how a board card says its tender left. */
  liveSessions?: Set<string>;
  /** False until the first push lands; an empty garden and an unread one differ. */
  loaded?: boolean;
  /** One real lifecycle move, for the board's drop zones and verb menu. */
  moveSeed?: (seedId: string, verb: Verb, reason?: string) => Promise<unknown>;
  /** Write on a seed's log. */
  noteSeed?: (seedId: string, body: string) => Promise<unknown>;
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
 *
 * Two views over one garden: the list answers what is here, the board answers
 * how it is moving. The switch lives here rather than in either view, so
 * neither owns the other. Prototype placement — see DESIGN-NOTE.md.
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
  liveSessions,
  loaded = true,
  moveSeed,
  noteSeed,
  onClose,
}: GardenSurfaceProps) {
  const [view, setView] = useState<'list' | 'board'>('board');
  useEscapeStack(onClose, isOpen);
  if (!isOpen) return null;
  const boardable = Boolean(moveSeed && noteSeed);
  const toggle = boardable ? (
    <div className="garden-view-switch" role="group" aria-label="Garden view">
      <button type="button" aria-pressed={view === 'list'} onClick={() => setView('list')}>
        list
      </button>
      <button type="button" aria-pressed={view === 'board'} onClick={() => setView('board')}>
        board
      </button>
    </div>
  ) : undefined;
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
          {view === 'board' && boardable ? (
            <GardenBoard
              seeds={seeds}
              seedsTotal={seedsTotal}
              liveSessions={liveSessions ?? new Set()}
              loaded={loaded}
              onTransition={moveSeed!}
              onNote={noteSeed!}
              viewToggle={toggle}
              onClose={onClose}
            />
          ) : (
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
              viewToggle={toggle}
              onClose={onClose}
            />
          )}
        </div>
      </FocusTrap>
    </div>
  );
}
