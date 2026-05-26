import XCTest
@testable import AttnNative

@MainActor
final class NativeAutomationActionsTests: XCTestCase {
    func testPingIsReadyBeforeTerminalSurfaceExists() {
        let actions = makeActions()

        let result = actions.dispatch(action: "ping", payload: .null)

        guard case .success(let payload) = result else {
            return XCTFail("expected successful ping")
        }
        XCTAssertEqual(payload["frontendReady"], .bool(true))
    }

    func testTerminalActionFailsUntilDaemonPaneIsVisible() {
        let actions = makeActions()

        let result = actions.dispatch(action: "type_terminal", payload: .object([
            "text": .string("echo ready\n"),
        ]))

        guard case .failure(let error) = result else {
            return XCTFail("expected terminal action failure without a visible daemon pane")
        }
        XCTAssertEqual(error.message, "terminal surface not found")
    }

    func testWorkspaceSelectionFailsUntilWorkspaceIsKnown() {
        let actions = makeActions()

        let result = actions.dispatch(action: "select_workspace", payload: .object([
            "workspace_id": .string("not-yet-visible"),
        ]))

        guard case .failure(let error) = result else {
            return XCTFail("expected selection to fail for an unknown workspace")
        }
        XCTAssertEqual(error.message, "workspace not found: not-yet-visible")
    }

    func testBackgroundModeCanBeChangedThroughAction() {
        let actions = makeActions()

        let result = actions.dispatch(
            action: "set_window_background_mode",
            payload: .object(["enabled": .bool(true)])
        )

        guard case .success(let payload) = result else {
            return XCTFail("expected background mode update")
        }
        XCTAssertEqual(payload["backgroundMode"], .bool(true))
        guard case .success(let state) = actions.dispatch(action: "get_state", payload: .null) else {
            return XCTFail("expected state snapshot")
        }
        XCTAssertEqual(state["backgroundMode"], .bool(true))
    }

    func testNewWorkspaceDialogCanBeOpenedAndCancelledThroughAutomation() {
        let actions = makeActions()

        guard case .success(let opened) = actions.dispatch(action: "open_new_workspace_dialog", payload: .null) else {
            return XCTFail("expected launcher to open")
        }
        XCTAssertEqual(opened["mode"], .string("new_workspace"))
        XCTAssertEqual(opened["presented"], .string("true"))

        guard case .success(let closed) = actions.dispatch(action: "cancel_launcher", payload: .null) else {
            return XCTFail("expected launcher to close")
        }
        XCTAssertEqual(closed["presented"], .string("false"))
    }

    func testLauncherSemanticActionTogglesYoloWithoutHardwareFocus() {
        let actions = makeActions(initialSettings: [
            "codex_available": "true",
            "codex_cap_yolo": "true",
        ])

        _ = actions.dispatch(action: "open_new_workspace_dialog", payload: .null)
        _ = actions.dispatch(action: "set_launcher_choice", payload: .object(["choice": .string("codex")]))
        let result = actions.dispatch(
            action: "perform_launcher_action",
            payload: .object(["action": .string("toggle_local_yolo")])
        )

        guard case .success(let state) = result else {
            return XCTFail("expected semantic launcher action to succeed")
        }
        XCTAssertEqual(state["yoloMode"], .string("true"))
    }

    func testPaneSnapshotSurfacesReportedTerminalDirectory() {
        let daemon = DaemonConnection(
            initialWorkspaces: [workspaceWithPane()],
            selectedWorkspaceID: "workspace-1"
        )
        daemon.register(surface: AutomationStubSurface(runtimeID: "runtime-1", directory: "/tmp/current"))
        let actions = NativeAutomationActions(
            daemon: daemon,
            launcher: WorkspaceLauncherModel(daemon: daemon),
            profile: AutomationProfile.current(environment: ["ATTN_AUTOMATION": "1"], bundleIdentifier: "com.attn.native")
        )

        guard case .success(let response) = actions.dispatch(action: "list_panes", payload: .null),
              case .array(let panes) = response["panes"],
              let pane = panes.first else {
            return XCTFail("expected terminal pane snapshot")
        }
        XCTAssertEqual(pane["reportedCurrentDirectory"], .string("/tmp/current"))
    }

