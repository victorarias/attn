import Foundation
import Network
import Security

final class NativeAutomationServer: @unchecked Sendable {
    private let profile: AutomationProfile
    private let actions: NativeAutomationActions
    private let queue = DispatchQueue(label: "com.attn.native.automation")
    private let token: String

    private var listener: NWListener?
    private var requestSequence: UInt64 = 0

    init(profile: AutomationProfile, actions: NativeAutomationActions) {
        self.profile = profile
        self.actions = actions
        self.token = NativeAutomationServer.makeToken()
    }

    func start() {
        guard listener == nil else { return }
        do {
            let listener = try NWListener(using: .tcp, on: .any)
            self.listener = listener
            listener.stateUpdateHandler = { [weak self, weak listener] state in
                guard let self else { return }
                switch state {
                case .ready:
                    guard let port = listener?.port else { return }
                    self.writeManifest(port: port.rawValue)
                case .failed(let error):
                    self.log("server failed: \(error)")
                    self.stop()
                default:
                    break
                }
            }
            listener.newConnectionHandler = { [weak self] connection in
                self?.serve(connection)
            }
            listener.start(queue: queue)
        } catch {
            log("failed to start server: \(error.localizedDescription)")
        }
    }

    func stop() {
        listener?.cancel()
        listener = nil
        try? FileManager.default.removeItem(at: profile.manifestURL)
    }

    deinit {
        stop()
    }

    private func serve(_ connection: NWConnection) {
        connection.start(queue: queue)
        receiveNextLine(on: connection, buffer: Data())
    }

    private func receiveNextLine(on connection: NWConnection, buffer: Data) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 1_048_576) { [weak self] data, _, isComplete, error in
            guard let self else { return }
            var accumulated = buffer
            if let data {
                accumulated.append(data)
            }

            while let newlineIndex = accumulated.firstIndex(of: 0x0a) {
                let line = Data(accumulated[..<newlineIndex])
                accumulated.removeSubrange(...newlineIndex)
                self.process(line: line, on: connection)
            }

            if error != nil || isComplete {
                connection.cancel()
            } else {
                self.receiveNextLine(on: connection, buffer: accumulated)
            }
        }
    }

    private func process(line: Data, on connection: NWConnection) {
        requestSequence += 1
        let sequence = requestSequence
        Task {
            let response = await AutomationProtocol.process(
                line: line,
                token: token,
                sequence: sequence
            ) { [actions] action, payload in
                await actions.dispatch(action: action, payload: payload)
            }
            do {
                var body = try JSONEncoder().encode(response)
                body.append(0x0a)
                connection.send(content: body, completion: .contentProcessed { _ in })
            } catch {
                self.log("failed to encode response: \(error.localizedDescription)")
                connection.cancel()
            }
        }
    }

    private func writeManifest(port: UInt16) {
        let manifest = AutomationManifest(
            enabled: true,
            port: port,
            token: token,
            pid: ProcessInfo.processInfo.processIdentifier,
            started_at: String(Int(Date().timeIntervalSince1970))
        )
        do {
            try FileManager.default.createDirectory(
                at: profile.manifestURL.deletingLastPathComponent(),
                withIntermediateDirectories: true
            )
            let encoder = JSONEncoder()
            encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
            var body = try encoder.encode(manifest)
            body.append(0x0a)
            try body.write(to: profile.manifestURL, options: .atomic)
            log("server start pid=\(manifest.pid) port=\(port) enabled=true")
        } catch {
            log("failed to write manifest: \(error.localizedDescription)")
        }
    }

    private func log(_ message: String) {
        let logURL = profile.manifestURL.deletingLastPathComponent()
            .appendingPathComponent("ui-automation-server.log")
        let line = "\(message)\n"
        try? FileManager.default.createDirectory(
            at: logURL.deletingLastPathComponent(),
            withIntermediateDirectories: true
        )
        if !FileManager.default.fileExists(atPath: logURL.path) {
            try? line.write(to: logURL, atomically: true, encoding: .utf8)
            return
        }
        guard let handle = try? FileHandle(forWritingTo: logURL) else { return }
        defer { try? handle.close() }
        _ = try? handle.seekToEnd()
        try? handle.write(contentsOf: Data(line.utf8))
    }

    private static func makeToken() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else {
            preconditionFailure("automation token randomness unavailable")
        }
        return bytes.map { String(format: "%02x", $0) }.joined()
    }
}
