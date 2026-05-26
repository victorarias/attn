import Foundation

@MainActor
final class WorkspaceLauncherModel: ObservableObject {
    enum Mode: Equatable {
        case newWorkspace
        case addPane(workspaceID: String, targetPaneID: String?, direction: SplitDirection)
    }

    enum Stage: Equatable {
        case location
        case destinations
    }

    enum SelectionDirection {
        case up
        case down
    }

    enum Action {
        case selectPane(PaneChoice)
        case toggleLocalYolo
        case moveLocation(SelectionDirection)
        case acceptLocation
        case moveDestination(SelectionDirection)
        case acceptDestination
        case toggleWorktreeStartBranch
    }

    enum PaneChoice: String, CaseIterable, Identifiable {
        case terminal
        case claude
        case codex
        case copilot
        case pi

        var id: String { rawValue }

        var label: String {
            switch self {
            case .terminal: return "Terminal"
            case .claude: return "Claude"
            case .codex: return "Codex"
            case .copilot: return "Copilot"
            case .pi: return "Pi"
            }
        }

        var agent: String {
            switch self {
            case .terminal: return "shell"
            default: return rawValue
            }
        }

    }

    @Published private(set) var mode: Mode?
    @Published private(set) var stage: Stage = .location
    @Published var paneChoice: PaneChoice = .codex
    @Published var yoloMode = false
    @Published var path = "~"
    @Published private(set) var recentLocations: [RecentLocationSnapshot] = []
    @Published private(set) var directoryEntries: [DirectoryEntrySnapshot] = []
    @Published private(set) var homePath: String?
    @Published private(set) var browsedDirectory: String?
    @Published private(set) var repoInfo: RepoInfoSnapshot?
    @Published private(set) var highlightedLocationPath: String?
    @Published private(set) var highlightedDestinationIndex: Int?
    @Published private(set) var selectedDestinationIndex: Int?
    @Published private(set) var isCreatingWorktree = false
    @Published var newBranch = ""
    @Published var worktreeStartFromDefault = false
    @Published private(set) var operation: String?
    @Published private(set) var error: String?

    private let daemon: DaemonConnection
    private var browseGeneration = UUID()
    private var initialPathAwaitingHomeAbbreviation: String?

    init(daemon: DaemonConnection) {
        self.daemon = daemon
    }

    var isPresented: Bool { mode != nil }

    var title: String {
        switch mode {
        case .newWorkspace: return "New Workspace"
        case .addPane: return "Add Pane"
        case nil: return ""
        }
    }

    var locationHeading: String {
        switch mode {
        case .newWorkspace: return "NEW WORKSPACE LOCATION"
        case .addPane: return "NEW PANE LOCATION"
        case nil: return "LOCATION"
        }
    }

    var direction: SplitDirection? {
        guard case .addPane(_, _, let direction) = mode else { return nil }
        return direction
    }

    var visibleLocations: [RecentLocationSnapshot] {
        let input = path.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !input.isEmpty, input != "~" else { return recentLocations }
        let search = expandedDisplayPath(input).lowercased()
        return recentLocations.filter { item in
            item.path.lowercased().contains(search) || item.label.lowercased().contains(search)
        }
    }

    var visibleDirectoryEntries: [DirectoryEntrySnapshot] {
        let visibleRecentPaths = Set(visibleLocations.map(\.path))
        return directoryEntries.filter { !visibleRecentPaths.contains($0.path) }
    }

    var ghostCompletion: String? {
        guard !path.isEmpty else { return nil }
        let candidate = selectableLocationPaths.first {
            displayPath($0).lowercased().hasPrefix(path.lowercased())
        }.map(displayPath)
        guard let candidate, candidate != path else { return nil }
        return candidate
    }

    var ghostCompletionSuffix: String? {
        guard let completion = ghostCompletion,
              let prefixEnd = completion.index(completion.startIndex, offsetBy: path.count, limitedBy: completion.endIndex) else {
            return nil
        }
        let suffix = String(completion[prefixEnd...])
        return suffix.isEmpty ? nil : suffix
    }

    var tabCompletion: String? {
        highlightedLocationPath.map(displayPath) ?? ghostCompletion
    }

    var yoloSupported: Bool {
        paneChoice != .terminal && daemon.settings["\(paneChoice.rawValue)_cap_yolo"] == "true"
    }

