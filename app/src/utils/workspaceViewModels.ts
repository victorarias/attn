export interface WorkspaceViewSession {
  id: string;
  label: string;
  workspaceId?: string;
  workspace_id?: string;
  cwd?: string;
  directory?: string;
  endpointId?: string;
  endpoint_id?: string;
  state?: string;
}

export interface WorkspaceViewWorkspace {
  id: string;
  title: string;
  directory: string;
  status?: string;
  endpointId?: string;
  endpoint_id?: string;
}

export interface WorkspaceWithSessions<TSession extends WorkspaceViewSession = WorkspaceViewSession> {
  id: string;
  title: string;
  directory: string;
  status?: string;
  endpointId?: string;
  sessions: TSession[];
  firstSessionId: string | null;
  focusedSessionId: string | null;
}

interface WorkspaceViewModelOptions {
  focusedSessionIdByWorkspace?: Record<string, string | null | undefined>;
}

function sessionWorkspaceId(session: WorkspaceViewSession): string {
  return session.workspaceId || session.workspace_id || `workspace-${session.id}`;
}

function sessionDirectory(session: WorkspaceViewSession): string {
  return session.cwd || session.directory || session.id;
}

function sessionEndpointId(session: WorkspaceViewSession): string | undefined {
  return session.endpointId || session.endpoint_id;
}

function workspaceEndpointId(workspace: WorkspaceViewWorkspace): string | undefined {
  return workspace.endpointId || workspace.endpoint_id;
}

function fallbackTitle(directory: string): string {
  return directory.split('/').filter(Boolean).pop() || directory;
}

function workspaceKey(workspaceId: string, endpointId?: string): string {
  return `${endpointId || 'local'}::${workspaceId}`;
}

export function buildWorkspaceViewModels<TSession extends WorkspaceViewSession>(
  workspaces: WorkspaceViewWorkspace[],
  sessions: TSession[],
  options: WorkspaceViewModelOptions = {},
): WorkspaceWithSessions<TSession>[] {
  const sessionsByWorkspace = new Map<string, TSession[]>();
  const sessionKeysByWorkspaceId = new Map<string, string[]>();

  for (const session of sessions) {
    const workspaceId = sessionWorkspaceId(session);
    const key = workspaceKey(workspaceId, sessionEndpointId(session));
    const current = sessionsByWorkspace.get(key) || [];
    current.push(session);
    sessionsByWorkspace.set(key, current);
    if (!sessionKeysByWorkspaceId.has(workspaceId)) {
      sessionKeysByWorkspaceId.set(workspaceId, []);
    }
    const keys = sessionKeysByWorkspaceId.get(workspaceId)!;
    if (!keys.includes(key)) {
      keys.push(key);
    }
  }

  const result: WorkspaceWithSessions<TSession>[] = [];
  const consumed = new Set<string>();

  for (const workspace of workspaces) {
    const key = resolveWorkspaceSessionKey(workspace, sessionKeysByWorkspaceId, consumed);
    const workspaceSessions = sessionsByWorkspace.get(key) || [];
    consumed.add(key);
    result.push(toWorkspaceViewModel(workspace, workspaceSessions, options));
  }

  for (const session of sessions) {
    const workspaceId = sessionWorkspaceId(session);
    const endpointId = sessionEndpointId(session);
    const key = workspaceKey(workspaceId, endpointId);
    if (consumed.has(key)) {
      continue;
    }
    consumed.add(key);
    const workspaceSessions = sessionsByWorkspace.get(key) || [];
    const directory = sessionDirectory(workspaceSessions[0] || session);
    result.push(toWorkspaceViewModel({
      id: workspaceId,
      title: fallbackTitle(directory),
      directory,
      endpoint_id: endpointId,
    }, workspaceSessions, options));
  }

  return result;
}

function resolveWorkspaceSessionKey(
  workspace: WorkspaceViewWorkspace,
  sessionKeysByWorkspaceId: Map<string, string[]>,
  consumed: Set<string>,
): string {
  const endpointId = workspaceEndpointId(workspace);
  if (endpointId) {
    return workspaceKey(workspace.id, endpointId);
  }
  const unconsumedSessionKey = (sessionKeysByWorkspaceId.get(workspace.id) || [])
    .find((key) => !consumed.has(key));
  return unconsumedSessionKey || workspaceKey(workspace.id);
}

function toWorkspaceViewModel<TSession extends WorkspaceViewSession>(
  workspace: WorkspaceViewWorkspace,
  sessions: TSession[],
  options: WorkspaceViewModelOptions,
): WorkspaceWithSessions<TSession> {
  const firstSessionId = sessions[0]?.id ?? null;
  const requestedFocus = options.focusedSessionIdByWorkspace?.[workspace.id] || null;
  const focusedSessionId = requestedFocus && sessions.some((session) => session.id === requestedFocus)
    ? requestedFocus
    : firstSessionId;

  return {
    id: workspace.id,
    title: workspace.title,
    directory: workspace.directory,
    status: workspace.status,
    endpointId: workspaceEndpointId(workspace) || (sessions[0] ? sessionEndpointId(sessions[0]) : undefined),
    sessions,
    firstSessionId,
    focusedSessionId,
  };
}

export function firstSessionIdForWorkspace<TSession extends WorkspaceViewSession>(
  workspace: WorkspaceWithSessions<TSession> | undefined | null,
): string | null {
  return workspace?.firstSessionId ?? null;
}
