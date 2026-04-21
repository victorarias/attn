import AppKit
import ApplicationServices
import Foundation

enum DriverError: Error, CustomStringConvertible {
    case usage(String)
    case appNotRunning(String)
    case accessibilityDenied
    case invalidArgument(String)
    case eventCreationFailed(String)

    var description: String {
        switch self {
        case let .usage(message):
            return message
        case let .appNotRunning(bundleId):
            return "App is not running for bundle id \(bundleId)"
        case .accessibilityDenied:
            return "Accessibility permission is required for the real app harness input driver."
        case let .invalidArgument(message):
            return message
        case let .eventCreationFailed(message):
            return message
        }
    }
}

struct Options {
    var bundleId = "com.attn.manager"
    var promptAccessibility = false
    var command: String?
    var text: String?
    var key: String?
    var keyCode: Int?
    var relativeX: Double?
    var relativeY: Double?
    var modifiers = [String]()
    var menuPath = [String]()
    var visiblePx: Int?
}

func parseOptions() throws -> Options {
    var options = Options()
    var index = 1
    let args = CommandLine.arguments

    while index < args.count {
        let arg = args[index]
        switch arg {
        case "activate", "activate_background", "frontmost", "windowid", "text", "key", "keycode", "click", "menu", "window_park":
            options.command = arg
        case "--visible-px":
            index += 1
            guard index < args.count, let value = Int(args[index]), value > 0 else {
                throw DriverError.invalidArgument("Missing or invalid value for --visible-px")
            }
            options.visiblePx = value
        case "--bundle-id":
            index += 1
            guard index < args.count else {
                throw DriverError.invalidArgument("Missing value for --bundle-id")
            }
            options.bundleId = args[index]
        case "--prompt-accessibility":
            options.promptAccessibility = true
        case "--text":
            index += 1
            guard index < args.count else {
                throw DriverError.invalidArgument("Missing value for --text")
            }
            options.text = args[index]
        case "--key":
            index += 1
            guard index < args.count else {
                throw DriverError.invalidArgument("Missing value for --key")
            }
            options.key = args[index]
        case "--key-code":
            index += 1
            guard index < args.count, let code = Int(args[index]) else {
                throw DriverError.invalidArgument("Missing or invalid value for --key-code")
            }
            options.keyCode = code
        case "--modifiers":
            index += 1
            guard index < args.count else {
                throw DriverError.invalidArgument("Missing value for --modifiers")
            }
            options.modifiers = args[index]
                .split(separator: ",")
                .map { $0.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() }
                .filter { !$0.isEmpty }
        case "--relative-x":
            index += 1
            guard index < args.count, let value = Double(args[index]) else {
                throw DriverError.invalidArgument("Missing or invalid value for --relative-x")
            }
            options.relativeX = value
        case "--relative-y":
            index += 1
            guard index < args.count, let value = Double(args[index]) else {
                throw DriverError.invalidArgument("Missing or invalid value for --relative-y")
            }
            options.relativeY = value
        case "--path":
            index += 1
            guard index < args.count else {
                throw DriverError.invalidArgument("Missing value for --path")
            }
            options.menuPath = args[index]
                .split(separator: ">")
                .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                .filter { !$0.isEmpty }
        case "--help", "-h":
            throw DriverError.usage("""
            Usage:
              InputDriver.swift activate [--bundle-id com.attn.manager]
              InputDriver.swift activate_background [--bundle-id ...]
              InputDriver.swift frontmost
              InputDriver.swift windowid [--bundle-id ...]
              InputDriver.swift text --text "hello" [--bundle-id ...] [--prompt-accessibility]
              InputDriver.swift key --key d [--modifiers command,option]
              InputDriver.swift keycode --key-code 36 [--modifiers command]
              InputDriver.swift click --relative-x 0.75 --relative-y 0.5
              InputDriver.swift menu --path "File>New Session" [--bundle-id ...]
              InputDriver.swift window_park --visible-px 200 [--bundle-id ...]
            """)
        default:
            throw DriverError.invalidArgument("Unknown argument: \(arg)")
        }
        index += 1
    }

    guard options.command != nil else {
        throw DriverError.usage("Missing command. Use activate, activate_background, frontmost, windowid, text, key, keycode, click, or menu.")
    }

    return options
}

