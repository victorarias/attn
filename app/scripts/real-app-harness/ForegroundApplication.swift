import AppKit

guard let application = NSWorkspace.shared.frontmostApplication else {
    exit(1)
}

print("\(application.processIdentifier)\t\(application.bundleIdentifier ?? "")")
