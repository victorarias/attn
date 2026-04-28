// Post real Cmd+scroll-wheel events to the OS, targeting a specific
// screen position. Used by the canvas perf-spike diagnostic to drive
// the same on_scroll_wheel code path the user hits with their trackpad,
// so we can measure post-zoom steady-state without the user having to
// scroll by hand each iteration.
//
// Usage:
//   swift scroll-driver.swift <x> <y> <count> <delta_per_step> <step_us>
//
// All scrolls are Cmd-modified (zoom). Negative delta = zoom out
// (matches a two-finger swipe-down on a trackpad).
//
// Requires Accessibility permission for whatever process invokes
// `swift` (Terminal / Claude Code / etc.).

import CoreGraphics
import Foundation

let args = CommandLine.arguments
guard args.count >= 6,
      let x = Double(args[1]),
      let y = Double(args[2]),
      let count = Int(args[3]),
      let delta = Int32(args[4]),
      let stepUs = UInt32(args[5])
else {
    FileHandle.standardError.write("usage: scroll-driver.swift <x> <y> <count> <delta_per_step> <step_us> [--no-cmd]\n".data(using: .utf8)!)
    exit(2)
}
let useCmd = !args.contains("--no-cmd")
FileHandle.standardError.write("[scroll-driver] x=\(x) y=\(y) count=\(count) delta=\(delta) step_us=\(stepUs) cmd=\(useCmd)\n".data(using: .utf8)!)

// Move the cursor first — scroll events are dispatched to whichever
// window is under the cursor. CGWarpMouseCursorPosition is silent,
// doesn't trigger mouse-moved events to the user app.
CGWarpMouseCursorPosition(CGPoint(x: x, y: y))
usleep(20_000)

for _ in 0..<count {
    guard let event = CGEvent(scrollWheelEvent2Source: nil,
                              units: .pixel,
                              wheelCount: 1,
                              wheel1: delta,
                              wheel2: 0,
                              wheel3: 0)
    else { continue }
    if useCmd {
        event.flags = .maskCommand
    }
    event.post(tap: .cghidEventTap)
    usleep(stepUs)
}
FileHandle.standardError.write("[scroll-driver] done\n".data(using: .utf8)!)
