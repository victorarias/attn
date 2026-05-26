import Foundation

enum ProtocolVersion {
    static let number = "66"
    static let clientVersion = "protocol-\(number)"
}

struct ClientHelloMessage: Encodable, Equatable {
    let cmd = "client_hello"
    let clientKind = "swift-native"
    let version = ProtocolVersion.clientVersion
    let capabilities: [String] = []

    enum CodingKeys: String, CodingKey {
        case cmd
        case clientKind = "client_kind"
        case version
        case capabilities
    }
}

struct ServerEventHeader: Decodable {
    let event: String
}

struct WorkspaceSnapshot: Decodable, Identifiable, Equatable {
    let id: String
    let title: String
    let directory: String
    let status: String
    var layout: WorkspaceLayoutSnapshot?
}

struct WorkspaceLayoutSnapshot: Decodable, Equatable {
    let workspaceID: String
    let activePaneID: String
    let layoutJSON: String
    let panes: [WorkspacePaneSnapshot]

    var rootNode: WorkspaceLayoutNode? {
        try? JSONDecoder().decode(WorkspaceLayoutNode.self, from: Data(layoutJSON.utf8))
    }

    enum CodingKeys: String, CodingKey {
        case workspaceID = "workspace_id"
        case activePaneID = "active_pane_id"
        case layoutJSON = "layout_json"
        case panes
    }
}

indirect enum WorkspaceLayoutNode: Decodable, Equatable {
    case pane(String)
    case split(direction: SplitDirection, ratio: Double, children: [WorkspaceLayoutNode])

    private enum CodingKeys: String, CodingKey {
        case type
        case paneID = "pane_id"
        case direction
        case ratio
        case children
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        switch try container.decode(String.self, forKey: .type) {
        case "pane":
            self = .pane(try container.decode(String.self, forKey: .paneID))
        case "split":
            self = .split(
                direction: try container.decode(SplitDirection.self, forKey: .direction),
                ratio: try container.decodeIfPresent(Double.self, forKey: .ratio) ?? 0.5,
                children: try container.decode([WorkspaceLayoutNode].self, forKey: .children)
            )
        default:
            throw DecodingError.dataCorruptedError(forKey: .type, in: container, debugDescription: "Unknown layout node type.")
        }
    }
}

struct WorkspacePaneSnapshot: Decodable, Identifiable, Equatable {
    let paneID: String
    let runtimeID: String?
    let sessionID: String?
    let kind: String
    let title: String

    var id: String { paneID }

    enum CodingKeys: String, CodingKey {
        case paneID = "pane_id"
        case runtimeID = "runtime_id"
        case sessionID = "session_id"
        case kind
        case title
    }
}

struct InitialStateMessage: Decodable {
    let workspaces: [WorkspaceSnapshot]?
    let settings: [String: String]?
}

struct WorkspaceEventMessage: Decodable {
    let workspace: WorkspaceSnapshot
}

struct WorkspaceLayoutEventMessage: Decodable {
    let workspaceLayout: WorkspaceLayoutSnapshot

    enum CodingKeys: String, CodingKey {
        case workspaceLayout = "workspace_layout"
    }
}

struct AttachResultMessage: Decodable {
    struct ReplaySegment: Decodable {
        let data: String
    }

    let id: String
    let success: Bool
    let scrollback: String?
    let replaySegments: [ReplaySegment]?
    let screenSnapshot: String?
    let lastSeq: Int?

    enum CodingKeys: String, CodingKey {
        case id
        case success
        case scrollback
        case replaySegments = "replay_segments"
        case screenSnapshot = "screen_snapshot"
        case lastSeq = "last_seq"
    }
}

struct PTYOutputMessage: Decodable {
    let id: String
    let data: String
    let seq: Int
}

struct AttachSessionCommand: Encodable {
    let cmd = "attach_session"
    let id: String
    let attachPolicy = "relaunch_restore"