    var worktreeSelectedSourceBranch: String? {
        guard let repoInfo, let selectedDestinationIndex else { return nil }
        if selectedDestinationIndex == 0 {
            return repoInfo.currentBranch
        }
        let worktreeIndex = selectedDestinationIndex - 1
        guard repoInfo.worktrees.indices.contains(worktreeIndex) else { return nil }
        return repoInfo.worktrees[worktreeIndex].branch
    }

    var worktreeStartingFrom: String? {
        guard let repoInfo else { return nil }
        return worktreeStartFromDefault
            ? "origin/\(repoInfo.defaultBranch)"
            : (worktreeSelectedSourceBranch ?? repoInfo.currentBranch)
    }

    var isWorktreeCreationRunning: Bool {
        isCreatingWorktree && operation == "Creating worktree..."
    }

    func isAvailable(_ choice: PaneChoice) -> Bool {
        guard choice != .terminal else { return true }
        return daemon.settings["\(choice.rawValue)_available"] == "true"
    }

    func openNewWorkspace() {
        reset()
        mode = .newWorkspace
        paneChoice = preferredChoice()
        yoloMode = yoloSupported && daemon.settings["new_session_yolo_local"] == "true"
        path = "~"
        loadLocations()
    }

    func openAddPane(direction: SplitDirection) {
        guard let workspace = daemon.selectedWorkspace else {
            error = "Select a workspace before adding a pane."
            return
        }
        let targetPaneID = workspace.layout.flatMap { layout in
            layout.panes.contains(where: { $0.paneID == layout.activePaneID }) ? layout.activePaneID : nil
        }
        reset()
        mode = .addPane(workspaceID: workspace.id, targetPaneID: targetPaneID, direction: direction)
        paneChoice = preferredChoice()
        yoloMode = yoloSupported && daemon.settings["new_session_yolo_local"] == "true"
        let activeRuntimeID = workspace.layout?.panes.first(where: { $0.paneID == targetPaneID })?.runtimeID
        path = daemon.terminalSurface(runtimeID: activeRuntimeID)?.reportedCurrentDirectory ?? workspace.directory
        initialPathAwaitingHomeAbbreviation = path
        loadLocations()
    }

    func cancel() {
        if isCreatingWorktree {
            isCreatingWorktree = false
            newBranch = ""
            worktreeStartFromDefault = false
            highlightedDestinationIndex = selectedDestinationIndex
            return
        }
        if stage == .destinations {
            stage = .location
            repoInfo = nil
            return
        }
        mode = nil
    }

    func close() {
        mode = nil
    }

    func updatePath(_ value: String) {
        path = value
        initialPathAwaitingHomeAbbreviation = nil
        highlightedLocationPath = nil
        error = nil
        let generation = UUID()
        browseGeneration = generation
        Task {
            do {
                let result = try await daemon.browseDirectory(value)
                guard browseGeneration == generation else { return }
                applyBrowseDirectoryResult(result)
            } catch {
                guard browseGeneration == generation else { return }
                browsedDirectory = nil
                directoryEntries = []
                updateHighlightedLocationAfterResultsChanged()
            }
        }
    }

    func applyCompletion() {
        guard let tabCompletion else { return }
        updatePath(tabCompletion)
    }

    func selectPaneChoice(_ choice: PaneChoice) {
        guard isAvailable(choice) else { return }
        paneChoice = choice
        daemon.setSetting("new_pane_choice", value: choice.rawValue)
        if choice != .terminal {
            daemon.setSetting("new_session_agent", value: choice.rawValue)
        } else {
            yoloMode = false
        }
        if !yoloSupported {
            yoloMode = false
        }
    }

    func setYoloMode(_ enabled: Bool) {
        guard yoloSupported else {
            yoloMode = false
            return
        }
        yoloMode = enabled
        daemon.setSetting("new_session_yolo_local", value: String(enabled))
    }

    func perform(_ action: Action) {
        switch action {
        case .selectPane(let choice):
            selectPaneChoice(choice)
        case .toggleLocalYolo:
            setYoloMode(!yoloMode)
        case .moveLocation(let direction):
            moveLocationSelection(direction)
        case .acceptLocation:
            if highlightedLocationPath != nil {
                acceptHighlightedLocation()
            }
            confirmLocation()
        case .moveDestination(let direction):
            moveDestinationSelection(direction)
        case .acceptDestination:
            acceptHighlightedDestination()
        case .toggleWorktreeStartBranch:
            toggleWorktreeStartBranch()
        }
    }

