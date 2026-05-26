import XCTest
import AppKit
@testable import AttnNative

@MainActor
final class WorkspaceLauncherModelTests: XCTestCase {
    func testBothLaunchersRestoreRememberedTerminalChoice() {
        let daemon = DaemonConnection(
            initialSettings: ["new_pane_choice": "terminal"],
            initialWorkspaces: [workspace()],
            selectedWorkspaceID: "workspace-1"
        )
        let model = WorkspaceLauncherModel(daemon: daemon)

        model.openNewWorkspace()
        XCTAssertEqual(model.paneChoice, .terminal)

        model.openAddPane(direction: .vertical)
        XCTAssertEqual(model.paneChoice, .terminal)
    }

    func testSelectingPaneChoicePersistsTerminalAsWellAsAgents() {
        let daemon = DaemonConnection(initialSettings: ["codex_available": "true"])
        let model = WorkspaceLauncherModel(daemon: daemon)

        model.selectPaneChoice(.terminal)
        XCTAssertEqual(daemon.settings["new_pane_choice"], "terminal")

        model.selectPaneChoice(.codex)
        XCTAssertEqual(daemon.settings["new_pane_choice"], "codex")
        XCTAssertEqual(daemon.settings["new_session_agent"], "codex")
    }

    func testAddPaneDefaultsToFocusedTerminalReportedDirectory() {
        let daemon = DaemonConnection(
            initialSettings: ["new_pane_choice": "terminal"],
            initialWorkspaces: [workspace()],
            selectedWorkspaceID: "workspace-1"
        )
        daemon.register(surface: StubTerminalSurface(runtimeID: "runtime-main", reportedCurrentDirectory: "/tmp/inside-shell"))
        let model = WorkspaceLauncherModel(daemon: daemon)

        model.openAddPane(direction: .horizontal)

        XCTAssertEqual(model.path, "/tmp/inside-shell")
    }

    func testAddPaneFallsBackToWorkspaceStartLocationWithoutReportedDirectory() {
        let daemon = DaemonConnection(
            initialSettings: ["new_pane_choice": "terminal"],
            initialWorkspaces: [workspace()],
            selectedWorkspaceID: "workspace-1"
        )
        let model = WorkspaceLauncherModel(daemon: daemon)

        model.openAddPane(direction: .horizontal)

        XCTAssertEqual(model.path, "/tmp/workspace-default")
    }

    func testLocationArrowNavigationAndAcceptActionAdoptSelectedPath() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.applyAvailableLocations(
            recents: [
                RecentLocationSnapshot(path: "/tmp/one", label: "one", lastSeen: "", useCount: 1),
                RecentLocationSnapshot(path: "/tmp/two", label: "two", lastSeen: "", useCount: 1),
            ],
            directories: []
        )

