import { useEffect } from 'react';
import { MarkdownReader } from '../../src/components/MarkdownReader';
import { fileMarkdownSource } from '../../src/components/MarkdownReader/documentSource';
import type { HarnessProps } from '../types';

const DOCUMENT_SOURCE = fileMarkdownSource('test-harness', '/tmp/mermaid-diagram-harness.md');

const DOCUMENT = `# Large Mermaid diagrams

## Small diagram

\`\`\`mermaid
flowchart LR
  A[Small] --> B[Diagram]
\`\`\`

## Component flow

\`\`\`mermaid
flowchart LR
  subgraph Agent[Agent process]
    Codex[Codex TUI]
    JSONL[Native transcript JSONL]
  end

  subgraph Daemon[attn daemon]
    Watcher[Transcript watcher]
    State[Session state]
    Get[session_messages_get handler]
    Reader[Recent assistant-message reader]
  end

  subgraph App[attn app]
    Socket[Daemon socket]
    Annotated[AnnotatedTerminal]
    Store[TerminalAnnotationStore]
    Aligner[Transcript-to-grid aligner]
    Grid[Live Ghostty terminal grid]
  end

  Codex -->|renders bytes| Grid
  Codex -->|appends completed entries| JSONL
  JSONL -->|file delta| Watcher
  Watcher -->|classification evidence| State
  State -->|only idle / waiting_input / pending_approval| Annotated
  Annotated -->|session_messages_get| Socket
  Socket --> Get
  Get --> Reader
  Reader -->|reads| JSONL
  Reader -->|message window| Get
  Get --> Socket
  Socket -->|messages| Annotated
  Annotated --> Store
  Store --> Aligner
  Grid --> Aligner
  Aligner -->|message offsets mapped to cells| Store

  Watcher -. "completed message while working\\n(no refresh signal)" .-> Annotated

  classDef gap fill:#5a2330,stroke:#ff6b81,color:#fff;
  class Watcher,Annotated gap;
\`\`\`

## Refresh sequence

\`\`\`mermaid
sequenceDiagram
  autonumber
  participant C as Codex
  participant T as Transcript JSONL
  participant W as Transcript watcher
  participant S as Session state
  participant A as AnnotatedTerminal
  participant D as session_messages_get
  participant R as Message reader
  participant M as Annotation store + aligner

  C->>T: Append completed assistant commentary
  T-->>W: File delta
  W->>W: Extract and deduplicate assistant content
  Note over W,A: No message-change event exists
  S-->>A: State remains working
  Note over A: Settled-state gate prevents fetch
  C-->>A: Same commentary is visible in terminal grid
  A->>M: User selects visible commentary
  M-->>A: outside-messages
  A-->>A: Show misleading refusal

  C->>T: Append final assistant message
  S-->>A: State changes to idle
  A->>D: session_messages_get
  D->>R: Read recent assistant messages
  R->>T: Scan transcript
  T-->>R: Completed messages
  R-->>D: Canonical bounded window
  D-->>A: session_messages_get_result
  A->>M: Replace message window and align to grid
  M-->>A: Selection can now resolve
\`\`\`
`;

export function MermaidDiagramHarness({ onReady }: HarnessProps) {
  useEffect(() => {
    const ready = () => {
      if (document.querySelectorAll('.markdown-mermaid svg').length === 3) {
        onReady();
        return true;
      }
      return false;
    };
    if (ready()) return;
    const observer = new MutationObserver(() => {
      if (ready()) observer.disconnect();
    });
    observer.observe(document.body, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, [onReady]);

  return (
    <div style={{ width: 760, maxWidth: '100%', margin: '0 auto', padding: 12 }}>
      <MarkdownReader content={DOCUMENT} source={DOCUMENT_SOURCE} />
    </div>
  );
}
