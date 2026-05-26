import XCTest
@testable import AttnNative

final class WorkspaceNavigationPolicyTests: XCTestCase {
    func testMovesRightToGeometricNeighborInNestedSplit() {
        let workspace = workspace(
            id: "workspace-1",
            activePaneID: "top-left",
            layoutJSON: """
                {"type":"split","direction":"horizontal","ratio":0.5,"children":[{"type":"split","direction":"vertical","ratio":0.5,"children":[{"type":"pane","pane_id":"top-left"},{"type":"pane","pane_id":"top-right"}]},{"type":"pane","pane_id":"bottom"}]}
                """
        )

        XCTAssertEqual(
            WorkspaceNavigationPolicy.intent(
                direction: .right,
                selectedWorkspaceID: workspace.id,
                workspaces: [workspace]
            ),
            .focusPane(workspaceID: "workspace-1", paneID: "top-right")
        )
    }

    func testMovesDownToOverlappingPaneInNestedSplit() {
        let workspace = workspace(
            id: "workspace-1",
            activePaneID: "top-right",
            layoutJSON: """
                {"type":"split","direction":"horizontal","ratio":0.5,"children":[{"type":"split","direction":"vertical","ratio":0.5,"children":[{"type":"pane","pane_id":"top-left"},{"type":"pane","pane_id":"top-right"}]},{"type":"pane","pane_id":"bottom"}]}
                """
        )

        XCTAssertEqual(
            WorkspaceNavigationPolicy.intent(
                direction: .down,
                selectedWorkspaceID: workspace.id,
                workspaces: [workspace]
            ),
            .focusPane(workspaceID: "workspace-1", paneID: "bottom")
        )
    }

    func testMovesToNextWorkspaceWhenPaneHasNoRightNeighbor() {
        let first = workspace(id: "workspace-1", activePaneID: "only", layoutJSON: pane("only"))
        let second = workspace(id: "workspace-2", activePaneID: "other", layoutJSON: pane("other"))

        XCTAssertEqual(
            WorkspaceNavigationPolicy.intent(
                direction: .right,
                selectedWorkspaceID: first.id,
                workspaces: [first, second]
            ),
            .selectWorkspace("workspace-2")
        )
    }

    func testWrapsToLastWorkspaceWhenMovingLeftAtFirstWorkspaceEdge() {
        let first = workspace(id: "workspace-1", activePaneID: "only", layoutJSON: pane("only"))
        let second = workspace(id: "workspace-2", activePaneID: "other", layoutJSON: pane("other"))

        XCTAssertEqual(
            WorkspaceNavigationPolicy.intent(
                direction: .left,
                selectedWorkspaceID: first.id,
                workspaces: [first, second]
            ),
            .selectWorkspace("workspace-2")
        )
    }

    func testMovesAwayFromRetainedWorkspaceWithoutALiveLayout() {
        let empty = WorkspaceSnapshot(
            id: "workspace-empty",
            title: "Empty",
            directory: "/tmp/empty",
            status: "idle",
            layout: nil
        )
        let occupied = workspace(id: "workspace-live", activePaneID: "only", layoutJSON: pane("only"))

        XCTAssertEqual(
            WorkspaceNavigationPolicy.intent(
                direction: .right,
                selectedWorkspaceID: empty.id,
                workspaces: [empty, occupied]
            ),
            .selectWorkspace("workspace-live")
        )
    }

    private func pane(_ paneID: String) -> String {
        #"{"type":"pane","pane_id":"\#(paneID)"}"#
    }

    private func workspace(id: String, activePaneID: String, layoutJSON: String) -> WorkspaceSnapshot {
        WorkspaceSnapshot(
            id: id,
            title: id,
            directory: "/tmp/\(id)",
            status: "idle",
            layout: WorkspaceLayoutSnapshot(
                workspaceID: id,
                activePaneID: activePaneID,
                layoutJSON: layoutJSON,
                panes: []
            )
        )
    }
}
