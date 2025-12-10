// app/src/contexts/DaemonContext.tsx
import { createContext, useContext, ReactNode } from 'react';

interface PRActionResult {
  success: boolean;
  error?: string;
}

interface DaemonContextType {
  sendPRAction: (
    action: 'approve' | 'merge',
    repo: string,
    number: number,
    method?: string
  ) => Promise<PRActionResult>;
}

const DaemonContext = createContext<DaemonContextType | null>(null);

export function DaemonProvider({
  children,
  sendPRAction,
}: {
  children: ReactNode;
  sendPRAction: DaemonContextType['sendPRAction'];
}) {
  return (
    <DaemonContext.Provider value={{ sendPRAction }}>
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