    func selectLocation(_ selectedPath: String) {
        updatePath(displayPath(selectedPath))
    }

    func applyAvailableLocations(
        recents: [RecentLocationSnapshot],
        directories: [DirectoryEntrySnapshot],
        homePath: String? = nil
    ) {
        if let homePath {
            self.homePath = homePath
            if path == initialPathAwaitingHomeAbbreviation {
                path = displayPath(path)
            }
            initialPathAwaitingHomeAbbreviation = nil
        }
        recentLocations = recents
        directoryEntries = directories
        updateHighlightedLocationAfterResultsChanged()
    }

    func applyBrowseDirectoryResult(_ result: BrowseDirectoryResultMessage) {
        browsedDirectory = result.directory.isEmpty ? nil : result.directory
        directoryEntries = result.entries
        homePath = result.homePath ?? homePath
        updateHighlightedLocationAfterResultsChanged()
    }

    func moveLocationSelection(_ direction: SelectionDirection) {
        let paths = selectableLocationPaths
        guard !paths.isEmpty else {
            highlightedLocationPath = nil
            return
        }
        let current = highlightedLocationPath.flatMap { paths.firstIndex(of: $0) }
        switch direction {
        case .down:
            highlightedLocationPath = paths[min((current ?? -1) + 1, paths.count - 1)]
        case .up:
            highlightedLocationPath = paths[max((current ?? paths.count) - 1, 0)]
        }
    }

    func acceptHighlightedLocation() {
        guard let highlightedLocationPath else { return }
        selectLocation(highlightedLocationPath)
    }

    func confirmLocation() {
        guard operation == nil else { return }
        let candidate = path.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !candidate.isEmpty else {
            error = "Enter a workspace location."
            return
        }
        operation = "Inspecting location..."
        error = nil
        Task {
            do {
                let inspection = try await daemon.inspectPath(candidate)
                guard inspection.exists == true, inspection.isDirectory == true,
                      let resolved = inspection.resolvedPath else {
                    throw DaemonCommandError("That location is not a directory.")
                }
                if let repoRoot = inspection.repoRoot {
                    applyRepositoryDestinations(try await daemon.repoInfo(repoRoot), selectedPath: resolved)
                    operation = nil
                } else {
                    try await submit(path: resolved)
                }
            } catch {
                operation = nil
                self.error = error.localizedDescription
            }
        }
    }

    func chooseDestination(_ selectedPath: String) {
        guard operation == nil else { return }
        path = selectedPath
        operation = "Opening..."
        error = nil
        Task {
            do {
                try await submit(path: selectedPath)
            } catch {
                operation = nil
                self.error = error.localizedDescription
            }
        }
    }

    func showCreateWorktree() {
        newBranch = ""
        worktreeStartFromDefault = false
        if let repoInfo {
            highlightedDestinationIndex = repoInfo.worktrees.count + 1
        }
        isCreatingWorktree = true
    }

    func applyRepositoryDestinations(_ info: RepoInfoSnapshot, selectedPath: String? = nil) {
        repoInfo = info
        stage = .destinations
        let initialIndex = destinationIndex(for: selectedPath ?? expandedDisplayPath(path), in: info) ?? 0
        selectedDestinationIndex = initialIndex
        highlightedDestinationIndex = initialIndex
        isCreatingWorktree = false
    }

    func toggleWorktreeStartBranch() {
        worktreeStartFromDefault.toggle()
    }

    func moveDestinationSelection(_ direction: SelectionDirection) {
        guard let repoInfo, !isCreatingWorktree else { return }
        let count = repoInfo.worktrees.count + 2
        let nextIndex: Int
        switch direction {
        case .down:
            nextIndex = min((highlightedDestinationIndex ?? -1) + 1, count - 1)
        case .up:
            nextIndex = max((highlightedDestinationIndex ?? count) - 1, 0)
        }
        highlightedDestinationIndex = nextIndex
        if nextIndex <= repoInfo.worktrees.count {
            selectedDestinationIndex = nextIndex
            path = destinationPath(at: nextIndex, in: repoInfo)
        }
    }