    enum CodingKeys: String, CodingKey {
        case cmd
        case id
        case attachPolicy = "attach_policy"
    }
}

struct PTYInputCommand: Encodable {
    let cmd = "pty_input"
    let id: String
    let data: String
    let source = "swift-native-ghostty"
}

struct PTYResizeCommand: Encodable {
    let cmd = "pty_resize"
    let id: String
    let cols: Int
    let rows: Int
}

enum SplitDirection: String, Codable, Equatable {
    case horizontal
    case vertical
}

struct RecentLocationSnapshot: Decodable, Identifiable, Equatable {
    let path: String
    let label: String
    let lastSeen: String
    let useCount: Int

    var id: String { path }

    enum CodingKeys: String, CodingKey {
        case path
        case label
        case lastSeen = "last_seen"
        case useCount = "use_count"
    }
}

struct DirectoryEntrySnapshot: Decodable, Identifiable, Equatable {
    let name: String
    let path: String

    var id: String { path }
}

struct PathInspectionSnapshot: Decodable, Equatable {
    let inputPath: String
    let resolvedPath: String?
    let homePath: String?
    let exists: Bool?
    let isDirectory: Bool?
    let repoRoot: String?

    enum CodingKeys: String, CodingKey {
        case inputPath = "input_path"
        case resolvedPath = "resolved_path"
        case homePath = "home_path"
        case exists
        case isDirectory = "is_directory"
        case repoRoot = "repo_root"
    }
}

struct WorktreeSnapshot: Decodable, Identifiable, Equatable {
    let path: String
    let branch: String

    var id: String { path }
}

struct RepoInfoSnapshot: Decodable, Equatable {
    let repo: String
    let currentBranch: String
    let currentCommitHash: String
    let currentCommitTime: String
    let defaultBranch: String
    let worktrees: [WorktreeSnapshot]

    enum CodingKeys: String, CodingKey {
        case repo
        case currentBranch = "current_branch"
        case currentCommitHash = "current_commit_hash"
        case currentCommitTime = "current_commit_time"
        case defaultBranch = "default_branch"
        case worktrees
    }
}

struct RecentLocationsResultMessage: Decodable {
    let requestID: String?
    let recentLocations: [RecentLocationSnapshot]
    let homePath: String?
    let success: Bool
    let error: String?

    enum CodingKeys: String, CodingKey {
        case requestID = "request_id"
        case recentLocations = "recent_locations"
        case homePath = "home_path"
        case success
        case error
    }
}

struct BrowseDirectoryResultMessage: Decodable {
    let requestID: String?
    let directory: String
    let entries: [DirectoryEntrySnapshot]
    let homePath: String?
    let success: Bool
    let error: String?

    enum CodingKeys: String, CodingKey {
        case requestID = "request_id"
        case directory
        case entries
        case homePath = "home_path"
        case success
        case error
    }
}

struct InspectPathResultMessage: Decodable {
    let requestID: String?
    let inspection: PathInspectionSnapshot?
    let success: Bool
    let error: String?

    enum CodingKeys: String, CodingKey {
        case requestID = "request_id"
        case inspection
        case success
        case error
    }
}

struct GetRepoInfoResultMessage: Decodable {
    let info: RepoInfoSnapshot?
    let success: Bool
    let error: String?
}

struct BootstrapWorkspaceResultMessage: Decodable {
    let workspaceID: String
    let success: Bool
    let error: String?

    enum CodingKeys: String, CodingKey {
        case workspaceID = "workspace_id"
        case success
        case error
    }
}

struct SpawnResultMessage: Decodable {
    let id: String
    let success: Bool
    let error: String?
}

struct WorkspaceLayoutActionResultMessage: Decodable {
    let action: String
    let workspaceID: String
    let success: Bool
    let error: String?

    enum CodingKeys: String, CodingKey {
        case action
        case workspaceID = "workspace_id"
        case success
        case error
    }
}

struct SettingsUpdatedMessage: Decodable {
    let settings: [String: String]?
}

