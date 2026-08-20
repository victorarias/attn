import type { ReactNode, RefObject } from 'react';
import { SidePanel } from './SidePanel';

export type DockPanelTone = 'default' | 'idle' | 'running' | 'awaiting_user' | 'completed' | 'stopped' | 'error';

export interface DockPanelDefinition {
  id: string;
  isOpen: boolean;
  width: string;
  tone?: DockPanelTone;
  className?: string;
  /**
   * The panel holds a place in the dock but paints somewhere else. The dock
   * still reserves its width, so the panels beside it sit where they should,
   * and lays out an empty slot at the rectangle the panel would have filled —
   * which is how a surface rendered outside the dock finds where to be. The
   * garden does this: see GardenFrame. The ref is attached to that slot, and
   * `children` is not rendered.
   */
  detached?: RefObject<HTMLDivElement | null>;
  children: ReactNode;
}

interface RightDockProps {
  panels: DockPanelDefinition[];
  panelOrder?: string[];
}

function addWidthOffset(currentOffset: string, width: string): string {
  if (currentOffset === '0px') {
    return width;
  }
  return `calc(${currentOffset} + ${width})`;
}

export function RightDock({ panels, panelOrder }: RightDockProps) {
  let offset = '0px';
  const orderedPanels = panelOrder && panelOrder.length > 0
    ? [
        ...panelOrder
          .map((id) => panels.find((panel) => panel.id === id))
          .filter((panel): panel is DockPanelDefinition => Boolean(panel)),
        ...panels.filter((panel) => !panelOrder.includes(panel.id)),
      ]
    : panels;

  return (
    <>
      {orderedPanels.map((panel) => {
        const panelOffset = offset;
        if (panel.isOpen) {
          offset = addWidthOffset(offset, panel.width);
        }

        if (panel.detached) {
          return (
            <div key={panel.id} className="side-panel-shell side-panel-shell--absolute" aria-hidden="true">
              <div
                ref={panel.detached}
                className="side-panel-slot"
                style={{ right: panelOffset, width: panel.width }}
              />
            </div>
          );
        }

        return (
          <SidePanel
            key={panel.id}
            isOpen={panel.isOpen}
            position="absolute"
            tone={panel.tone ?? 'default'}
            width={panel.width}
            offsetRight={panelOffset}
            className={panel.className}
          >
            {panel.children}
          </SidePanel>
        );
      })}
    </>
  );
}
