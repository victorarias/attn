import CoreGraphics
import Foundation

guard CommandLine.arguments.count == 2,
      let pid = Int32(CommandLine.arguments[1]) else {
    fputs("usage: NativeWindowID.swift <pid>\n", stderr)
    exit(2)
}

let options: CGWindowListOption = [.optionOnScreenOnly, .excludeDesktopElements]
guard let windows = CGWindowListCopyWindowInfo(options, kCGNullWindowID) as? [[String: Any]] else {
    fputs("failed to read window list\n", stderr)
    exit(1)
}

let matches = windows.compactMap { entry -> (CGWindowID, CGFloat)? in
    guard (entry[kCGWindowOwnerPID as String] as? Int32) == pid,
          (entry[kCGWindowLayer as String] as? Int) == 0,
          let id = entry[kCGWindowNumber as String] as? CGWindowID,
          let rawBounds = entry[kCGWindowBounds as String] as? NSDictionary,
          let bounds = CGRect(dictionaryRepresentation: rawBounds) else {
        return nil
    }
    return (id, bounds.width * bounds.height)
}.sorted { $0.1 > $1.1 }

guard let match = matches.first else {
    fputs("no onscreen layer-0 window for pid \(pid)\n", stderr)
    exit(1)
}

print(match.0)
