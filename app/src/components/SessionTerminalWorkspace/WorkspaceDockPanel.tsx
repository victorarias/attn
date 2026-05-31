import type { PointerEvent as ReactPointerEvent } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { PanelLeaf } from '../../types/workspace';
import './WorkspaceDockPanel.css';

// Prototype: fixed sample content. Real `.md` file rendering (fetched from the
// daemon / disk) is a planned follow-up — the panel plumbing is what this PR
// establishes.
const SAMPLE_MARKDOWN = `# Markdown Panel

This panel is a **first-class citizen** of the workspace layout. The daemon owns
where it sits and how big it is, so it survives app restarts and follows you to
other clients.

## What works

- **Drag the title bar** onto any terminal to re-dock it between panes.
- **Drag a divider** to resize it — same machinery as terminal splits.
- **Close** it from the × and the daemon forgets it.

## Formatting check

Inline \`code\`, a [link](https://example.com), and a list:

1. First
2. Second
3. Third

> Blockquotes render too.

\`\`\`ts
function hello(name: string) {
  return \`hi \${name}\`;
}
\`\`\`

| Col A | Col B |
| ----- | ----- |
| 1     | 2     |
| 3     | 4     |
`;

const PANEL_TITLES: Record<string, string> = {
  markdown: 'README.md',
};

interface WorkspaceDockPanelProps {
  panel: PanelLeaf;
  dragging: boolean;
  onClose: () => void;
  onHeaderPointerDown: (event: ReactPointerEvent<HTMLDivElement>) => void;
}

export function WorkspaceDockPanel({ panel, dragging, onClose, onHeaderPointerDown }: WorkspaceDockPanelProps) {
  const title = PANEL_TITLES[panel.panelKind] ?? panel.panelKind;
  return (
    <div className={`workspace-dock-panel ${dragging ? 'workspace-dock-panel--dragging' : ''}`.trim()}>
      <div
        className="workspace-dock-panel-header"
        onPointerDown={onHeaderPointerDown}
        title="Drag to re-dock"
      >
        <span className="workspace-dock-panel-title">{title}</span>
        <button
          type="button"
          className="workspace-dock-panel-close"
          title="Close panel"
          aria-label="Close panel"
          onPointerDown={(event) => event.stopPropagation()}
          onClick={onClose}
        >
          ×
        </button>
      </div>
      <div className="workspace-dock-panel-body">
        {panel.panelKind === 'markdown' ? (
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{SAMPLE_MARKDOWN}</ReactMarkdown>
        ) : (
          <div className="workspace-dock-panel-unknown">Unsupported panel: {panel.panelKind}</div>
        )}
      </div>
    </div>
  );
}