func findRunningApp(bundleId: String) -> NSRunningApplication? {
    NSRunningApplication.runningApplications(withBundleIdentifier: bundleId).first
}

func runningPID(bundleId: String) throws -> pid_t {
    guard let app = findRunningApp(bundleId: bundleId) else {
        throw DriverError.appNotRunning(bundleId)
    }
    return app.processIdentifier
}

func activateApp(bundleId: String) throws {
    guard let app = findRunningApp(bundleId: bundleId) else {
        throw DriverError.appNotRunning(bundleId)
    }
    app.activate(options: [.activateIgnoringOtherApps])
    Thread.sleep(forTimeInterval: 0.2)
}

// Non-activating resolution: verify the app is running but do not change
// frontmost. Returns the process-scoped AX handle so callers can drive menus
// or window-chrome actions without an HID event tap.
func axApplication(bundleId: String) throws -> (pid: pid_t, element: AXUIElement) {
    let pid = try runningPID(bundleId: bundleId)
    return (pid, AXUIElementCreateApplication(pid))
}

func axCopyAttribute(_ element: AXUIElement, _ name: String) -> CFTypeRef? {
    var raw: CFTypeRef?
    let status = AXUIElementCopyAttributeValue(element, name as CFString, &raw)
    return status == .success ? raw : nil
}

func axChildren(_ element: AXUIElement) -> [AXUIElement] {
    guard let raw = axCopyAttribute(element, kAXChildrenAttribute as String) else {
        return []
    }
    return (raw as? [AXUIElement]) ?? []
}

func axTitle(_ element: AXUIElement) -> String? {
    axCopyAttribute(element, kAXTitleAttribute as String) as? String
}

func axRole(_ element: AXUIElement) -> String? {
    axCopyAttribute(element, kAXRoleAttribute as String) as? String
}

// Descend one level: find a child (direct or inside the owning AXMenu) whose
// title matches `segment`. AppKit menus expose a two-tier shape where menu
// items are wrapped in an AXMenu intermediate; flatten that for traversal.
func axFindNext(_ element: AXUIElement, title segment: String) -> AXUIElement? {
    for child in axChildren(element) {
        if axTitle(child) == segment {
            return child
        }
    }
    for child in axChildren(element) where axRole(child) == "AXMenu" {
        if let match = axFindNext(child, title: segment) {
            return match
        }
    }
    return nil
}

func axPressMenuItem(bundleId: String, path: [String]) throws {
    guard !path.isEmpty else {
        throw DriverError.invalidArgument("menu --path requires at least one segment")
    }
    let (_, appElement) = try axApplication(bundleId: bundleId)
    guard let rawMenuBar = axCopyAttribute(appElement, kAXMenuBarAttribute as String) else {
        throw DriverError.eventCreationFailed("App \(bundleId) exposes no AX menu bar")
    }
    var current = rawMenuBar as! AXUIElement
    for segment in path {
        guard let next = axFindNext(current, title: segment) else {
            throw DriverError.eventCreationFailed(
                "AX menu path segment \(segment.debugDescription) not found under \(path.joined(separator: ">"))"
            )
        }
        current = next
    }
    let pressStatus = AXUIElementPerformAction(current, kAXPressAction as CFString)
    guard pressStatus == .success else {
        throw DriverError.eventCreationFailed(
            "AXPressAction on menu item \(path.joined(separator: ">")) failed (status=\(pressStatus.rawValue))"
        )
    }
}

