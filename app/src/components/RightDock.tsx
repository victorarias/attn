import type { ReactNode } from 'react';
import { SidePanel } from './SidePanel';

export type DockPanelTone = 'default' | 'idle' | 'running' | 'awaiting_user' | 'completed' | 'stopped' | 'error';

export interface DockPanelDefinition {
  id: string;
  isOpen: boolean;
  width: string;
  tone?: DockPanelTone;
  className?: string;
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
