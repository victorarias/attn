import Foundation

@MainActor
final class DaemonConnection: ObservableObject {
    enum State: Equatable {
        case disconnected
        case connecting
        case waitingForInitialState
        case ready
        case failed(String)

        var label: String {
            switch self {
            case .disconnected:
                return "disconnected"
            case .connecting:
                return "connecting"
            case .waitingForInitialState:
                return "connected, awaiting state"
            case .ready:
                return "connected"
            case .failed(let reason):
                return "error: \(reason)"
            }
        }
    }

    @Published private(set) var state: State = .disconnected
    @Published private(set) var lastEvent: String?
    @Published private(set) var workspaces: [WorkspaceSnapshot] = []
    @Published private(set) var selectedWorkspaceID: String?
    @Published private(set) var settings: [String: String] = [:]

    let url: URL
    private var socket: URLSessionWebSocketTask?
    private var receiver: Task<Void, Never>?
    private var surfaces: [String: any TerminalSurface] = [:]
    private var pendingAttach: Set<String> = []
    private var bufferedOutput: [String: [PTYOutputMessage]] = [:]
    private var lastPTYSequence: [String: Int] = [:]
    private var recentRequests: [String: CheckedContinuation<RecentLocationsResultMessage, Error>] = [:]
    private var browseRequests: [String: CheckedContinuation<BrowseDirectoryResultMessage, Error>] = [:]
    private var inspectRequests: [String: CheckedContinuation<PathInspectionSnapshot, Error>] = [:]
    private var repoInfoRequest: CheckedContinuation<RepoInfoSnapshot, Error>?
    private var createWorktreeRequest: CheckedContinuation<String, Error>?
    private var bootstrapRequests: [String: CheckedContinuation<Void, Error>] = [:]
    private var spawnRequests: [String: CheckedContinuation<Void, Error>] = [:]
    private var splitRequest: CheckedContinuation<Void, Error>?
    private var closePaneRequest: CheckedContinuation<Void, Error>?
    private var unregisterRequests: [String: CheckedContinuation<Void, Error>] = [:]

    init(
        url: URL = AppEnvironment.daemonURL(),
        initialSettings: [String: String] = [:],
        initialWorkspaces: [WorkspaceSnapshot] = [],
        selectedWorkspaceID: String? = nil
    ) {
        self.url = url
        self.settings = initialSettings
        self.workspaces = initialWorkspaces
        self.selectedWorkspaceID = selectedWorkspaceID
    }

    func start() {
        guard socket == nil else { return }
        state = .connecting

        let socket = URLSession.shared.webSocketTask(with: url)
        self.socket = socket
        socket.resume()

        receiver = Task { [weak self] in
            guard let self else { return }
            do {
                try await socket.send(.data(JSONEncoder().encode(ClientHelloMessage())))
                self.state = .waitingForInitialState
                try await self.receiveEvents(from: socket)
            } catch is CancellationError {
                self.state = .disconnected
            } catch {
                self.state = .failed(error.localizedDescription)
                self.socket = nil
            }
        }
    }

    func stop() {
        receiver?.cancel()
        receiver = nil
        socket?.cancel(with: .goingAway, reason: nil)
        socket = nil
        state = .disconnected
    }

    var selectedWorkspace: WorkspaceSnapshot? {
        guard let selectedWorkspaceID else { return nil }
        return workspaces.first { $0.id == selectedWorkspaceID }
    }

    var visiblePanes: [WorkspacePaneSnapshot] {
        selectedWorkspace?.layout?.panes.filter { $0.runtimeID != nil } ?? []
    }

    @discardableResult
    func selectWorkspace(_ workspaceID: String) -> Bool {
        guard workspaces.contains(where: { $0.id == workspaceID }) else { return false }
        selectedWorkspaceID = workspaceID
        return true
    }

    func register(surface: any TerminalSurface) {
        guard surfaces[surface.runtimeID] == nil else { return }
        surfaces[surface.runtimeID] = surface
        pendingAttach.insert(surface.runtimeID)
        bufferedOutput[surface.runtimeID] = []
        send(AttachSessionCommand(id: surface.runtimeID))
    }