func frontmostBundleIdentifier() -> String {
    NSWorkspace.shared.frontmostApplication?.bundleIdentifier ?? ""
}

func ensureAccessibility(prompt: Bool) throws {
    let options = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: prompt] as CFDictionary
    guard AXIsProcessTrustedWithOptions(options) else {
        throw DriverError.accessibilityDenied
    }
}

func mainWindowBounds(bundleId: String) throws -> CGRect {
    let pid = try runningPID(bundleId: bundleId)
    let options: CGWindowListOption = [.optionOnScreenOnly, .excludeDesktopElements]
    guard let windows = CGWindowListCopyWindowInfo(options, kCGNullWindowID) as? [[String: Any]] else {
        throw DriverError.eventCreationFailed("Failed to read window list.")
    }

    let bounds = windows
        .filter { (($0[kCGWindowOwnerPID as String] as? pid_t) ?? -1) == pid }
        .filter { (($0[kCGWindowLayer as String] as? Int) ?? 1) == 0 }
        .compactMap { entry -> CGRect? in
            guard let rawBounds = entry[kCGWindowBounds as String] as? NSDictionary else {
                return nil
            }
            return CGRect(dictionaryRepresentation: rawBounds)
        }
        .sorted { ($0.width * $0.height) > ($1.width * $1.height) }

    guard let bounds = bounds.first else {
        throw DriverError.eventCreationFailed("Failed to find an onscreen window for \(bundleId)")
    }
    return bounds
}

// Return the CGWindowID of the largest layer-0 onscreen window owned by the
// bundle. AppleScript's `count of windows of application process ...` returns 0
// for Tauri/wry apps even while a visible window exists, so callers needing a
// reliable "window exists now" gate should use CGWindowList instead.
func mainWindowID(bundleId: String) throws -> CGWindowID {
    let pid = try runningPID(bundleId: bundleId)
    let options: CGWindowListOption = [.optionOnScreenOnly, .excludeDesktopElements]
    guard let windows = CGWindowListCopyWindowInfo(options, kCGNullWindowID) as? [[String: Any]] else {
        throw DriverError.eventCreationFailed("Failed to read window list.")
    }

    let matches = windows
        .filter { (($0[kCGWindowOwnerPID as String] as? pid_t) ?? -1) == pid }
        .filter { (($0[kCGWindowLayer as String] as? Int) ?? 1) == 0 }
        .compactMap { entry -> (CGWindowID, CGFloat)? in
            guard let number = entry[kCGWindowNumber as String] as? CGWindowID else {
                return nil
            }
            let rect: CGRect = (entry[kCGWindowBounds as String] as? NSDictionary)
                .flatMap { CGRect(dictionaryRepresentation: $0) } ?? .zero
            return (number, rect.width * rect.height)
        }
        .sorted { $0.1 > $1.1 }

    guard let first = matches.first else {
        throw DriverError.eventCreationFailed("No onscreen window for \(bundleId)")
    }
    return first.0
}

func modifierFlags(_ modifiers: [String]) -> CGEventFlags {
    var flags: CGEventFlags = []
    for modifier in modifiers {
        switch modifier {
        case "command", "cmd", "meta":
            flags.insert(.maskCommand)
        case "option", "alt":
            flags.insert(.maskAlternate)
        case "shift":
            flags.insert(.maskShift)
        case "control", "ctrl":
            flags.insert(.maskControl)
        default:
            continue
        }
    }
    return flags
}

func virtualKeyCode(for key: String) throws -> CGKeyCode {
    let normalized = key.lowercased()
    let mapping: [String: CGKeyCode] = [
        "a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9,
        "b": 11, "q": 12, "w": 13, "e": 14, "r": 15, "y": 16, "t": 17, "1": 18, "2": 19,
        "3": 20, "4": 21, "6": 22, "5": 23, "=": 24, "9": 25, "7": 26, "-": 27, "8": 28,
        "0": 29, "]": 30, "o": 31, "u": 32, "[": 33, "i": 34, "p": 35, "l": 37, "j": 38,
        "'": 39, "k": 40, ";": 41, "\\": 42, ",": 43, "/": 44, "n": 45, "m": 46, ".": 47,
        "`": 50
    ]

    guard let code = mapping[normalized] else {
        throw DriverError.invalidArgument("Unsupported key for --key: \(key)")
    }
    return code
}

