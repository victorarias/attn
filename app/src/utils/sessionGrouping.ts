interface GroupableSession {
  id: string;
  cwd?: string;
  label: string;
  branch?: string;
}

export interface SessionGroup<T extends GroupableSession> {
  directory: string;
  label: string;
  branch?: string;
  sessions: T[];
}

export function groupSessionsByDirectory<T extends GroupableSession>(sessions: T[]): SessionGroup<T>[] {
  const groups = new Map<string, SessionGroup<T>>();

  for (const session of sessions) {
    const key = session.cwd || session.id;
    if (!groups.has(key)) {
      groups.set(key, {
        directory: key,
        label: key.split('/').pop() || key,
        branch: session.branch,
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
