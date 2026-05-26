import XCTest
@testable import AttnNative

final class WorkspaceClosePolicyTests: XCTestCase {
    func testClosesFocusedAuxiliaryPaneBeforeWorkspace() {
        XCTAssertEqual(
            WorkspaceClosePolicy.intent(for: workspace(activePaneID: "pane-b", paneIDs: ["main", "pane-a", "pane-b"])),
            .closePane(workspaceID: "workspace-1", paneID: "pane-b")
        )
    }

    func testClosesAuxiliaryPaneWhenRootHasFocusAndWorkspaceStillHasSplits() {
        XCTAssertEqual(
            WorkspaceClosePolicy.intent(for: workspace(activePaneID: "main", paneIDs: ["main", "pane-a"])),
            .closePane(workspaceID: "workspace-1", paneID: "pane-a")
        )
    }

    func testClosesWorkspaceOnlyAfterItsLastPane() {
        XCTAssertEqual(
            WorkspaceClosePolicy.intent(for: workspace(activePaneID: "main", paneIDs: ["main"])),
            .closeWorkspace("workspace-1")
        )
    }

    func testEmptyClientFallsThroughToWindowClose() {
        XCTAssertEqual(WorkspaceClosePolicy.intent(for: nil), .closeWindow)
    }

    func testWorkspaceAwaitingItsLayoutStillClosesAsContent() {
        let workspace = WorkspaceSnapshot(
            id: "workspace-1",
            title: "Workspace",
            directory: "/tmp/workspace",
            status: "idle",
            layout: nil
        )

        XCTAssertEqual(WorkspaceClosePolicy.intent(for: workspace), .closeWorkspace("workspace-1"))
    }

    private func workspace(activePaneID: String, paneIDs: [String]) -> WorkspaceSnapshot {
        WorkspaceSnapshot(
            id: "workspace-1",
            title: "Workspace",
            directory: "/tmp/workspace",
            status: "idle",
            layout: WorkspaceLayoutSnapshot(
                workspaceID: "workspace-1",
                activePaneID: activePaneID,
                layoutJSON: "{}",
                panes: paneIDs.map {
                    WorkspacePaneSnapshot(
                        paneID: $0,
                        runtimeID: "runtime-\($0)",
                        sessionID: nil,
                        kind: "shell",
                        title: $0
                    )
                }
            )
        )
    }
}
