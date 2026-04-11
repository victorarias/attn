import WebSocket from 'ws';

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function decodeBase64Utf8(value) {
  if (!value) {
    return '';
  }
  return Buffer.from(value, 'base64').toString('utf8');
}

function workspacePaneIds(workspace) {
  return (workspace?.panes || []).map((pane) => pane.pane_id);
}

function pruneWorkspacesBySessions(sessionsById, workspacesBySessionId) {
  for (const sessionId of Array.from(workspacesBySessionId.keys())) {
    if (sessionsById.has(sessionId)) {
      continue;
    }
    workspacesBySessionId.delete(sessionId);
  }
}

export class DaemonObserver {
  constructor({
    wsUrl = 'ws://127.0.0.1:9849/ws',
    connectTimeoutMs = 45_000,
  } = {}) {
    this.wsUrl = wsUrl;
    this.connectTimeoutMs = connectTimeoutMs;
    this.ws = null;
    this.sessionsById = new Map();
    this.workspacesBySessionId = new Map();
    this.endpointsById = new Map();
    this.connected = false;
    this.initialStateReceived = false;
  }

  async connect() {
    if (this.connected && this.ws) {
      return;
    }

    const startedAt = Date.now();
    while (Date.now() - startedAt < this.connectTimeoutMs) {
      try {
        this.initialStateReceived = false;
        await this.#connectOnce();
        await this.waitFor(
          () => this.initialStateReceived,
          'daemon initial_state',
          Math.min(5_000, this.connectTimeoutMs),
        );
        return;
      } catch (error) {
        await delay(250);
        this.lastConnectError = error;
      }
    }

    throw new Error(
      `Timed out connecting to daemon websocket at ${this.wsUrl}: ${this.lastConnectError instanceof Error ? this.lastConnectError.message : this.lastConnectError}`
    );
  }

  async close() {
    const ws = this.ws;
    this.ws = null;
    this.connected = false;
    this.initialStateReceived = false;
    if (!ws) {
      return;
    }
    await new Promise((resolve) => {
      ws.once('close', resolve);
      ws.close();
      setTimeout(resolve, 500);
    });
  }

