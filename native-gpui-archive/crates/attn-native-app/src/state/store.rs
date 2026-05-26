use std::collections::HashMap;

use attn_protocol::{Session, SettingsMap, Workspace, WorkspaceLayout};

#[derive(Default)]
pub struct ClientStore {
    pub sessions: HashMap<String, Session>,
    pub workspaces: Vec<Workspace>,
    pub layouts: HashMap<String, WorkspaceLayout>,
    pub settings: SettingsMap,
}

impl ClientStore {
    pub fn reset(
        &mut self,
        sessions: Vec<Session>,
        workspaces: Vec<Workspace>,
        settings: SettingsMap,
    ) {
        self.sessions = sessions
            .into_iter()
            .map(|session| (session.id.clone(), session))
            .collect();
        self.layouts.clear();
        for workspace in &workspaces {
            if let Some(layout) = workspace.layout.clone() {
                self.layouts.insert(workspace.id.clone(), layout);
            }
        }
        self.workspaces = workspaces;
        self.settings = settings;
        self.sort_workspaces();
    }

    pub fn upsert_session(&mut self, session: Session) {
        self.sessions.insert(session.id.clone(), session);
    }

    pub fn remove_session(&mut self, session_id: &str) {
        self.sessions.remove(session_id);
    }

    pub fn upsert_workspace(&mut self, workspace: Workspace) {
        if let Some(layout) = workspace.layout.clone() {
            self.layouts.insert(workspace.id.clone(), layout);
        }
        match self
            .workspaces
            .iter()
            .position(|item| item.id == workspace.id)
        {
            Some(index) => self.workspaces[index] = workspace,
            None => self.workspaces.push(workspace),
        }
        self.sort_workspaces();
    }

    pub fn remove_workspace(&mut self, workspace_id: &str) {
        self.workspaces
            .retain(|workspace| workspace.id != workspace_id);
        self.layouts.remove(workspace_id);
    }

    pub fn set_layout(&mut self, layout: WorkspaceLayout) {
        self.layouts.insert(layout.workspace_id.clone(), layout);
    }

    pub fn workspace(&self, workspace_id: &str) -> Option<&Workspace> {
        self.workspaces
            .iter()
            .find(|workspace| workspace.id == workspace_id)
    }

    fn sort_workspaces(&mut self) {
        self.workspaces
            .sort_by(|left, right| left.title.cmp(&right.title).then(left.id.cmp(&right.id)));
    }
}