    func unregister(surface: any TerminalSurface) {
        guard let retained = surfaces[surface.runtimeID], retained === surface else { return }
        guard !visiblePanes.contains(where: { $0.runtimeID == surface.runtimeID }) else { return }
        surfaces.removeValue(forKey: surface.runtimeID)
        pendingAttach.remove(surface.runtimeID)
        bufferedOutput.removeValue(forKey: surface.runtimeID)
    }

    func terminalSurface(runtimeID: String?) -> (any TerminalSurface)? {
        let resolvedID = runtimeID ?? visiblePanes.first?.runtimeID
        guard let resolvedID else { return nil }
        return surfaces[resolvedID]
    }

    func sendTerminalInput(runtimeID: String, data: String) {
        send(PTYInputCommand(id: runtimeID, data: data))
    }

    func resizeTerminal(runtimeID: String, columns: Int, rows: Int) {
        guard columns > 0, rows > 0 else { return }
        send(PTYResizeCommand(id: runtimeID, cols: columns, rows: rows))
    }

    func focusPane(runtimeID: String) {
        guard let workspaceID = selectedWorkspaceID,
              let paneID = visiblePanes.first(where: { $0.runtimeID == runtimeID })?.paneID else { return }
        send(FocusPaneCommand(workspaceID: workspaceID, paneID: paneID))
    }

    @discardableResult
    func navigate(_ direction: WorkspaceNavigationDirection) -> WorkspaceNavigationIntent {
        let intent = WorkspaceNavigationPolicy.intent(
            direction: direction,
            selectedWorkspaceID: selectedWorkspaceID,
            workspaces: workspaces
        )
        switch intent {
        case .focusPane(let workspaceID, let paneID):
            send(FocusPaneCommand(workspaceID: workspaceID, paneID: paneID))
            if let runtimeID = selectedWorkspace?.layout?.panes.first(where: { $0.paneID == paneID })?.runtimeID {
                terminalSurface(runtimeID: runtimeID)?.focus()
            }
        case .selectWorkspace(let workspaceID):
            _ = selectWorkspace(workspaceID)
        case .none:
            break
        }
        return intent
    }

    func recentLocations() async throws -> RecentLocationsResultMessage {
        let requestID = UUID().uuidString
        return try await withCheckedThrowingContinuation { continuation in
            recentRequests[requestID] = continuation
            send(GetRecentLocationsCommand(requestID: requestID))
        }
    }

    func browseDirectory(_ path: String) async throws -> BrowseDirectoryResultMessage {
        let requestID = UUID().uuidString
        return try await withCheckedThrowingContinuation { continuation in
            browseRequests[requestID] = continuation
            send(BrowseDirectoryCommand(inputPath: path, requestID: requestID))
        }
    }

    func inspectPath(_ path: String) async throws -> PathInspectionSnapshot {
        let requestID = UUID().uuidString
        return try await withCheckedThrowingContinuation { continuation in
            inspectRequests[requestID] = continuation
            send(InspectPathCommand(path: path, requestID: requestID))
        }
    }

    func repoInfo(_ path: String) async throws -> RepoInfoSnapshot {
        guard repoInfoRequest == nil else {
            throw DaemonCommandError("A repository lookup is already running.")
        }
        return try await withCheckedThrowingContinuation { continuation in
            repoInfoRequest = continuation
            send(GetRepoInfoCommand(repo: path))
        }
    }

    func createWorktree(mainRepo: String, branch: String, startingFrom: String) async throws -> String {
        guard createWorktreeRequest == nil else {
            throw DaemonCommandError("A worktree operation is already running.")
        }
        return try await withCheckedThrowingContinuation { continuation in
            createWorktreeRequest = continuation
            send(CreateWorktreeCommand(mainRepo: mainRepo, branch: branch, startingFrom: startingFrom))
        }
    }

    func bootstrapWorkspace(_ command: BootstrapWorkspaceCommand) async throws {
        try await withCheckedThrowingContinuation { continuation in
            bootstrapRequests[command.id] = continuation
            send(command)
        }
    }

    func spawnSession(_ command: SpawnSessionCommand) async throws {
        try await withCheckedThrowingContinuation { continuation in
            spawnRequests[command.id] = continuation
            send(command)
        }
    }

    func splitPane(_ command: SplitPaneCommand) async throws {
        guard splitRequest == nil else {
            throw DaemonCommandError("A split operation is already running.")
        }
        try await withCheckedThrowingContinuation { continuation in
            splitRequest = continuation
            send(command)
        }
    }

