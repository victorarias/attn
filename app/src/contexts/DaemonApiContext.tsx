// app/src/contexts/DaemonApiContext.tsx
import { createContext, useContext, type ReactNode } from 'react';
import type { useDaemonSocket } from '../hooks/useDaemonSocket';

// The whole return value of useDaemonSocket — the frontend's entire daemon API.
export type DaemonApi = ReturnType<typeof useDaemonSocket>;

// Only App calls useDaemonSocket; everything below it reads the API from here.
// It used to arrive by prop, which meant adding one daemon command touched four
// places in App.tsx (destructure, call site, props interface, parameter list)
// and did nothing in three of them but carry the name downhill. Missing any one
// of the four left the function undefined at the call site.
//
// Deliberately not the same context as DaemonContext: that one is a small piece
// of behavior (mute + undo bookkeeping) that happens to wrap daemon calls, and
// folding it in here would make every consumer of a send function re-render on
// an unrelated undo.
const DaemonApiContext = createContext<DaemonApi | null>(null);

export function DaemonApiProvider({ api, children }: { api: DaemonApi; children: ReactNode }) {
  return <DaemonApiContext.Provider value={api}>{children}</DaemonApiContext.Provider>;
}

export function useDaemonApi(): DaemonApi {
  const api = useContext(DaemonApiContext);
  if (!api) {
    throw new Error('useDaemonApi must be used within DaemonApiProvider');
  }
  return api;
}