  send(message) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      throw new Error('Daemon websocket is not connected');
    }
    this.ws.send(JSON.stringify(message));
  }

  addEndpoint(name, sshTarget) {
    this.send({ cmd: 'add_endpoint', name, ssh_target: sshTarget });
  }

  updateEndpoint(endpointId, updates) {
    this.send({ cmd: 'update_endpoint', endpoint_id: endpointId, ...updates });
  }

  removeEndpoint(endpointId) {
    this.send({ cmd: 'remove_endpoint', endpoint_id: endpointId });
  }

  unregisterSession(sessionId) {
    this.send({ cmd: 'unregister', id: sessionId });
  }

  async unregisterMatchingSessions(predicate, timeoutMs = 15_000) {
    const matches = [...this.sessionsById.values()].filter(predicate);
    if (matches.length === 0) {
      return [];
    }

    for (const session of matches) {
      this.unregisterSession(session.id);
    }

    const targetIds = new Set(matches.map((session) => session.id));
    await this.waitFor(() => {
      const remaining = [...this.sessionsById.values()].filter((session) => targetIds.has(session.id));
      return remaining.length === 0 ? true : null;
    }, `daemon unregister for sessions ${[...targetIds].join(', ')}`, timeoutMs);

    return matches;
  }

  getWorkspace(sessionId) {
    return this.workspacesBySessionId.get(sessionId) || null;
  }

  getSession(sessionId) {
    return this.sessionsById.get(sessionId) || null;
  }

  getEndpoint(endpointId) {
    return this.endpointsById.get(endpointId) || null;
  }

  findEndpointByName(name) {
    for (const endpoint of this.endpointsById.values()) {
      if (endpoint.name === name) {
        return endpoint;
      }
    }
    return null;
  }

  async waitForEndpoint({ id, name, sshTarget, status, timeoutMs = 20_000 } = {}) {
    return this.waitFor(
      () => {
        for (const endpoint of this.endpointsById.values()) {
          if (id && endpoint.id !== id) continue;
          if (name && endpoint.name !== name) continue;
          if (sshTarget && endpoint.ssh_target !== sshTarget) continue;
          if (status && endpoint.status !== status) continue;
          return endpoint;
        }
        return null;
      },
      `endpoint id=${id || '*'} name=${name || '*'} sshTarget=${sshTarget || '*'} status=${status || '*'}`,
      timeoutMs
    );
  }

  async waitForSession({ id, label, directory, timeoutMs = 20_000 } = {}) {
    return this.waitFor(
      () => {
        for (const session of this.sessionsById.values()) {
          if (id && session.id !== id) {
            continue;
          }
          if (label && session.label !== label) {
            continue;
          }
          if (directory && session.directory !== directory) {
            continue;
          }
          return session;
        }
        return null;
      },
      `session id=${id || '*'} label=${label || '*'} directory=${directory || '*'}`,
      timeoutMs
    );
  }

  async waitForWorkspace(sessionId, predicate, description, timeoutMs = 20_000) {
    return this.waitFor(() => {
      const workspace = this.workspacesBySessionId.get(sessionId);
      if (!workspace) {
        return null;
      }
      return predicate(workspace) ? workspace : null;
    }, description || `workspace for session ${sessionId}`, timeoutMs);
  }

  async waitForUtilityPane(sessionId, timeoutMs = 20_000) {
    const workspace = await this.waitForWorkspace(
      sessionId,
      (entry) =>
        (entry.panes || []).some((pane) => pane.kind === 'shell' && typeof pane.runtime_id === 'string' && pane.runtime_id.length > 0),
      `utility pane for session ${sessionId}`,
      timeoutMs
    );
    return workspace.panes.find((pane) => pane.kind === 'shell' && pane.runtime_id) || null;
  }

  async readScrollback(runtimeId, timeoutMs = 5_000) {
    return readScrollback(this.wsUrl, runtimeId, timeoutMs);
  }

  async waitForScrollbackContains(runtimeId, needle, timeoutMs = 12_000) {
    const startedAt = Date.now();
    let lastScrollback = '';
    while (Date.now() - startedAt < timeoutMs) {
      try {
        lastScrollback = await this.readScrollback(runtimeId, Math.min(5_000, timeoutMs));
        if (lastScrollback.includes(needle)) {
          return lastScrollback;
        }
      } catch {
        // Retry while the runtime is still attaching.
      }
      await delay(400);
    }
    throw new Error(
      `Timed out waiting for scrollback ${runtimeId} to contain ${JSON.stringify(needle)}. Last scrollback tail:\n${lastScrollback.slice(-400)}`
    );
  }

  async waitForScrollbackReady(runtimeId, timeoutMs = 12_000) {
    const startedAt = Date.now();
    let lastError = null;
    while (Date.now() - startedAt < timeoutMs) {
      try {
        return await this.readScrollback(runtimeId, Math.min(5_000, timeoutMs));
      } catch (error) {
        lastError = error;
      }
      await delay(250);
    }
    throw new Error(
      `Timed out waiting for scrollback ${runtimeId} to become attachable: ${lastError instanceof Error ? lastError.message : String(lastError || 'unknown error')}`
    );
  }

  async waitFor(predicate, description, timeoutMs = 10_000) {
    const startedAt = Date.now();
    let lastSnapshot = this.describeState();
    while (Date.now() - startedAt < timeoutMs) {
      const value = predicate();
      if (value) {
        return value;
      }
      await delay(100);
      lastSnapshot = this.describeState();
    }
    throw new Error(`Timed out waiting for ${description}. Current daemon state:\n${lastSnapshot}`);
  }

  describeState() {
    const sessions = [...this.sessionsById.values()].map((session) => ({
      id: session.id,
      label: session.label,
      directory: session.directory,
      state: session.state,
      agent: session.agent,
    }));
    const workspaces = [...this.workspacesBySessionId.values()].map((workspace) => ({
      sessionId: workspace.session_id,
      activePaneId: workspace.active_pane_id,
      paneIds: workspacePaneIds(workspace),
    }));
    const endpoints = [...this.endpointsById.values()].map((endpoint) => ({
      id: endpoint.id,
      name: endpoint.name,
      sshTarget: endpoint.ssh_target,
      status: endpoint.status,
      sessionCount: endpoint.session_count,
    }));
    return JSON.stringify({ sessions, workspaces, endpoints }, null, 2);
  }

  #connectOnce() {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(this.wsUrl);
      let settled = false;

      const fail = (error) => {
        if (settled) {
          return;
        }
        settled = true;
        try {
          ws.close();
        } catch {
          // Ignore close failures during connect.
        }
        reject(error);
      };

      const succeed = () => {
        if (settled) {
          return;
        }
        settled = true;
        this.ws = ws;
        this.connected = true;
        resolve();
      };

      const timeout = setTimeout(() => {
        fail(new Error(`connect timeout after ${this.connectTimeoutMs}ms`));
      }, Math.min(5_000, this.connectTimeoutMs));

      ws.once('open', () => {
        clearTimeout(timeout);
        succeed();
      });

      ws.once('error', (error) => {
        clearTimeout(timeout);
        fail(error);
      });

      ws.on('close', () => {
        if (this.ws === ws) {
          this.connected = false;
          this.ws = null;
        }
      });

      ws.on('message', (raw) => {
        try {
          const data = JSON.parse(raw.toString());
          this.#handleMessage(data);
        } catch (error) {
          console.warn('[RealAppHarness] Failed to parse daemon message:', error);
        }
      });
    });
  }

  #handleMessage(data) {
    switch (data.event) {
      case 'initial_state':
        this.initialStateReceived = true;
        this.sessionsById.clear();
        for (const session of data.sessions || []) {
          this.sessionsById.set(session.id, session);
        }
        this.workspacesBySessionId.clear();
        for (const workspace of data.workspaces || []) {
          this.workspacesBySessionId.set(workspace.session_id, workspace);
        }
        this.endpointsById.clear();
        for (const endpoint of data.endpoints || []) {
          this.endpointsById.set(endpoint.id, endpoint);
        }
        break;
      case 'sessions_updated':
        this.sessionsById.clear();
        for (const session of data.sessions || []) {
          this.sessionsById.set(session.id, session);
        }
        pruneWorkspacesBySessions(this.sessionsById, this.workspacesBySessionId);
        break;
      case 'endpoint_status_changed':
        if (data.endpoint?.id) {
          this.endpointsById.set(data.endpoint.id, data.endpoint);
        }
        break;
      case 'endpoints_updated':
        this.endpointsById.clear();
        for (const endpoint of data.endpoints || []) {
          this.endpointsById.set(endpoint.id, endpoint);
        }
        break;
      case 'session_registered':
      case 'session_state_changed':
      case 'session_todos_updated':
        if (data.session?.id) {
          this.sessionsById.set(data.session.id, data.session);
        }
        break;
      case 'session_unregistered':
        if (data.session?.id) {
          this.sessionsById.delete(data.session.id);
          this.workspacesBySessionId.delete(data.session.id);
        }
        break;
      case 'workspace_snapshot':
      case 'workspace_updated':
        if (data.workspace?.session_id) {
          if (this.sessionsById.has(data.workspace.session_id)) {
            this.workspacesBySessionId.set(data.workspace.session_id, data.workspace);
          } else {
            this.workspacesBySessionId.delete(data.workspace.session_id);
          }
        }
        break;
      default:
        break;
    }
  }
}

export async function readScrollback(wsUrl, runtimeId, timeoutMs = 5_000) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(wsUrl);
    const timeout = setTimeout(() => {
      try {
        ws.close();
      } catch {
        // Ignore close errors during timeout handling.
      }
      reject(new Error(`attach_session timeout for ${runtimeId}`));
    }, timeoutMs);

    ws.once('open', () => {
      ws.send(JSON.stringify({ cmd: 'attach_session', id: runtimeId }));
    });

    ws.on('message', (raw) => {
      const data = JSON.parse(raw.toString());
      if (data.event !== 'attach_result' || data.id !== runtimeId) {
        return;
      }
      clearTimeout(timeout);
      ws.close();
      if (!data.success) {
        reject(new Error(data.error || `attach_session failed for ${runtimeId}`));
        return;
      }
      resolve(decodeBase64Utf8(data.scrollback));
    });

    ws.once('error', (error) => {
      clearTimeout(timeout);
      reject(error);
    });
  });
}
