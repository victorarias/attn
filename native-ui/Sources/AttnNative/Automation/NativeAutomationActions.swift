import AppKit
import Foundation

@MainActor
final class NativeAutomationActions {
    private let daemon: DaemonConnection
    private let launcher: WorkspaceLauncherModel
    private let eventLog = AutomationEventLog()
    private let closeSelectedContent: (() -> Void)?
    private var backgroundMode: Bool

    init(
        daemon: DaemonConnection,
        launcher: WorkspaceLauncherModel,
        profile: AutomationProfile,
        closeSelectedContent: (() -> Void)? = nil
    ) {
        self.daemon = daemon
        self.launcher = launcher
        self.closeSelectedContent = closeSelectedContent
        self.backgroundMode = profile.backgroundWindow
    }

    func applyInitialWindowModeWhenAvailable(remainingAttempts: Int = 20) {
        guard backgroundMode else { return }
        guard NSApplication.shared.windows.first == nil else {
            applyBackgroundMode(true)
            return
        }
        guard remainingAttempts > 0 else { return }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) { [weak self] in
            self?.applyInitialWindowModeWhenAvailable(remainingAttempts: remainingAttempts - 1)
        }
    }

    func dispatch(action: String, payload: JSONValue) -> AutomationActionResult {
        eventLog.record(action: action)
        switch action {
        case "ping":
            return .success(.object([
                "pong": .bool(true),
                "frontendReady": .bool(true),
                "pid": .number(Double(ProcessInfo.processInfo.processIdentifier)),
            ]))
        case "get_state":
            return .success(stateSnapshot())
        case "list_panes":
            return .success(.object(["panes": .array(panesSnapshot())]))
        case "select_workspace":
            guard let workspaceID = payload["workspace_id"]?.stringValue ?? payload["workspaceId"]?.stringValue else {
                return .failure("payload.workspace_id (string) is required")
            }
            guard daemon.selectWorkspace(workspaceID) else {
                return .failure("workspace not found: \(workspaceID)")
            }
            return .success(.object(["workspaceId": .string(workspaceID)]))
        case "navigate":
            guard let rawDirection = payload["direction"]?.stringValue,
                  let direction = WorkspaceNavigationDirection(rawValue: rawDirection) else {
                return .failure("payload.direction must be left, right, up, or down")
            }
            let intent = daemon.navigate(direction)
            switch intent {
            case .focusPane(let workspaceID, let paneID):
                return .success(.object([
                    "result": .string("focus_pane"),
                    "workspaceId": .string(workspaceID),
                    "paneId": .string(paneID),
                ]))
            case .selectWorkspace(let workspaceID):
                return .success(.object([
                    "result": .string("select_workspace"),
                    "workspaceId": .string(workspaceID),
                ]))
            case .none:
                return .success(.object(["result": .string("none")]))
            }
        case "open_new_workspace_dialog":
            launcher.openNewWorkspace()
            return .success(launcherSnapshot())
        case "open_add_pane_dialog":
            let rawDirection = payload["direction"]?.stringValue ?? "vertical"
            guard let direction = SplitDirection(rawValue: rawDirection) else {
                return .failure("payload.direction must be horizontal or vertical")
            }
            launcher.openAddPane(direction: direction)
            return .success(launcherSnapshot())
        case "quick_split":
            let rawDirection = payload["direction"]?.stringValue ?? "vertical"
            guard let direction = SplitDirection(rawValue: rawDirection) else {
                return .failure("payload.direction must be horizontal or vertical")
            }
            launcher.quickSplit(direction)
            return .success(.object(["direction": .string(direction.rawValue)]))
        case "get_launcher_state":
            return .success(launcherSnapshot())
        case "set_launcher_path":
            guard let path = payload["path"]?.stringValue else {
                return .failure("payload.path (string) is required")
            }
            launcher.updatePath(path)
            return .success(launcherSnapshot())
        case "set_launcher_choice":
            guard let value = payload["choice"]?.stringValue,
                  let choice = WorkspaceLauncherModel.PaneChoice(rawValue: value) else {
                return .failure("payload.choice must be terminal, claude, codex, copilot, or pi")
            }
            guard launcher.isAvailable(choice) else {
                return .failure("requested launcher choice is unavailable")
            }
            launcher.selectPaneChoice(choice)
            if let yolo = payload["yolo"]?.boolValue {
                launcher.setYoloMode(yolo)
            }
            return .success(launcherSnapshot())
        case "perform_launcher_action":
            guard let value = payload["action"]?.stringValue else {
                return .failure("payload.action (string) is required")
            }
            switch value {
            case "toggle_local_yolo":
                launcher.perform(.toggleLocalYolo)
            case "move_location_up":
                launcher.perform(.moveLocation(.up))
            case "move_location_down":
                launcher.perform(.moveLocation(.down))
            case "accept_location":
                launcher.perform(.acceptLocation)
            case "move_destination_up":
                launcher.perform(.moveDestination(.up))
            case "move_destination_down":
                launcher.perform(.moveDestination(.down))
            case "accept_destination":
                launcher.perform(.acceptDestination)
            case "toggle_worktree_start_branch":
                launcher.perform(.toggleWorktreeStartBranch)
            default:
                return .failure("unsupported launcher action: \(value)")
            }
            return .success(launcherSnapshot())
        case "submit_launcher_location":
            launcher.confirmLocation()
            return .success(launcherSnapshot())
        case "choose_launcher_destination":
            guard let path = payload["path"]?.stringValue else {
                return .failure("payload.path (string) is required")
            }
            launcher.chooseDestination(path)
            return .success(launcherSnapshot())
        case "cancel_launcher":
            launcher.cancel()
            return .success(launcherSnapshot())
        case "close_selected_content":
            guard let closeSelectedContent else {
                return .failure("close action is unavailable")
            }
            closeSelectedContent()
            return .success(.object(["requested": .bool(true)]))
        case "close_window":
            guard let window = NSApplication.shared.windows.first(where: { $0.sheetParent == nil }) else {
                return .failure("no main native window")
            }
            window.performClose(nil)
            return .success(.object(["requested": .bool(true)]))
        case "tail_events":
            return .success(eventLog.tail(since: payload["since_id"]?.numberValue.map(UInt64.init) ?? 0))
        case "get_window_bounds":
            return windowBounds()
        case "set_window_background_mode":
            guard let enabled = payload["enabled"]?.boolValue else {
                return .failure("payload.enabled (boolean) is required")
            }
            applyBackgroundMode(enabled)
            return .success(.object(["backgroundMode": .bool(backgroundMode)]))
        case "park_window":
            return parkWindow(payload: payload)
        case "screenshot", "screenshot_window":
            return screenshot(payload: payload)
        case "focus_pane":
            guard let surface = surface(from: payload) else {
                return .failure("terminal surface not found")
            }
            daemon.focusPane(runtimeID: surface.runtimeID)
            if payload["key_window"]?.boolValue == true {
                surface.focusForHardwareInput()
            } else {
                surface.focus()
            }
            return .success(.object(["runtimeId": .string(surface.runtimeID)]))
        case "type_terminal", "type_pane_via_ui":
            guard let surface = surface(from: payload) else {
                return .failure("terminal surface not found")
            }
            guard let text = payload["text"]?.stringValue else {
                return .failure("payload.text (string) is required")
            }
            surface.typeText(text)
            return .success(.object([
                "runtimeId": .string(surface.runtimeID),
                "bytes": .number(Double(text.utf8.count)),
            ]))
        case "press_terminal_enter":
            guard let surface = surface(from: payload) else {
                return .failure("terminal surface not found")
            }
            surface.pressEnter()
            return .success(.object(["runtimeId": .string(surface.runtimeID)]))
        case "copy_terminal_selection":
            guard let surface = surface(from: payload) else {
                return .failure("terminal surface not found")
            }
            surface.copySelectionToClipboard()
            return .success(.object(["runtimeId": .string(surface.runtimeID)]))
        case "paste_terminal_clipboard":
            guard let surface = surface(from: payload) else {
                return .failure("terminal surface not found")
            }
            surface.pasteFromClipboard()
            return .success(.object(["runtimeId": .string(surface.runtimeID)]))
        case "read_pane_text":
            guard let surface = surface(from: payload) else {
                return .failure("terminal surface not found")
            }
            return .success(.object([
                "runtimeId": .string(surface.runtimeID),
                "text": .string(surface.readVisibleText()),
            ]))
        case "read_terminal_selection":
            guard let surface = surface(from: payload) else {
                return .failure("terminal surface not found")
            }
            return .success(.object([
                "runtimeId": .string(surface.runtimeID),
                "text": surface.readSelectionText().map(JSONValue.string) ?? .null,
            ]))
        case "move_terminal_pointer":
            guard let surface = surface(from: payload) else {
                return .failure("terminal surface not found")
            }
            guard let column = cellCoordinate("column", in: payload),
                  let row = cellCoordinate("row", in: payload) else {
                return .failure("payload.column and payload.row must be non-negative numbers")
            }
            surface.movePointer(toColumn: column, row: row)
            return .success(.object(["runtimeId": .string(surface.runtimeID)]))
        case "click_terminal_cell":
            guard let surface = surface(from: payload) else {
                return .failure("terminal surface not found")
            }
            guard let column = cellCoordinate("column", in: payload),
                  let row = cellCoordinate("row", in: payload) else {
                return .failure("payload.column and payload.row must be non-negative numbers")
            }
            surface.clickCell(column: column, row: row)
            return .success(.object(["runtimeId": .string(surface.runtimeID)]))
        case "drag_terminal_selection":
            guard let surface = surface(from: payload) else {
                return .failure("terminal surface not found")
            }
            guard let startColumn = cellCoordinate("start_column", in: payload),
                  let startRow = cellCoordinate("start_row", in: payload),
                  let endColumn = cellCoordinate("end_column", in: payload),
                  let endRow = cellCoordinate("end_row", in: payload) else {
                return .failure("payload start/end row/column values must be non-negative numbers")
            }
            surface.dragSelection(
                fromColumn: startColumn,
                row: startRow,
                toColumn: endColumn,
                row: endRow
            )
            return .success(.object(["runtimeId": .string(surface.runtimeID)]))
        case "get_surface_geometry":
            guard let surface = surface(from: payload) else {
                return .failure("terminal surface not found")
            }
            return .success(.object([
                "runtimeId": .string(surface.runtimeID),
                "cols": .number(Double(surface.geometry.columns)),
                "rows": .number(Double(surface.geometry.rows)),
            ]))
        case "wait_for_terminal_text":
            guard let surface = surface(from: payload) else {
                return .failure("terminal surface not found")
            }
            guard let needle = payload["text"]?.stringValue else {
                return .failure("payload.text (string) is required")
            }
            let text = surface.readVisibleText()
            guard text.contains(needle) else {
                return .failure("terminal text not present yet")
            }
            return .success(.object(["text": .string(text)]))
        default:
            return .failure("unknown action: \(action)")
        }
    }

    private func stateSnapshot() -> JSONValue {
        .object([
            "app": .string("swift-native"),
            "daemonReady": .bool(daemon.state == .ready),
            "connectionState": .string(daemon.state.label),
            "lastEvent": daemon.lastEvent.map(JSONValue.string) ?? .null,
            "selectedWorkspaceId": daemon.selectedWorkspaceID.map(JSONValue.string) ?? .null,
            "launcher": launcherSnapshot(),
            "backgroundMode": .bool(backgroundMode),
            "panes": .array(panesSnapshot()),
            "capabilities": .array([
                .string("background_window"),
                .string("parked_window"),
                .string("native_screenshot"),
                .string("ghostty_terminal_surface"),
                .string("terminal_input"),
                .string("workspace_launcher"),
                .string("launcher_semantic_actions"),
                .string("terminal_key_input"),
            ]),
        ])
    }

    private func launcherSnapshot() -> JSONValue {
        .object(launcher.snapshot().mapValues(JSONValue.string))
    }

    private func panesSnapshot() -> [JSONValue] {
        daemon.visiblePanes.map { pane in
            let surface = daemon.terminalSurface(runtimeID: pane.runtimeID)
            let inactiveOverlayOpacity = daemon.selectedWorkspace?.layout.map {
                WorkspacePaneAppearance.inactiveOverlayOpacity(paneID: pane.paneID, layout: $0)
            } ?? 0
            return .object([
                "paneId": .string(pane.paneID),
                "runtimeId": pane.runtimeID.map(JSONValue.string) ?? .null,
                "kind": .string(pane.kind),
                "title": .string(pane.title),
                "attached": .bool(surface != nil),
                "surfaceIdentity": surface.map { .string($0.automationIdentity) } ?? .null,
                "inactiveOverlayOpacity": .number(inactiveOverlayOpacity),
                "focused": .bool(surface?.isFocusedForRendering ?? false),
                "inputFocused": .bool(surface?.hasInputFocus ?? false),
                "focusLossWrites": .number(Double(surface?.focusLossWrites ?? 0)),
                "mouseCaptured": .bool(surface?.mouseCaptured ?? false),
                "reportedCurrentDirectory": surface?.reportedCurrentDirectory.map(JSONValue.string) ?? .null,
            ])
        }
    }

    private func cellCoordinate(_ key: String, in payload: JSONValue) -> Int? {
        guard let rawValue = payload[key]?.numberValue,
              rawValue >= 0,
              rawValue.rounded(.towardZero) == rawValue else {
            return nil
        }
        return Int(rawValue)
    }

    private func surface(from payload: JSONValue) -> (any TerminalSurface)? {
        if let runtimeID = payload["runtime_id"]?.stringValue ?? payload["runtimeId"]?.stringValue {
            return daemon.terminalSurface(runtimeID: runtimeID)
        }
        if let paneID = payload["pane_id"]?.stringValue ?? payload["paneId"]?.stringValue,
           let runtimeID = daemon.visiblePanes.first(where: { $0.paneID == paneID })?.runtimeID {
            return daemon.terminalSurface(runtimeID: runtimeID)
        }
        return daemon.terminalSurface(runtimeID: nil)
    }

    private func applyBackgroundMode(_ enabled: Bool) {
        backgroundMode = enabled
        guard let window = NSApplication.shared.windows.first else { return }
        window.level = .normal
    }

    private func windowBounds() -> AutomationActionResult {
        guard let window = NSApplication.shared.windows.first else {
            return .failure("no native window")
        }
        let frame = window.frame
        return .success(.object([
            "windowId": .number(Double(window.windowNumber)),
            "scaleFactor": .number(Double(window.backingScaleFactor)),
            "minimized": .bool(window.isMiniaturized),
            "logicalBounds": .object([
                "x": .number(frame.origin.x),
                "y": .number(frame.origin.y),
                "width": .number(frame.width),
                "height": .number(frame.height),
            ]),
        ]))
    }

    private func parkWindow(payload: JSONValue) -> AutomationActionResult {
        guard let window = NSApplication.shared.windows.first else {
            return .failure("no native window")
        }
        guard let screen = NSScreen.main else {
            return .failure("no native screen")
        }
        let requestedPixels = payload["visible_px"]?.numberValue ?? payload["visiblePx"]?.numberValue ?? 20
        let visiblePixels = max(1, min(CGFloat(requestedPixels), window.frame.width))
        let screenFrame = screen.frame
        let origin = CGPoint(
            x: screenFrame.maxX - visiblePixels,
            y: screenFrame.minY + max(0, (screenFrame.height - window.frame.height) / 2)
        )
        window.setFrameOrigin(origin)
        return .success(.object([
            "parked": .bool(true),
            "visiblePx": .number(Double(visiblePixels)),
            "logicalBounds": .object([
                "x": .number(window.frame.origin.x),
                "y": .number(window.frame.origin.y),
                "width": .number(window.frame.width),
                "height": .number(window.frame.height),
            ]),
        ]))
    }

    private func screenshot(payload: JSONValue) -> AutomationActionResult {
        guard let window = NSApplication.shared.windows.first else {
            return .failure("no native window")
        }
        let path = payload["path"]?.stringValue ?? "/tmp/attn-native.png"
        let requestedWindowID = payload["windowId"]?.numberValue.map(Int.init) ?? window.windowNumber
        let directory = URL(fileURLWithPath: path).deletingLastPathComponent()
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
            process.arguments = ["-x", "-l", String(requestedWindowID), "-o", path]
            let standardError = Pipe()
            process.standardError = standardError
            try process.run()
            process.waitUntilExit()
            guard process.terminationStatus == 0 else {
                let error = standardError.fileHandleForReading.readDataToEndOfFile()
                let detail = String(data: error, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                return .failure("native screencapture failed\(detail.isEmpty ? "" : ": \(detail)")")
            }
            return .success(.object([
                "source": .string("native_process"),
                "path": .string(path),
                "windowId": .number(Double(requestedWindowID)),
            ]))
        } catch {
            return .failure("run screencapture: \(error.localizedDescription)")
        }
    }
}

@MainActor
private final class AutomationEventLog {
    private var nextID: UInt64 = 1
    private var events: [(id: UInt64, action: String)] = []

    func record(action: String) {
        events.append((nextID, action))
        nextID += 1
        if events.count > 256 {
            events.removeFirst(events.count - 256)
        }
    }

    func tail(since: UInt64) -> JSONValue {
        let values = events.filter { $0.id > since }.map { event in
            JSONValue.object([
                "id": .number(Double(event.id)),
                "kind": .string("automation_action"),
                "action": .string(event.action),
            ])
        }
        return .object([
            "events": .array(values),
            "next_cursor": .number(Double(nextID - 1)),
        ])
    }
}

private extension JSONValue {
    var numberValue: Double? {
        guard case .number(let value) = self else { return nil }
        return value
    }
}
