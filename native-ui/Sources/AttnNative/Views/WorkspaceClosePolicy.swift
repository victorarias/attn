import Foundation

enum WorkspaceCloseIntent: Equatable {
    case closePane(workspaceID: String, paneID: String)
    case closeWorkspace(String)
    case closeWindow
}

struct WorkspaceClosePolicy {
    static func intent(for workspace: WorkspaceSnapshot?) -> WorkspaceCloseIntent {
        guard let workspace else {
            return .closeWindow
        }
        guard let layout = workspace.layout, !layout.panes.isEmpty else {
            return .closeWorkspace(workspace.id)
        }
        if layout.panes.count > 1 {
            if layout.activePaneID != "main",
               layout.panes.contains(where: { $0.paneID == layout.activePaneID }) {
                return .closePane(workspaceID: workspace.id, paneID: layout.activePaneID)
            }
            if let auxiliary = layout.panes.last(where: { $0.paneID != "main" }) {
                return .closePane(workspaceID: workspace.id, paneID: auxiliary.paneID)
            }
        }
        return .closeWorkspace(workspace.id)
    }
}
