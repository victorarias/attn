import Foundation

struct AutomationProfile: Equatable {
    static let baseBundleIdentifier = "com.attn.native"

    let name: String?
    let automationEnabled: Bool
    let backgroundWindow: Bool
    let restoreForegroundProcessID: pid_t?
    let manifestURL: URL

    static func current(
        environment: [String: String] = ProcessInfo.processInfo.environment,
        bundleIdentifier: String? = Bundle.main.bundleIdentifier,
        homeDirectory: URL = FileManager.default.homeDirectoryForCurrentUser
    ) -> AutomationProfile {
        let name = normalizedProfile(
            runtimeValue: environment["ATTN_PROFILE"],
            bundleIdentifier: bundleIdentifier
        )
        let automationEnabled = decidesAutomation(
            value: environment["ATTN_AUTOMATION"],
            profile: name
        )
        let backgroundWindow = automationEnabled &&
            environment["ATTN_AUTOMATION_BACKGROUND"]?.trimmingCharacters(in: .whitespacesAndNewlines) == "1"
        let restoreForegroundProcessID = backgroundWindow
            ? environment["ATTN_AUTOMATION_RESTORE_FOREGROUND_PID"].flatMap { pid_t($0) }
            : nil
        let resolvedBundleIdentifier = name.map { "\(baseBundleIdentifier).\($0)" } ?? baseBundleIdentifier
        let manifestURL = homeDirectory
            .appendingPathComponent("Library/Application Support", isDirectory: true)
            .appendingPathComponent(resolvedBundleIdentifier, isDirectory: true)
            .appendingPathComponent("debug/ui-automation.json")

        return AutomationProfile(
            name: name,
            automationEnabled: automationEnabled,
            backgroundWindow: backgroundWindow,
            restoreForegroundProcessID: restoreForegroundProcessID,
            manifestURL: manifestURL
        )
    }

    static func normalizedProfile(runtimeValue: String?, bundleIdentifier: String?) -> String? {
        if let value = validProfile(runtimeValue) {
            return value
        }
        guard let identifier = bundleIdentifier,
              identifier.hasPrefix("\(baseBundleIdentifier).") else {
            return nil
        }
        return validProfile(String(identifier.dropFirst(baseBundleIdentifier.count + 1)))
    }

    static func decidesAutomation(value: String?, profile: String?) -> Bool {
        switch value?.trimmingCharacters(in: .whitespacesAndNewlines) {
        case "1":
            return true
        case "0":
            return false
        case "", nil:
            return profile == "dev"
        default:
            return false
        }
    }

    private static func validProfile(_ rawValue: String?) -> String? {
        guard let rawValue else { return nil }
        let value = rawValue.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !value.isEmpty, value != "default", value.count <= 16 else { return nil }
        guard let first = value.first, first.isASCIIAlphaNumeric else { return nil }
        guard value.allSatisfy({ $0.isASCIIAlphaNumeric || $0 == "-" }) else { return nil }
        return value
    }
}

private extension Character {
    var isASCIIAlphaNumeric: Bool {
        unicodeScalars.count == 1 && unicodeScalars.allSatisfy {
            ("a"..."z").contains(Character($0)) || ("0"..."9").contains(Character($0))
        }
    }
}
