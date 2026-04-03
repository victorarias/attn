interface GroupableSession {
  id: string;
  cwd?: string;
  label: string;
  branch?: string;
  endpointId?: string;
  endpointName?: string;
  endpointStatus?: string;
}

export interface SessionGroup<T extends GroupableSession> {
  directory: string;
  label: string;
  branch?: string;
  endpointId?: string;
  endpointName?: string;
  endpointStatus?: string;
  sessions: T[];
}

export function groupSessionsByDirectory<T extends GroupableSession>(sessions: T[]): SessionGroup<T>[] {
  const groups = new Map<string, SessionGroup<T>>();

  for (const session of sessions) {
    const directory = session.cwd || session.id;
    const key = `${session.endpointId || 'local'}::${directory}`;
    if (!groups.has(key)) {
      groups.set(key, {
        directory,
        label: directory.split('/').pop() || directory,
        branch: session.branch,
        endpointId: session.endpointId,
        endpointName: session.endpointName,
        endpointStatus: session.endpointStatus,
        sessions: [],
      });
    }
    groups.get(key)!.sessions.push(session);
  }

  return Array.from(groups.values());
}

/** Flatten grouped sessions into visual display order */
export function getVisualSessionOrder<T extends GroupableSession>(sessions: T[]): T[] {
  const groups = groupSessionsByDirectory(sessions);
  return groups.flatMap(g => g.sessions);
}
