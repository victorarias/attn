import Foundation

struct TerminalGeometry: Equatable {
    let columns: Int
    let rows: Int
}

@MainActor
protocol TerminalSurface: AnyObject {
    var runtimeID: String { get }
    var automationIdentity: String { get }
    var geometry: TerminalGeometry { get }
    var isFocusedForRendering: Bool { get }
    var hasInputFocus: Bool { get }
    var focusLossWrites: Int { get }
    var mouseCaptured: Bool { get }
    var reportedCurrentDirectory: String? { get }
    func processOutput(_ data: Data)
    func processReplay(_ data: Data)
    func typeText(_ text: String)
    func pressEnter()
    func copySelectionToClipboard()
    func pasteFromClipboard()
    func readVisibleText() -> String
    func readSelectionText() -> String?
    func movePointer(toColumn column: Int, row: Int)
    func clickCell(column: Int, row: Int)
    func dragSelection(fromColumn startColumn: Int, row startRow: Int, toColumn endColumn: Int, row endRow: Int)
    func focus()
    func focusForHardwareInput()
}
