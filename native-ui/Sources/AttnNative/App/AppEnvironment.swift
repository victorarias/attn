import Foundation

enum AppEnvironment {
    static let daemonURLVariable = "ATTN_NATIVE_WS_URL"
    static let launchGuardPIDVariable = "ATTN_NATIVE_LAUNCH_GUARD_PID"
    static let defaultDaemonURL = URL(string: "ws://localhost:29849/ws")!

    static func daemonURL(environment: [String: String] = ProcessInfo.processInfo.environment) -> URL {
        guard let rawValue = environment[daemonURLVariable],
              let url = URL(string: rawValue) else {
            return defaultDaemonURL
        }
        return url
    }

    static func launchGuardProcessID(
        environment: [String: String] = ProcessInfo.processInfo.environment
    ) -> pid_t? {
        guard let rawValue = environment[launchGuardPIDVariable],
              let processID = pid_t(rawValue.trimmingCharacters(in: .whitespacesAndNewlines)),
              processID > 0 else {
            return nil
        }
        return processID
    }
}