        model.moveLocationSelection(.down)
        XCTAssertEqual(model.highlightedLocationPath, "/tmp/one")
        model.moveLocationSelection(.down)
        XCTAssertEqual(model.highlightedLocationPath, "/tmp/two")
        model.perform(.acceptLocation)
        XCTAssertEqual(model.path, "/tmp/two")
    }

    func testLocationSelectionKeepsHomePathsAbbreviatedForDisplay() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.applyAvailableLocations(
            recents: [
                RecentLocationSnapshot(path: "/Users/victora/projects/attn", label: "attn", lastSeen: "", useCount: 1),
            ],
            directories: [],
            homePath: "/Users/victora"
        )

        model.selectLocation("/Users/victora/projects/attn")

        XCTAssertEqual(model.path, "~/projects/attn")
    }

    func testTildeQueryFiltersAndCompletesAgainstAbsoluteDaemonLocations() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.applyAvailableLocations(
            recents: [
                RecentLocationSnapshot(path: "/Users/victora/projects/attn", label: "attn", lastSeen: "", useCount: 1),
            ],
            directories: [],
            homePath: "/Users/victora"
        )

        model.updatePath("~/projects/at")

        XCTAssertEqual(model.visibleLocations.map(\.path), ["/Users/victora/projects/attn"])
        XCTAssertEqual(model.ghostCompletion, "~/projects/attn")
        XCTAssertEqual(model.ghostCompletionSuffix, "tn")
    }

    func testAbsoluteTypedQueryIsNotRewrittenAfterHomePathIsKnown() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.applyAvailableLocations(recents: [], directories: [], homePath: "/Users/victora")

        model.updatePath("/Users/victora/projects/attn")

        XCTAssertEqual(model.path, "/Users/victora/projects/attn")
    }

    func testLocationTabCompletesTheHighlightedRowInsteadOfTheFirstGhostCandidate() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.updatePath("/tmp/")
        model.applyAvailableLocations(
            recents: [
                RecentLocationSnapshot(path: "/tmp/one", label: "one", lastSeen: "", useCount: 1),
                RecentLocationSnapshot(path: "/tmp/two", label: "two", lastSeen: "", useCount: 1),
            ],
            directories: []
        )

        model.moveLocationSelection(.down)
        model.moveLocationSelection(.down)
        model.applyCompletion()

        XCTAssertEqual(model.path, "/tmp/two")
        XCTAssertNil(model.highlightedLocationPath)
    }

    func testGhostCompletionExposesOnlyTheVisibleSuffixAndSkipsEmptyInput() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.applyAvailableLocations(
            recents: [
                RecentLocationSnapshot(path: "/tmp/projects", label: "projects", lastSeen: "", useCount: 1),
            ],
            directories: []
        )

        model.updatePath("")
        XCTAssertNil(model.ghostCompletionSuffix)

        model.updatePath("/tmp/pro")
        XCTAssertEqual(model.ghostCompletionSuffix, "jects")
    }

    func testMatchingDirectoryIsHiddenWhenAlreadyShownAsARecentLocation() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.updatePath("/tmp/project")
        model.applyAvailableLocations(
            recents: [
                RecentLocationSnapshot(path: "/tmp/project", label: "project", lastSeen: "", useCount: 1),
            ],
            directories: [
                DirectoryEntrySnapshot(name: "project", path: "/tmp/project"),
                DirectoryEntrySnapshot(name: "project-other", path: "/tmp/project-other"),
            ]
        )

        XCTAssertEqual(model.visibleDirectoryEntries.map(\.path), ["/tmp/project-other"])
    }

    func testApplyingTypedDirectoryBrowseUpdatesDisplayedDirectoryAndVisibleRows() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.applyAvailableLocations(recents: [], directories: [], homePath: "/Users/victora")

        model.applyBrowseDirectoryResult(BrowseDirectoryResultMessage(
            requestID: nil,
            directory: "/Users/victora/src",
            entries: [DirectoryEntrySnapshot(name: "attn", path: "/Users/victora/src/attn")],
            homePath: "/Users/victora",
            success: true,
            error: nil
        ))

        XCTAssertEqual(model.browsedDirectory, "/Users/victora/src")
        XCTAssertEqual(model.displayPath(try! XCTUnwrap(model.browsedDirectory)), "~/src")
        XCTAssertEqual(model.visibleDirectoryEntries.map(\.path), ["/Users/victora/src/attn"])
    }

    func testWorktreeCreationRemainsInlineAndStartsFromSelectedWorktreeBranch() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.applyRepositoryDestinations(RepoInfoSnapshot(
            repo: "/tmp/repo",
            currentBranch: "main",
            currentCommitHash: "deadbeef",
            currentCommitTime: "",
            defaultBranch: "main",
            worktrees: [WorktreeSnapshot(path: "/tmp/feature", branch: "feature")]
        ))

        XCTAssertTrue(model.isHighlightedDestination(0))
        model.moveDestinationSelection(.down)
        XCTAssertTrue(model.isHighlightedDestination(1))
        model.moveDestinationSelection(.down)
        XCTAssertTrue(model.isHighlightedDestination(2))
        model.acceptHighlightedDestination()
        XCTAssertEqual(model.stage, .destinations)
        XCTAssertTrue(model.isCreatingWorktree)
        XCTAssertEqual(model.worktreeStartingFrom, "feature")
    }

    func testCreateWorktreeHighlightDoesNotLeaveItsSourceBranchHighlighted() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.applyRepositoryDestinations(RepoInfoSnapshot(
            repo: "/tmp/repo",
            currentBranch: "main",
            currentCommitHash: "deadbeef",
            currentCommitTime: "",
            defaultBranch: "main",
            worktrees: [WorktreeSnapshot(path: "/tmp/feature", branch: "feature")]
        ))

        model.moveDestinationSelection(.down)
        XCTAssertEqual(model.worktreeStartingFrom, "feature")
        model.moveDestinationSelection(.down)

        XCTAssertFalse(model.isHighlightedDestination(1))
        XCTAssertTrue(model.isHighlightedDestination(2))
        XCTAssertEqual(model.worktreeStartingFrom, "feature")
    }

    func testWorktreeCreationStartsFromMainCheckoutWhenNoWorktreeWasSelected() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.applyRepositoryDestinations(RepoInfoSnapshot(
            repo: "/tmp/repo",
            currentBranch: "topic/main-checkout",
            currentCommitHash: "deadbeef",
            currentCommitTime: "",
            defaultBranch: "main",
            worktrees: [WorktreeSnapshot(path: "/tmp/feature", branch: "feature")]
        ))
        model.showCreateWorktree()

        XCTAssertTrue(model.isCreatingWorktree)
        XCTAssertEqual(model.worktreeStartingFrom, "topic/main-checkout")
    }

    func testWorktreeTabSwitchesBetweenSelectedBranchAndDefaultBranch() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.applyRepositoryDestinations(RepoInfoSnapshot(
            repo: "/tmp/repo",
            currentBranch: "main",
            currentCommitHash: "deadbeef",
            currentCommitTime: "",
            defaultBranch: "main",
            worktrees: [WorktreeSnapshot(path: "/tmp/feature", branch: "feature")]
        ))
        model.moveDestinationSelection(.down)
        model.moveDestinationSelection(.down)
        model.acceptHighlightedDestination()

        XCTAssertEqual(model.worktreeStartingFrom, "feature")
        model.toggleWorktreeStartBranch()
        XCTAssertEqual(model.worktreeStartingFrom, "origin/main")
        model.toggleWorktreeStartBranch()
        XCTAssertEqual(model.worktreeStartingFrom, "feature")
    }

    func testEscapeFromInlineWorktreeFormRestoresSelectedDestination() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.applyRepositoryDestinations(RepoInfoSnapshot(
            repo: "/tmp/repo",
            currentBranch: "main",
            currentCommitHash: "deadbeef",
            currentCommitTime: "",
            defaultBranch: "main",
            worktrees: [WorktreeSnapshot(path: "/tmp/feature", branch: "feature")]
        ))
        model.moveDestinationSelection(.down)
        model.moveDestinationSelection(.down)
        model.acceptHighlightedDestination()

        model.cancel()

        XCTAssertEqual(model.stage, .destinations)
        XCTAssertFalse(model.isCreatingWorktree)
        XCTAssertTrue(model.isHighlightedDestination(1))
    }

    func testEscapeFromDestinationsReturnsToLocationThenClosesLauncher() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.openNewWorkspace()
        model.applyRepositoryDestinations(RepoInfoSnapshot(
            repo: "/tmp/repo",
            currentBranch: "main",
            currentCommitHash: "deadbeef",
            currentCommitTime: "",
            defaultBranch: "main",
            worktrees: []
        ))

        model.cancel()
        XCTAssertEqual(model.stage, .location)
        XCTAssertTrue(model.isPresented)

        model.cancel()
        XCTAssertFalse(model.isPresented)
    }

    func testDestinationKeyboardResponderMovesSelectionWithoutTabFocus() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())
        model.applyRepositoryDestinations(RepoInfoSnapshot(
            repo: "/tmp/repo",
            currentBranch: "main",
            currentCommitHash: "deadbeef",
            currentCommitTime: "",
            defaultBranch: "main",
            worktrees: [WorktreeSnapshot(path: "/tmp/feature", branch: "feature")]
        ))
        let responder = WorkspaceLauncherDestinationKeyResponder { command in
            switch command {
            case .up:
                model.perform(.moveDestination(.up))
            case .down:
                model.perform(.moveDestination(.down))
            case .accept:
                model.perform(.acceptDestination)
            case .cancel:
                model.cancel()
            }
        }

        XCTAssertTrue(responder.acceptsFirstResponder)
        responder.keyDown(with: keyEvent(keyCode: 125))
        XCTAssertTrue(model.isHighlightedDestination(1))
        responder.keyDown(with: keyEvent(keyCode: 125))
        XCTAssertTrue(model.isHighlightedDestination(2))
        responder.keyDown(with: keyEvent(keyCode: 36))
        XCTAssertEqual(model.stage, .destinations)
        XCTAssertTrue(model.isCreatingWorktree)
    }

    func testYoloIsDisabledWhenSelectedAgentLacksCapability() {
        let daemon = DaemonConnection(initialSettings: ["codex_available": "true", "codex_cap_yolo": "false"])
        let model = WorkspaceLauncherModel(daemon: daemon)

        model.selectPaneChoice(.codex)
        model.setYoloMode(true)

        XCTAssertFalse(model.yoloSupported)
        XCTAssertFalse(model.yoloMode)
    }

    func testAgentsAndYoloStayUnavailableUntilDaemonAdvertisesCapabilities() {
        let model = WorkspaceLauncherModel(daemon: DaemonConnection())

        model.openNewWorkspace()
        XCTAssertFalse(model.isAvailable(.codex))
        model.selectPaneChoice(.codex)

        XCTAssertEqual(model.paneChoice, .terminal)
        XCTAssertFalse(model.yoloSupported)
    }

    private func workspace() -> WorkspaceSnapshot {
        WorkspaceSnapshot(
            id: "workspace-1",
            title: "Workspace",
            directory: "/tmp/workspace-default",
            status: "idle",
            layout: WorkspaceLayoutSnapshot(
                workspaceID: "workspace-1",
                activePaneID: "main",
                layoutJSON: #"{"type":"pane","pane_id":"main"}"#,
                panes: [
                    WorkspacePaneSnapshot(
                        paneID: "main",
                        runtimeID: "runtime-main",
                        sessionID: nil,
                        kind: "shell",
                        title: "Terminal"
                    ),
                ]
            )
        )
    }

    private func keyEvent(keyCode: UInt16) -> NSEvent {
        NSEvent.keyEvent(
            with: .keyDown,
            location: .zero,
            modifierFlags: [],
            timestamp: 0,
            windowNumber: 0,
            context: nil,
            characters: "",
            charactersIgnoringModifiers: "",
            isARepeat: false,
            keyCode: keyCode
        )!
    }
}

@MainActor
private final class StubTerminalSurface: TerminalSurface {
    let runtimeID: String
    let automationIdentity = "stub"
    let geometry = TerminalGeometry(columns: 80, rows: 24)
    let isFocusedForRendering = true
    let hasInputFocus = true
    let focusLossWrites = 0
    let mouseCaptured = false
    let reportedCurrentDirectory: String?

    init(runtimeID: String, reportedCurrentDirectory: String?) {
        self.runtimeID = runtimeID
        self.reportedCurrentDirectory = reportedCurrentDirectory
    }

    func processOutput(_ data: Data) {}
    func processReplay(_ data: Data) {}
    func typeText(_ text: String) {}
    func pressEnter() {}
    func copySelectionToClipboard() {}
    func pasteFromClipboard() {}
    func readVisibleText() -> String { "" }
    func readSelectionText() -> String? { nil }
    func movePointer(toColumn column: Int, row: Int) {}
    func clickCell(column: Int, row: Int) {}
    func dragSelection(fromColumn startColumn: Int, row startRow: Int, toColumn endColumn: Int, row endRow: Int) {}
    func focus() {}
    func focusForHardwareInput() {}
}
