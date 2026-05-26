import Foundation
import XCTest
@testable import AttnNative

final class ProtocolModelsTests: XCTestCase {
    func testHelloIdentifiesSwiftNativeClientAndProtocolVersion() throws {
        let data = try JSONEncoder().encode(ClientHelloMessage())
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(object["cmd"] as? String, "client_hello")
        XCTAssertEqual(object["client_kind"] as? String, "swift-native")
        XCTAssertEqual(object["version"] as? String, "protocol-66")
        XCTAssertEqual(object["capabilities"] as? [String], [])
    }

    func testTerminalAttachRestoresDaemonOwnedScreenState() throws {
        let data = try JSONEncoder().encode(AttachSessionCommand(id: "runtime-1"))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(object["cmd"] as? String, "attach_session")
        XCTAssertEqual(object["id"] as? String, "runtime-1")
        XCTAssertEqual(object["attach_policy"] as? String, "relaunch_restore")
    }

    func testBootstrapWorkspaceEncodesShellFirstLaunch() throws {
        let command = BootstrapWorkspaceCommand(
            id: "workspace-1",
            title: "scratch",
            directory: "/tmp/scratch",
            initialSession: BootstrapWorkspaceInitialSessionCommand(
                id: "runtime-1",
                cwd: "/tmp/scratch",
                kind: "shell",
                agent: "shell",
                cols: 120,
                rows: 40,
                label: "Terminal",
                yoloMode: nil
            )
        )
        let data = try JSONEncoder().encode(command)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let initialSession = try XCTUnwrap(object["initial_session"] as? [String: Any])

        XCTAssertEqual(object["cmd"] as? String, "bootstrap_workspace")
        XCTAssertEqual(object["id"] as? String, "workspace-1")
        XCTAssertEqual(initialSession["kind"] as? String, "shell")
        XCTAssertEqual(initialSession["agent"] as? String, "shell")
    }

    func testDialogTerminalSplitCarriesEditedDirectoryAndDirection() throws {
        let data = try JSONEncoder().encode(SplitPaneCommand(
            workspaceID: "workspace-1",
            targetPaneID: "main",
            direction: .horizontal,
            cwd: "/tmp/edited"
        ))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(object["cmd"] as? String, "workspace_layout_split_pane")
        XCTAssertEqual(object["workspace_id"] as? String, "workspace-1")
        XCTAssertEqual(object["target_pane_id"] as? String, "main")
        XCTAssertEqual(object["direction"] as? String, "horizontal")
        XCTAssertEqual(object["cwd"] as? String, "/tmp/edited")
    }

    func testWorkspaceLayoutDecodesDaemonSplitTree() throws {
        let data = Data("""
            {
              "workspace_id": "workspace-1",
              "active_pane_id": "pane-shell",
              "layout_json": "{\\"type\\":\\"split\\",\\"direction\\":\\"horizontal\\",\\"ratio\\":0.375,\\"children\\":[{\\"type\\":\\"pane\\",\\"pane_id\\":\\"main\\"},{\\"type\\":\\"pane\\",\\"pane_id\\":\\"pane-shell\\"}]}",
              "panes": []
            }
            """.utf8)
        let layout = try JSONDecoder().decode(WorkspaceLayoutSnapshot.self, from: data)

        XCTAssertEqual(layout.rootNode, .split(direction: .horizontal, ratio: 0.375, children: [.pane("main"), .pane("pane-shell")]))
    }

    func testDirectoryBrowseDecodesTheDirectoryBeingFiltered() throws {
        let data = Data("""
            {
              "directory": "/Users/victora/src",
              "entries": [{"name": "attn", "path": "/Users/victora/src/attn"}],
              "home_path": "/Users/victora",
              "success": true
            }
            """.utf8)
        let result = try JSONDecoder().decode(BrowseDirectoryResultMessage.self, from: data)

        XCTAssertEqual(result.directory, "/Users/victora/src")
        XCTAssertEqual(result.entries.map(\.path), ["/Users/victora/src/attn"])
    }
}
