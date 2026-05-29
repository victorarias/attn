import type { ReactNode } from 'react';
import type { TerminalLayoutNode } from '../../types/workspace';

function clampSplitRatio(ratio: number): number {
  if (ratio > 0 && ratio < 1) {
    return ratio;
  }
  return 0.5;
}

function renderLayoutMetadata(node: TerminalLayoutNode, path = 'root'): ReactNode {
  if (node.type === 'pane') {
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

interface WorkspaceLayoutRendererProps {
  layoutTree: TerminalLayoutNode;
  paneIds: string[];
  renderPane: (paneId: string) => ReactNode;
}

export function WorkspaceLayoutRenderer({
  layoutTree,
  paneIds,
  renderPane,
}: WorkspaceLayoutRendererProps) {
  return (
    <div className="session-terminal-panes">
      <div className="workspace-layout-metadata" aria-hidden="true">
        {renderLayoutMetadata(layoutTree)}
      </div>
      {paneIds.map((paneId) => renderPane(paneId))}
    </div>
  );
}
