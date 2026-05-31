import type { PointerEvent as ReactPointerEvent, ReactNode } from 'react';
import type { SplitDivider, TerminalLayoutNode } from '../../types/workspace';

function clampSplitRatio(ratio: number): number {
  if (ratio > 0 && ratio < 1) {
    return ratio;
  }
  return 0.5;
}

function renderLayoutMetadata(node: TerminalLayoutNode, path = 'root'): ReactNode {
  if (node.type !== 'split') {
    return null;
  }

  const firstRatio = clampSplitRatio(node.ratio);
  const secondRatio = clampSplitRatio(1 - firstRatio);
  const firstPath = `${path}/0`;
  const secondPath = `${path}/1`;
  const splitStyle = node.direction === 'vertical'
    ? { gridTemplateColumns: `minmax(0, ${firstRatio}fr) minmax(0, ${secondRatio}fr)` }
    : { gridTemplateRows: `minmax(0, ${firstRatio}fr) minmax(0, ${secondRatio}fr)` };

  return (
    <div
      key={node.splitId}
      className={`workspace-split split-${node.direction}`}
      data-split-id={node.splitId}
      data-split-path={path}
      data-split-direction={node.direction}
      data-split-ratio={firstRatio.toFixed(3)}
      style={splitStyle}
    >
      <div
        className="workspace-split-child"
        data-split-child-index="0"
        data-split-child-path={firstPath}
      >
        {renderLayoutMetadata(node.children[0], firstPath)}
      </div>
      <div
        className="workspace-split-child"
        data-split-child-index="1"
        data-split-child-path={secondPath}
      >
        {renderLayoutMetadata(node.children[1], secondPath)}
      </div>
    </div>
  );
}

function dividerStyle(divider: SplitDivider): React.CSSProperties {
  const pct = (value: number) => `${value * 100}%`;
  if (divider.direction === 'vertical') {
    const x = divider.left + (divider.right - divider.left) * divider.ratio;
    return {
      left: pct(x),
      top: pct(divider.top),
      height: pct(divider.bottom - divider.top),
    };
  }
  const y = divider.top + (divider.bottom - divider.top) * divider.ratio;
  return {
    top: pct(y),
    left: pct(divider.left),
    width: pct(divider.right - divider.left),
  };
}

interface WorkspaceLayoutRendererProps {
  layoutTree: TerminalLayoutNode;
  paneIds: string[];
  renderPane: (paneId: string) => ReactNode;
  dividers?: SplitDivider[];
  onDividerPointerDown?: (divider: SplitDivider, event: ReactPointerEvent<HTMLDivElement>) => void;
  // Rendered last inside the panes container (e.g. a drag-to-dock highlight).
  overlay?: ReactNode;
}

export function WorkspaceLayoutRenderer({
  layoutTree,
  paneIds,
  renderPane,
  dividers = [],
  onDividerPointerDown,
  overlay,
}: WorkspaceLayoutRendererProps) {
  return (
    <div className="session-terminal-panes">
      <div className="workspace-layout-metadata" aria-hidden="true">
        {renderLayoutMetadata(layoutTree)}
      </div>
      {paneIds.map((paneId) => renderPane(paneId))}
      {onDividerPointerDown && dividers.map((divider) => (
        <div
          key={divider.splitId}
          className={`workspace-split-divider workspace-split-divider--${divider.direction}`}
          style={dividerStyle(divider)}
          data-split-id={divider.splitId}
          role="separator"
          aria-orientation={divider.direction === 'vertical' ? 'vertical' : 'horizontal'}
          onPointerDown={(event) => onDividerPointerDown(divider, event)}
        />
      ))}
      {overlay}
    </div>
  );
}