struct CreateWorktreeResultMessage: Decodable {
    let path: String?
    let success: Bool
    let error: String?
}

struct GetRecentLocationsCommand: Encodable {
    let cmd = "get_recent_locations"
    let limit = 20
    let requestID: String

    enum CodingKeys: String, CodingKey {
        case cmd
        case limit
        case requestID = "request_id"
    }
}

struct BrowseDirectoryCommand: Encodable {
    let cmd = "browse_directory"
    let inputPath: String
    let requestID: String

    enum CodingKeys: String, CodingKey {
        case cmd
        case inputPath = "input_path"
        case requestID = "request_id"
    }
}

struct InspectPathCommand: Encodable {
    let cmd = "inspect_path"
    let path: String
    let requestID: String

    enum CodingKeys: String, CodingKey {
        case cmd
        case path
        case requestID = "request_id"
    }
}

struct GetRepoInfoCommand: Encodable {
    let cmd = "get_repo_info"
    let repo: String
}

struct BootstrapWorkspaceInitialSessionCommand: Encodable {
    let id: String
    let cwd: String
    let kind: String
    let agent: String?
    let cols: Int
    let rows: Int
    let label: String
    let yoloMode: Bool?

    enum CodingKeys: String, CodingKey {
        case id
        case cwd
        case kind
        case agent
        case cols
        case rows
        case label
        case yoloMode = "yolo_mode"
    }
}

struct BootstrapWorkspaceCommand: Encodable {
    let cmd = "bootstrap_workspace"
    let id: String
    let title: String
    let directory: String
    let initialSession: BootstrapWorkspaceInitialSessionCommand

    enum CodingKeys: String, CodingKey {
        case cmd
        case id
        case title
        case directory
        case initialSession = "initial_session"
    }
}

struct SpawnSessionCommand: Encodable {
    let cmd = "spawn_session"
    let id: String
    let cwd: String
    let workspaceID: String
    let agent: String
    let cols: Int
    let rows: Int
    let label: String
    let yoloMode: Bool?
    let targetPaneID: String
    let direction: SplitDirection

    enum CodingKeys: String, CodingKey {
        case cmd
        case id
        case cwd
        case workspaceID = "workspace_id"
        case agent
        case cols
        case rows
        case label
        case yoloMode = "yolo_mode"
        case targetPaneID = "target_pane_id"
        case direction
    }
}

struct SplitPaneCommand: Encodable {
    let cmd = "workspace_layout_split_pane"
    let workspaceID: String
    let targetPaneID: String
    let direction: SplitDirection
    let cwd: String?

    enum CodingKeys: String, CodingKey {
        case cmd
        case workspaceID = "workspace_id"
        case targetPaneID = "target_pane_id"
        case direction
        case cwd
    }
}

struct ClosePaneCommand: Encodable {
    let cmd = "workspace_layout_close_pane"
    let workspaceID: String
    let paneID: String

    enum CodingKeys: String, CodingKey {
        case cmd
        case workspaceID = "workspace_id"
        case paneID = "pane_id"
    }
}

struct UnregisterWorkspaceCommand: Encodable {
    let cmd = "unregister_workspace"
    let id: String
}

struct FocusPaneCommand: Encodable {
    let cmd = "workspace_layout_focus_pane"
    let workspaceID: String
    let paneID: String

    enum CodingKeys: String, CodingKey {
        case cmd
        case workspaceID = "workspace_id"
        case paneID = "pane_id"
    }
}

struct CreateWorktreeCommand: Encodable {
    let cmd = "create_worktree"
    let mainRepo: String
    let branch: String
    let startingFrom: String

    enum CodingKeys: String, CodingKey {
        case cmd
        case mainRepo = "main_repo"
        case branch
        case startingFrom = "starting_from"
    }
}

struct SetSettingCommand: Encodable {
    let cmd = "set_setting"
    let key: String
    let value: String
}
