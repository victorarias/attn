// app/src/contexts/DaemonContext.tsx
import { createContext, useContext, ReactNode, useCallback, useState } from 'react';

interface PRActionResult {
  success: boolean;
  error?: string;
}

// Track the last muted item for undo functionality
interface LastMuted {
  type: 'pr' | 'repo' | 'author';
  id: string;
  timestamp: number;
}

interface DaemonContextType {
  sendPRAction: (
    action: 'approve' | 'merge',
    repo: string,
    number: number,
    method?: string
  ) => Promise<PRActionResult>;
  sendMutePR: (prId: string) => void;
  sendMuteRepo: (repo: string) => void;
  sendMuteAuthor: (author: string) => void;
  sendPRVisited: (prId: string) => void;
  lastMuted: LastMuted | null;
  clearLastMuted: () => void;
}

const DaemonContext = createContext<DaemonContextType | null>(null);

export function DaemonProvider({
  children,
  sendPRAction,
  sendMutePR: sendMutePRProp,
  sendMuteRepo: sendMuteRepoProp,
  sendMuteAuthor: sendMuteAuthorProp,
  sendPRVisited,
}: {
  children: ReactNode;
  sendPRAction: DaemonContextType['sendPRAction'];
  sendMutePR: (prId: string) => void;
  sendMuteRepo: (repo: string) => void;
  sendMuteAuthor: (author: string) => void;
  sendPRVisited: (prId: string) => void;
}) {
  const [lastMuted, setLastMuted] = useState<LastMuted | null>(null);

  // Wrap mute functions to track last muted for undo
  const sendMutePR = useCallback((prId: string) => {
    sendMutePRProp(prId);
    setLastMuted({ type: 'pr', id: prId, timestamp: Date.now() });
  }, [sendMutePRProp]);

  const sendMuteRepo = useCallback((repo: string) => {
    sendMuteRepoProp(repo);
    setLastMuted({ type: 'repo', id: repo, timestamp: Date.now() });
  }, [sendMuteRepoProp]);

  const sendMuteAuthor = useCallback((author: string) => {
    sendMuteAuthorProp(author);
    setLastMuted({ type: 'author', id: author, timestamp: Date.now() });
  }, [sendMuteAuthorProp]);

  const clearLastMuted = useCallback(() => {
    setLastMuted(null);
  }, []);

  return (
    <DaemonContext.Provider value={{ sendPRAction, sendMutePR, sendMuteRepo, sendMuteAuthor, sendPRVisited, lastMuted, clearLastMuted }}>
      {children}
    </DaemonContext.Provider>
  );
}

export function useDaemonContext() {
  const context = useContext(DaemonContext);
  if (!context) {
    throw new Error('useDaemonContext must be used within DaemonProvider');
  }
  return context;
}