    func acceptHighlightedDestination() {
        guard let repoInfo, let index = highlightedDestinationIndex else { return }
        if index == 0 {
            chooseDestination(repoInfo.repo)
        } else if index <= repoInfo.worktrees.count {
            chooseDestination(repoInfo.worktrees[index - 1].path)
        } else {
            showCreateWorktree()
        }
    }

    func isHighlightedDestination(_ index: Int) -> Bool {
        highlightedDestinationIndex == index
    }

    func createWorktreeAndOpen() {
        guard let repoInfo, let startingFrom = worktreeStartingFrom else { return }
        let branch = newBranch.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !branch.isEmpty else {
            error = "Enter a branch name."
            return
        }
        operation = "Creating worktree..."
        error = nil
        Task {
            do {
                let createdPath = try await daemon.createWorktree(
                    mainRepo: repoInfo.repo,
                    branch: branch,
                    startingFrom: startingFrom
                )
                try await submit(path: createdPath)
            } catch {
                operation = nil
                self.error = error.localizedDescription
            }
        }
    }

    func quickSplit(_ direction: SplitDirection) {
        guard let workspace = daemon.selectedWorkspace else { return }
        Task {
            if let targetPaneID = workspace.layout?.activePaneID,
               workspace.layout?.panes.contains(where: { $0.paneID == targetPaneID }) == true {
                try? await daemon.splitPane(SplitPaneCommand(
                    workspaceID: workspace.id,
                    targetPaneID: targetPaneID,
                    direction: direction,
                    cwd: nil
                ))
            } else {
                try? await bootstrapFirstPane(in: workspace, path: workspace.directory, choice: .terminal)
            }
        }
    }

    func snapshot() -> [String: String] {
        var value = [
            "presented": String(isPresented),
            "stage": stageName,
            "path": path,
            "paneChoice": paneChoice.rawValue,
            "yoloMode": String(yoloMode),
            "operation": operation ?? "",
            "error": error ?? "",
        ]
        value["mode"] = modeName
        value["direction"] = direction?.rawValue ?? ""
        value["highlightedLocationPath"] = highlightedLocationPath ?? ""
        value["ghostCompletion"] = ghostCompletion ?? ""
        value["ghostCompletionSuffix"] = ghostCompletionSuffix ?? ""
        value["tabCompletion"] = tabCompletion ?? ""
        value["browsedDirectory"] = browsedDirectory ?? ""
        value["visibleLocationPaths"] = visibleLocations.map(\.path).joined(separator: "\n")
        value["visibleDirectoryPaths"] = visibleDirectoryEntries.map(\.path).joined(separator: "\n")
        value["highlightedDestinationIndex"] = highlightedDestinationIndex.map(String.init) ?? ""
        value["selectedDestinationIndex"] = selectedDestinationIndex.map(String.init) ?? ""
        value["isCreatingWorktree"] = String(isCreatingWorktree)
        value["newBranch"] = newBranch
        value["worktreeStartingFrom"] = worktreeStartingFrom ?? ""
        value["yoloSupported"] = String(yoloSupported)
        return value
    }

    private var modeName: String {
        switch mode {
        case .newWorkspace: return "new_workspace"
        case .addPane: return "add_pane"
        case nil: return ""
        }
    }

    private var stageName: String {
        switch stage {
        case .location: return "location"
        case .destinations: return "destinations"
        }
    }

