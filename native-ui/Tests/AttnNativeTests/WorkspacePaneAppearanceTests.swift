import XCTest
@testable import AttnNative

final class WorkspacePaneAppearanceTests: XCTestCase {
    func testInactiveSplitPaneUsesGhosttyDefaultDimmingOverlay() {
        XCTAssertEqual(
            WorkspacePaneAppearance.inactiveOverlayOpacity(paneID: "main", layout: splitLayout(activePaneID: "pane-b")),
            0.3
        )
    }

    func testActiveSplitPaneDoesNotDim() {
        XCTAssertEqual(
            WorkspacePaneAppearance.inactiveOverlayOpacity(paneID: "pane-b", layout: splitLayout(activePaneID: "pane-b")),
            0
        )
    }

    func testSinglePaneDoesNotDim() {
        let layout = WorkspaceLayoutSnapshot(
            workspaceID: "workspace-1",
            activePaneID: "main",
            layoutJSON: "{}",
            panes: [pane("main")]
        )

        XCTAssertEqual(WorkspacePaneAppearance.inactiveOverlayOpacity(paneID: "main", layout: layout), 0)
    }

    private func splitLayout(activePaneID: String) -> WorkspaceLayoutSnapshot {
        WorkspaceLayoutSnapshot(
            workspaceID: "workspace-1",
            activePaneID: activePaneID,
            layoutJSON: "{}",
            panes: [pane("main"), pane("pane-b")]
        )
    }

    private func pane(_ paneID: String) -> WorkspacePaneSnapshot {
        WorkspacePaneSnapshot(
            paneID: paneID,
            runtimeID: "runtime-\(paneID)",
            sessionID: nil,
            kind: "shell",
            title: paneID
        )
    }
}