func postKeyCode(_ keyCode: CGKeyCode, modifiers: [String]) throws {
    let flags = modifierFlags(modifiers)
    guard let keyDown = CGEvent(keyboardEventSource: nil, virtualKey: keyCode, keyDown: true),
          let keyUp = CGEvent(keyboardEventSource: nil, virtualKey: keyCode, keyDown: false) else {
        throw DriverError.eventCreationFailed("Failed to create keyboard events for keycode \(keyCode)")
    }
    keyDown.flags = flags
    keyUp.flags = flags
    keyDown.post(tap: .cghidEventTap)
    Thread.sleep(forTimeInterval: 0.02)
    keyUp.post(tap: .cghidEventTap)
}

func keystroke(for character: Character) throws -> (CGKeyCode, [String]) {
    switch character {
    case "a": return (0, [])
    case "b": return (11, [])
    case "c": return (8, [])
    case "d": return (2, [])
    case "e": return (14, [])
    case "f": return (3, [])
    case "g": return (5, [])
    case "h": return (4, [])
    case "i": return (34, [])
    case "j": return (38, [])
    case "k": return (40, [])
    case "l": return (37, [])
    case "m": return (46, [])
    case "n": return (45, [])
    case "o": return (31, [])
    case "p": return (35, [])
    case "q": return (12, [])
    case "r": return (15, [])
    case "s": return (1, [])
    case "t": return (17, [])
    case "u": return (32, [])
    case "v": return (9, [])
    case "w": return (13, [])
    case "x": return (7, [])
    case "y": return (16, [])
    case "z": return (6, [])
    case "A": return (0, ["shift"])
    case "B": return (11, ["shift"])
    case "C": return (8, ["shift"])
    case "D": return (2, ["shift"])
    case "E": return (14, ["shift"])
    case "F": return (3, ["shift"])
    case "G": return (5, ["shift"])
    case "H": return (4, ["shift"])
    case "I": return (34, ["shift"])
    case "J": return (38, ["shift"])
    case "K": return (40, ["shift"])
    case "L": return (37, ["shift"])
    case "M": return (46, ["shift"])
    case "N": return (45, ["shift"])
    case "O": return (31, ["shift"])
    case "P": return (35, ["shift"])
    case "Q": return (12, ["shift"])
    case "R": return (15, ["shift"])
    case "S": return (1, ["shift"])
    case "T": return (17, ["shift"])
    case "U": return (32, ["shift"])
    case "V": return (9, ["shift"])
    case "W": return (13, ["shift"])
    case "X": return (7, ["shift"])
    case "Y": return (16, ["shift"])
    case "Z": return (6, ["shift"])
    case "0": return (29, [])
    case "1": return (18, [])
    case "2": return (19, [])
    case "3": return (20, [])
    case "4": return (21, [])
    case "5": return (23, [])
    case "6": return (22, [])
    case "7": return (26, [])
    case "8": return (28, [])
    case "9": return (25, [])
    case " ": return (49, [])
    case "-": return (27, [])
    case "_": return (27, ["shift"])
    case "=": return (24, [])
    case "+": return (24, ["shift"])
    case "/": return (44, [])
    case "?": return (44, ["shift"])
    case ".": return (47, [])
    case ">": return (47, ["shift"])
    case ",": return (43, [])
    case "<": return (43, ["shift"])
    case ";": return (41, [])
    case ":": return (41, ["shift"])
    case "'": return (39, [])
    case "\"": return (39, ["shift"])
    case "[": return (33, [])
    case "{": return (33, ["shift"])
    case "]": return (30, [])
    case "}": return (30, ["shift"])
    case "\\": return (42, [])
    case "|": return (42, ["shift"])
    case "`": return (50, [])
    case "~": return (50, ["shift"])
    case "!": return (18, ["shift"])
    case "@": return (19, ["shift"])
    case "#": return (20, ["shift"])
    case "$": return (21, ["shift"])
    case "%": return (23, ["shift"])
    case "^": return (22, ["shift"])
    case "&": return (26, ["shift"])
    case "*": return (28, ["shift"])
    case "(": return (25, ["shift"])
    case ")": return (29, ["shift"])
    default:
        throw DriverError.invalidArgument("Unsupported text character for keycode typing: \(character)")
    }
}