    func testPressTerminalEnterUsesTerminalKeyInputWithoutHardwareFocus() {
        let daemon = DaemonConnection(
            initialWorkspaces: [workspaceWithPane()],
            selectedWorkspaceID: "workspace-1"
        )
        let surface = AutomationStubSurface(runtimeID: "runtime-1", directory: nil)
        daemon.register(surface: surface)
        let actions = NativeAutomationActions(
            daemon: daemon,
            launcher: WorkspaceLauncherModel(daemon: daemon),
            profile: AutomationProfile.current(environment: ["ATTN_AUTOMATION": "1"], bundleIdentifier: "com.attn.native")
        )

        let result = actions.dispatch(
            action: "press_terminal_enter",
            payload: .object(["runtime_id": .string("runtime-1")])
        )

        guard case .success = result else {
            return XCTFail("expected terminal key input to succeed")
        }
        XCTAssertEqual(surface.enterPresses, 1)
    }

    func testTerminalClipboardActionsUseSurfaceBindingsWithoutHardwareFocus() {
        let daemon = DaemonConnection(
            initialWorkspaces: [workspaceWithPane()],
            selectedWorkspaceID: "workspace-1"
        )
        let surface = AutomationStubSurface(runtimeID: "runtime-1", directory: nil)
        daemon.register(surface: surface)
        let actions = NativeAutomationActions(
            daemon: daemon,
            launcher: WorkspaceLauncherModel(daemon: daemon),
            profile: AutomationProfile.current(environment: ["ATTN_AUTOMATION": "1"], bundleIdentifier: "com.attn.native")
        )

        guard case .success = actions.dispatch(
            action: "copy_terminal_selection",
            payload: .object(["runtime_id": .string("runtime-1")])
        ), case .success = actions.dispatch(
            action: "paste_terminal_clipboard",
            payload: .object(["runtime_id": .string("runtime-1")])
        ) else {
            return XCTFail("expected terminal clipboard actions to succeed")
        }

        XCTAssertEqual(surface.copyRequests, 1)
        XCTAssertEqual(surface.pasteRequests, 1)
    }

    private func makeActions(initialSettings: [String: String] = [:]) -> NativeAutomationActions {
        let daemon = DaemonConnection(initialSettings: initialSettings)
        return NativeAutomationActions(
            daemon: daemon,
            launcher: WorkspaceLauncherModel(daemon: daemon),
            profile: AutomationProfile.current(
                environment: ["ATTN_AUTOMATION": "1"],
                bundleIdentifier: "com.attn.native"
            )
        )
    }

    private func workspaceWithPane() -> WorkspaceSnapshot {
        WorkspaceSnapshot(
            id: "workspace-1",
            title: "Workspace",
            directory: "/tmp",
            status: "idle",
            layout: WorkspaceLayoutSnapshot(
                workspaceID: "workspace-1",
                activePaneID: "main",
                layoutJSON: #"{"type":"pane","pane_id":"main"}"#,
                panes: [WorkspacePaneSnapshot(paneID: "main", runtimeID: "runtime-1", sessionID: nil, kind: "shell", title: "Terminal")]
            )
        )
    }
}

@MainActor
private final class AutomationStubSurface: TerminalSurface {
    let runtimeID: String
    let automationIdentity = "automation-stub"
    let geometry = TerminalGeometry(columns: 80, rows: 24)
    let isFocusedForRendering = true
    let hasInputFocus = false
    let focusLossWrites = 0
    let mouseCaptured = false
    let reportedCurrentDirectory: String?
    private(set) var enterPresses = 0
    private(set) var copyRequests = 0
    private(set) var pasteRequests = 0

    init(runtimeID: String, directory: String?) {
        self.runtimeID = runtimeID
        reportedCurrentDirectory = directory
    }

    func processOutput(_ data: Data) {}
    func processReplay(_ data: Data) {}
    func typeText(_ text: String) {}
    func pressEnter() { enterPresses += 1 }
    func copySelectionToClipboard() { copyRequests += 1 }
    func pasteFromClipboard() { pasteRequests += 1 }
    func readVisibleText() -> String { "" }
    func readSelectionText() -> String? { nil }
    func movePointer(toColumn column: Int, row: Int) {}
    func clickCell(column: Int, row: Int) {}
    func dragSelection(fromColumn startColumn: Int, row startRow: Int, toColumn endColumn: Int, row endRow: Int) {}
    func focus() {}
    func focusForHardwareInput() {}
}