    private func loadLocations() {
        guard daemon.state == .ready else { return }
        Task {
            do {
                let result = try await daemon.recentLocations()
                applyAvailableLocations(
                    recents: result.recentLocations,
                    directories: directoryEntries,
                    homePath: result.homePath
                )
                updatePath(path)
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    private func submit(path: String) async throws {
        guard let mode else { return }
        let label = URL(fileURLWithPath: path).lastPathComponent
        let runtimeID = UUID().uuidString
        switch mode {
        case .newWorkspace:
            let workspaceID = UUID().uuidString
            try await daemon.bootstrapWorkspace(BootstrapWorkspaceCommand(
                id: workspaceID,
                title: label,
                directory: path,
                initialSession: BootstrapWorkspaceInitialSessionCommand(
                    id: runtimeID,
                    cwd: path,
                    kind: paneChoice == .terminal ? "shell" : "agent",
                    agent: paneChoice.agent,
                    cols: 120,
                    rows: 40,
                    label: paneChoice.label,
                    yoloMode: yoloSupported ? yoloMode : nil
                )
            ))
            daemon.selectWorkspace(workspaceID)
        case .addPane(let workspaceID, let targetPaneID, let direction):
            guard let workspace = daemon.workspaces.first(where: { $0.id == workspaceID }) else {
                throw DaemonCommandError("That workspace is no longer available.")
            }
            guard let targetPaneID else {
                try await bootstrapFirstPane(in: workspace, path: path, choice: paneChoice)
                break
            }
            if paneChoice == .terminal {
                try await daemon.splitPane(SplitPaneCommand(
                    workspaceID: workspaceID,
                    targetPaneID: targetPaneID,
                    direction: direction,
                    cwd: path
                ))
            } else {
                try await daemon.spawnSession(SpawnSessionCommand(
                    id: runtimeID,
                    cwd: path,
                    workspaceID: workspaceID,
                    agent: paneChoice.agent,
                    cols: 120,
                    rows: 40,
                    label: paneChoice.label,
                    yoloMode: yoloMode,
                    targetPaneID: targetPaneID,
                    direction: direction
                ))
            }
        }
        operation = nil
        close()
    }

    private func bootstrapFirstPane(in workspace: WorkspaceSnapshot, path: String, choice: PaneChoice) async throws {
        let runtimeID = UUID().uuidString
        try await daemon.bootstrapWorkspace(BootstrapWorkspaceCommand(
            id: workspace.id,
            title: workspace.title,
            directory: workspace.directory,
            initialSession: BootstrapWorkspaceInitialSessionCommand(
                id: runtimeID,
                cwd: path,
                kind: choice == .terminal ? "shell" : "agent",
                agent: choice.agent,
                cols: 120,
                rows: 40,
                label: choice.label,
                yoloMode: choice != .terminal && yoloSupported ? yoloMode : nil
            )
        ))
        daemon.selectWorkspace(workspace.id)
    }

    private func preferredChoice() -> PaneChoice {
        let savedValue = daemon.settings["new_pane_choice"] ?? daemon.settings["new_session_agent"]
        guard let saved = savedValue,
              let choice = PaneChoice(rawValue: saved),
              isAvailable(choice) else {
            return PaneChoice.allCases.first(where: { $0 != .terminal && isAvailable($0) }) ?? .terminal
        }
        return choice
    }

    private func reset() {
        stage = .location
        yoloMode = false
        recentLocations = []
        directoryEntries = []
        browsedDirectory = nil
        repoInfo = nil
        highlightedLocationPath = nil
        highlightedDestinationIndex = nil
        selectedDestinationIndex = nil
        isCreatingWorktree = false
        newBranch = ""
        worktreeStartFromDefault = false
        operation = nil
        error = nil
        initialPathAwaitingHomeAbbreviation = nil
    }

    private var selectableLocationPaths: [String] {
        var seen = Set<String>()
        return (visibleLocations.map(\.path) + visibleDirectoryEntries.map(\.path)).filter { seen.insert($0).inserted }
    }

    private func updateHighlightedLocationAfterResultsChanged() {
        guard let highlightedLocationPath, !selectableLocationPaths.contains(highlightedLocationPath) else { return }
        self.highlightedLocationPath = nil
    }

    func displayPath(_ value: String) -> String {
        guard let homePath, !homePath.isEmpty else { return value }
        if value == homePath {
            return "~"
        }
        if value.hasPrefix(homePath + "/") {
            return "~" + value.dropFirst(homePath.count)
        }
        return value
    }

    private func expandedDisplayPath(_ value: String) -> String {
        guard let homePath, !homePath.isEmpty else { return value }
        if value == "~" {
            return homePath
        }
        if value.hasPrefix("~/") {
            return homePath + value.dropFirst()
        }
        return value
    }

    private func destinationIndex(for selectedPath: String, in info: RepoInfoSnapshot) -> Int? {
        if selectedPath == info.repo {
            return 0
        }
        return info.worktrees.firstIndex(where: { $0.path == selectedPath }).map { $0 + 1 }
    }

    private func destinationPath(at index: Int, in info: RepoInfoSnapshot) -> String {
        if index == 0 {
            return info.repo
        }
        return info.worktrees[index - 1].path
    }
}