func postText(_ text: String) throws {
    let pasteboard = NSPasteboard.general
    let previousString = pasteboard.string(forType: .string)
    let pasteRestoreDelay: TimeInterval = 0.3

    pasteboard.clearContents()
    guard pasteboard.setString(text, forType: .string) else {
        throw DriverError.eventCreationFailed("Failed to stage text on the system pasteboard.")
    }

    do {
        try postKeyCode(try virtualKeyCode(for: "v"), modifiers: ["command"])
        // AppKit paste dispatch is asynchronous; keep the staged contents alive
        // long enough for the target terminal to consume the paste payload.
        Thread.sleep(forTimeInterval: pasteRestoreDelay)
    } catch {
        if let previousString {
            pasteboard.clearContents()
            _ = pasteboard.setString(previousString, forType: .string)
        } else {
            pasteboard.clearContents()
        }
        throw error
    }

    if let previousString {
        pasteboard.clearContents()
        _ = pasteboard.setString(previousString, forType: .string)
    } else {
        pasteboard.clearContents()
    }
}

// Reposition the first AX window so only `visiblePx` pixels remain on-screen at
// the right edge of the main display; the rest extends off the right side. Used
// by the harness to keep attn "visible" (so WKWebView keeps ticking) while
// occupying a narrow strip instead of the full display.
func windowPark(bundleId: String, visiblePx: Int) throws {
    let (_, appElement) = try axApplication(bundleId: bundleId)
    guard let raw = axCopyAttribute(appElement, kAXWindowsAttribute as String),
          let windows = raw as? [AXUIElement],
          let window = windows.first
    else {
        throw DriverError.eventCreationFailed("No AX windows for \(bundleId)")
    }

    var size = CGSize(width: 0, height: 0)
    if let sizeRef = axCopyAttribute(window, kAXSizeAttribute as String) {
        let axSize = sizeRef as! AXValue
        AXValueGetValue(axSize, .cgSize, &size)
    }
    var curPos = CGPoint(x: 0, y: 0)
    if let posRef = axCopyAttribute(window, kAXPositionAttribute as String) {
        let axPos = posRef as! AXValue
        AXValueGetValue(axPos, .cgPoint, &curPos)
    }

    guard let screen = NSScreen.main else {
        throw DriverError.eventCreationFailed("No main screen")
    }
    let screenFrame = screen.frame

    // AX window positions are in top-left screen coordinates, same as
    // CGWindowList. NSScreen.frame.width/height give the raw display size
    // (independent of the bottom-left-origin NSScreen coordinate system).
    let newX = screenFrame.width - CGFloat(visiblePx)
    let newY = max(0, (screenFrame.height - size.height) / 2)
    var newPos = CGPoint(x: newX, y: newY)
    guard let posValue = AXValueCreate(.cgPoint, &newPos) else {
        throw DriverError.eventCreationFailed("Failed to create AXValue for position")
    }
    let status = AXUIElementSetAttributeValue(window, kAXPositionAttribute as CFString, posValue)
    guard status == .success else {
        throw DriverError.eventCreationFailed(
            "AXUIElementSetAttributeValue(position) failed: status=\(status.rawValue)"
        )
    }
    print("screen=\(Int(screenFrame.width))x\(Int(screenFrame.height)) win=\(Int(size.width))x\(Int(size.height)) from=\(Int(curPos.x)),\(Int(curPos.y)) to=\(Int(newX)),\(Int(newY))")
}