    func closePane(_ command: ClosePaneCommand) async throws {
        guard closePaneRequest == nil else {
            throw DaemonCommandError("A close-pane operation is already running.")
        }
        try await withCheckedThrowingContinuation { continuation in
            closePaneRequest = continuation
            send(command)
        }
    }

    func unregisterWorkspace(_ workspaceID: String) async throws {
        guard unregisterRequests[workspaceID] == nil else {
            throw DaemonCommandError("That workspace is already closing.")
        }
        try await withCheckedThrowingContinuation { continuation in
            unregisterRequests[workspaceID] = continuation
            send(UnregisterWorkspaceCommand(id: workspaceID))
        }
    }

    func setSetting(_ key: String, value: String) {
        settings[key] = value
        send(SetSettingCommand(key: key, value: value))
    }

    private func send<T: Encodable>(_ command: T) {
        guard let socket else { return }
        Task {
            do {
                try await socket.send(.data(JSONEncoder().encode(command)))
            } catch {
                state = .failed(error.localizedDescription)
            }
        }
    }

    private func receiveEvents(from socket: URLSessionWebSocketTask) async throws {
        while !Task.isCancelled {
            let message = try await socket.receive()
            let data: Data
            switch message {
            case .data(let received):
                data = received
            case .string(let received):
                data = Data(received.utf8)
            @unknown default:
                continue
            }

            let header = try JSONDecoder().decode(ServerEventHeader.self, from: data)
            lastEvent = header.event
            switch header.event {
            case "initial_state":
                let message = try JSONDecoder().decode(InitialStateMessage.self, from: data)
                workspaces = message.workspaces ?? []
                settings = message.settings ?? [:]
                if selectedWorkspaceID == nil || !workspaces.contains(where: { $0.id == selectedWorkspaceID }) {
                    selectedWorkspaceID = workspaces.first?.id
                }
                state = .ready
            case "workspace_registered", "workspace_state_changed":
                let message = try JSONDecoder().decode(WorkspaceEventMessage.self, from: data)
                upsert(workspace: message.workspace)
            case "workspace_unregistered":
                let message = try JSONDecoder().decode(WorkspaceEventMessage.self, from: data)
                workspaces.removeAll { $0.id == message.workspace.id }
                if selectedWorkspaceID == message.workspace.id {
                    selectedWorkspaceID = workspaces.first?.id
                }
                unregisterRequests.removeValue(forKey: message.workspace.id)?.resume()
            case "workspace_layout", "workspace_layout_updated":
                let message = try JSONDecoder().decode(WorkspaceLayoutEventMessage.self, from: data)
                update(layout: message.workspaceLayout)
            case "attach_result":
                let message = try JSONDecoder().decode(AttachResultMessage.self, from: data)
                guard message.success, let surface = surfaces[message.id] else { break }
                // Attach bytes restore terminal state, but must not answer historical queries into the live PTY.
                for segment in message.replaySegments ?? [] {
                    if let bytes = Data(base64Encoded: segment.data) {
                        surface.processReplay(bytes)
                    }
                }
                if let scrollback = message.scrollback, let bytes = Data(base64Encoded: scrollback) {
                    surface.processReplay(bytes)
                }
                if let snapshot = message.screenSnapshot, let bytes = Data(base64Encoded: snapshot) {
                    surface.processReplay(bytes)
                }
                lastPTYSequence[message.id] = message.lastSeq ?? 0
                pendingAttach.remove(message.id)
                for output in (bufferedOutput.removeValue(forKey: message.id) ?? []).sorted(by: { $0.seq < $1.seq }) {
                    apply(output: output)
                }
            case "pty_output":
                let message = try JSONDecoder().decode(PTYOutputMessage.self, from: data)
                if pendingAttach.contains(message.id) {
                    bufferedOutput[message.id, default: []].append(message)
                } else {
                    apply(output: message)
                }
            case "recent_locations_result":
                let message = try JSONDecoder().decode(RecentLocationsResultMessage.self, from: data)
                guard let requestID = message.requestID,
                      let continuation = recentRequests.removeValue(forKey: requestID) else { break }
                if message.success {
                    continuation.resume(returning: message)
                } else {
                    continuation.resume(throwing: DaemonCommandError(message.error ?? "Could not load recent locations."))
                }
            case "browse_directory_result":
                let message = try JSONDecoder().decode(BrowseDirectoryResultMessage.self, from: data)
                guard let requestID = message.requestID,
                      let continuation = browseRequests.removeValue(forKey: requestID) else { break }
                if message.success {
                    continuation.resume(returning: message)
                } else {
                    continuation.resume(throwing: DaemonCommandError(message.error ?? "Could not browse that directory."))
                }
            case "inspect_path_result":
                let message = try JSONDecoder().decode(InspectPathResultMessage.self, from: data)
                guard let requestID = message.requestID,
                      let continuation = inspectRequests.removeValue(forKey: requestID) else { break }
                if message.success, let inspection = message.inspection {
                    continuation.resume(returning: inspection)
                } else {
                    continuation.resume(throwing: DaemonCommandError(message.error ?? "Could not inspect that location."))
                }
            case "get_repo_info_result":
                let message = try JSONDecoder().decode(GetRepoInfoResultMessage.self, from: data)
                guard let continuation = repoInfoRequest else { break }
                repoInfoRequest = nil
                if message.success, let info = message.info {
                    continuation.resume(returning: info)
                } else {
                    continuation.resume(throwing: DaemonCommandError(message.error ?? "Could not load repository destinations."))
                }
            case "create_worktree_result":
                let message = try JSONDecoder().decode(CreateWorktreeResultMessage.self, from: data)
                guard let continuation = createWorktreeRequest else { break }
                createWorktreeRequest = nil
                if message.success, let path = message.path {
                    continuation.resume(returning: path)
                } else {
                    continuation.resume(throwing: DaemonCommandError(message.error ?? "Could not create worktree."))
                }
            case "bootstrap_workspace_result":
                let message = try JSONDecoder().decode(BootstrapWorkspaceResultMessage.self, from: data)
                guard let continuation = bootstrapRequests.removeValue(forKey: message.workspaceID) else { break }
                if message.success {
                    continuation.resume()
                } else {
                    continuation.resume(throwing: DaemonCommandError(message.error ?? "Could not create workspace."))
                }
            case "spawn_result":
                let message = try JSONDecoder().decode(SpawnResultMessage.self, from: data)
                guard let continuation = spawnRequests.removeValue(forKey: message.id) else { break }
                if message.success {
                    continuation.resume()
                } else {
                    continuation.resume(throwing: DaemonCommandError(message.error ?? "Could not launch pane."))
                }
            case "workspace_layout_action_result":
                let message = try JSONDecoder().decode(WorkspaceLayoutActionResultMessage.self, from: data)
                let continuation: CheckedContinuation<Void, Error>?
                switch message.action {
                case "workspace_layout_split_pane":
                    continuation = splitRequest
                    splitRequest = nil
                case "workspace_layout_close_pane":
                    continuation = closePaneRequest
                    closePaneRequest = nil
                default:
                    continuation = nil
                }
                guard let continuation else { break }
                if message.success {
                    continuation.resume()
                } else {
                    continuation.resume(throwing: DaemonCommandError(message.error ?? "Workspace layout action failed."))
                }
            case "settings_updated":
                let message = try JSONDecoder().decode(SettingsUpdatedMessage.self, from: data)
                settings = message.settings ?? settings
            default:
                break
            }
        }
    }

    private func upsert(workspace: WorkspaceSnapshot) {
        if let index = workspaces.firstIndex(where: { $0.id == workspace.id }) {
            let existingLayout = workspaces[index].layout
            var updated = workspace
            if updated.layout == nil {
                updated.layout = existingLayout
            }
            workspaces[index] = updated
        } else {
            workspaces.append(workspace)
        }
        if selectedWorkspaceID == nil {
            selectedWorkspaceID = workspace.id
        }
    }

    private func update(layout: WorkspaceLayoutSnapshot) {
        guard let index = workspaces.firstIndex(where: { $0.id == layout.workspaceID }) else { return }
        workspaces[index].layout = layout
    }

    private func apply(output: PTYOutputMessage) {
        guard output.seq > (lastPTYSequence[output.id] ?? 0),
              let bytes = Data(base64Encoded: output.data) else { return }
        lastPTYSequence[output.id] = output.seq
        surfaces[output.id]?.processOutput(bytes)
    }
}

struct DaemonCommandError: LocalizedError {
    let message: String

    init(_ message: String) {
        self.message = message
    }

    var errorDescription: String? { message }
}