func clickWindow(bundleId: String, relativeX: Double, relativeY: Double) throws {
    let bounds = try mainWindowBounds(bundleId: bundleId)
    let clampedX = min(max(relativeX, 0), 1)
    let clampedY = min(max(relativeY, 0), 1)
    let point = CGPoint(
        x: bounds.origin.x + bounds.width * clampedX,
        y: bounds.origin.y + bounds.height * clampedY
    )

    guard let move = CGEvent(mouseEventSource: nil, mouseType: .mouseMoved, mouseCursorPosition: point, mouseButton: .left),
          let down = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left),
          let up = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: point, mouseButton: .left) else {
        throw DriverError.eventCreationFailed("Failed to create mouse events.")
    }

    move.post(tap: .cghidEventTap)
    Thread.sleep(forTimeInterval: 0.02)
    down.post(tap: .cghidEventTap)
    Thread.sleep(forTimeInterval: 0.02)
    up.post(tap: .cghidEventTap)
}

do {
    let options = try parseOptions()

    // HID-based commands must run against a frontmost app; AX-based and
    // observation-only commands must NOT activate (that is the whole point).
    switch options.command {
    case "activate", "text", "key", "keycode", "click":
        try activateApp(bundleId: options.bundleId)
    default:
        break
    }

    switch options.command {
    case "activate":
        break
    case "activate_background":
        // Resolve the app without changing frontmost. If the app is not
        // running, surface a clear error; otherwise this is a no-op.
        _ = try axApplication(bundleId: options.bundleId)
    case "frontmost":
        print(frontmostBundleIdentifier())
    case "windowid":
        let wid = try mainWindowID(bundleId: options.bundleId)
        print(wid)
    case "menu":
        try ensureAccessibility(prompt: options.promptAccessibility)
        try axPressMenuItem(bundleId: options.bundleId, path: options.menuPath)
    case "text":
        try ensureAccessibility(prompt: options.promptAccessibility)
        guard let text = options.text else {
            throw DriverError.invalidArgument("Missing --text value")
        }
        try postText(text)
    case "key":
        try ensureAccessibility(prompt: options.promptAccessibility)
        guard let key = options.key else {
            throw DriverError.invalidArgument("Missing --key value")
        }
        try postKeyCode(try virtualKeyCode(for: key), modifiers: options.modifiers)
    case "keycode":
        try ensureAccessibility(prompt: options.promptAccessibility)
        guard let keyCode = options.keyCode else {
            throw DriverError.invalidArgument("Missing --key-code value")
        }
        try postKeyCode(CGKeyCode(keyCode), modifiers: options.modifiers)
    case "click":
        try ensureAccessibility(prompt: options.promptAccessibility)
        guard let relativeX = options.relativeX, let relativeY = options.relativeY else {
            throw DriverError.invalidArgument("Missing --relative-x/--relative-y for click")
        }
        try clickWindow(bundleId: options.bundleId, relativeX: relativeX, relativeY: relativeY)
    case "window_park":
        try ensureAccessibility(prompt: options.promptAccessibility)
        guard let visiblePx = options.visiblePx else {
            throw DriverError.invalidArgument("Missing --visible-px for window_park")
        }
        try windowPark(bundleId: options.bundleId, visiblePx: visiblePx)
    default:
        throw DriverError.invalidArgument("Unsupported command")
    }
} catch let error as DriverError {
    fputs("[RealAppHarness] \(error.description)\n", stderr)
    exit(1)
} catch {
    fputs("[RealAppHarness] \(error.localizedDescription)\n", stderr)
    exit(1)
}
