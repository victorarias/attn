import AppKit
import GhosttyKit
import QuartzCore
import SwiftUI

struct GhosttyTerminalRepresentable: NSViewRepresentable {
    let runtimeID: String
    let shouldClaimFocus: Bool
    @ObservedObject var daemon: DaemonConnection

    final class Coordinator {
        var claimedFocus = false
    }

    func makeCoordinator() -> Coordinator {
        Coordinator()
    }

    func makeNSView(context: Context) -> GhosttyTerminalHostView {
        let terminal: GhosttyTerminalView
        if let existing = daemon.terminalSurface(runtimeID: runtimeID) as? GhosttyTerminalView {
            terminal = existing
        } else {
            terminal = GhosttyTerminalView(runtimeID: runtimeID, daemon: daemon)
            daemon.register(surface: terminal)
        }
        return GhosttyTerminalHostView(terminal: terminal)
    }

    func updateNSView(_ nsView: GhosttyTerminalHostView, context: Context) {
        let terminal = nsView.terminal
        terminal.setRenderingFocus(shouldClaimFocus)
        if shouldClaimFocus, !context.coordinator.claimedFocus || !terminal.hasInputFocus {
            terminal.requestInputFocus()
            context.coordinator.claimedFocus = terminal.hasInputFocus
        } else if !shouldClaimFocus {
            terminal.cancelRequestedInputFocus()
            context.coordinator.claimedFocus = false
        }
    }

    static func dismantleNSView(_ nsView: GhosttyTerminalHostView, coordinator: Void) {
        nsView.detachTerminal()
        nsView.terminal.daemon.unregister(surface: nsView.terminal)
    }
}

@MainActor
final class GhosttyTerminalHostView: NSView {
    let terminal: GhosttyTerminalView
    private var isSuperseded = false

    init(terminal: GhosttyTerminalView) {
        self.terminal = terminal
        super.init(frame: .zero)
        attachTerminal()
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported")
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        attachTerminal()
    }

    override func layout() {
        super.layout()
        guard terminal.superview === self else { return }
        terminal.frame = bounds
    }

    func detachTerminal() {
        guard terminal.superview === self else { return }
        terminal.removeFromSuperview()
        terminal.unmount(from: self)
    }

    fileprivate func cedeTerminalOwnership() {
        isSuperseded = true
    }

    private func attachTerminal() {
        guard !isSuperseded else { return }
        guard terminal.superview !== self else {
            terminal.frame = bounds
            return
        }
        terminal.mount(in: self)
    }
}

@MainActor
final class GhosttyTerminalView: NSView, TerminalSurface {
    private static let selectionPasteboard = NSPasteboard(name: .init("com.attn.native.ghostty.selection"))

    let runtimeID: String
    unowned let daemon: DaemonConnection
    lazy var automationIdentity = String(describing: ObjectIdentifier(self))

    private var surface: ghostty_surface_t?
    private let runtime = GhosttyRuntime.shared
    private(set) var isFocusedForRendering = true
    private(set) var focusLossWrites = 0
    private var lastReportedCurrentDirectory: String?
    private var inputFocusRequested = false
    private weak var mountedHost: GhosttyTerminalHostView?
    var hasInputFocus: Bool { window?.firstResponder === self }

    var geometry: TerminalGeometry {
        guard let surface else { return TerminalGeometry(columns: 0, rows: 0) }
        let size = ghostty_surface_size(surface)
        return TerminalGeometry(columns: Int(size.columns), rows: Int(size.rows))
    }
    var mouseCaptured: Bool {
        guard let surface else { return false }
        return ghostty_surface_mouse_captured(surface)
    }
    var reportedCurrentDirectory: String? {
        readGhosttyReportedCurrentDirectory() ?? lastReportedCurrentDirectory
    }

    private func readGhosttyReportedCurrentDirectory() -> String? {
        guard let surface else { return nil }
        var result = ghostty_text_s()
        guard ghostty_surface_read_pwd(surface, &result), let text = result.text else { return nil }
        defer { ghostty_surface_free_text(surface, &result) }
        return String(decoding: Data(bytes: text, count: Int(result.text_len)), as: UTF8.self)
    }

    override var acceptsFirstResponder: Bool { true }
    override var isFlipped: Bool { true }

    init(runtimeID: String, daemon: DaemonConnection) {
        self.runtimeID = runtimeID
        self.daemon = daemon
        super.init(frame: NSRect(x: 0, y: 0, width: 800, height: 600))
        wantsLayer = true
        layerContentsRedrawPolicy = .onSetNeedsDisplay

        guard let app = runtime.app else { return }
        var configuration = ghostty_surface_config_new()
        configuration.platform_tag = GHOSTTY_PLATFORM_MACOS
        configuration.platform = ghostty_platform_u(
            macos: ghostty_platform_macos_s(nsview: Unmanaged.passUnretained(self).toOpaque())
        )
        configuration.userdata = Unmanaged.passUnretained(self).toOpaque()
        configuration.scale_factor = Double(NSScreen.main?.backingScaleFactor ?? 2)
        configuration.io_mode = GHOSTTY_SURFACE_IO_EXTERNAL
        configuration.io_userdata = Unmanaged.passUnretained(self).toOpaque()
        configuration.io_write = ghosttyExternalWrite
        configuration.io_resize = ghosttyExternalResize
        surface = ghostty_surface_new(app, &configuration)
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported")
    }

    deinit {
        if let surface {
            ghostty_surface_free(surface)
        }
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        updateSurfaceSize()
        if inputFocusRequested {
            focus()
        }
    }

    override func layout() {
        super.layout()
        updateSurfaceSize()
    }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        updateSurfaceSize()
    }

    override func viewDidChangeBackingProperties() {
        super.viewDidChangeBackingProperties()
        updateSurfaceSize()
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        trackingAreas.forEach { removeTrackingArea($0) }
        addTrackingArea(NSTrackingArea(
            rect: .zero,
            options: [.mouseEnteredAndExited, .mouseMoved, .inVisibleRect, .activeAlways],
            owner: self,
            userInfo: nil
        ))
    }

    override func draw(_ dirtyRect: NSRect) {
        guard let surface else { return }
        ghostty_surface_draw(surface)
    }

    override func becomeFirstResponder() -> Bool {
        let accepted = super.becomeFirstResponder()
        if accepted {
            setRenderingFocus(true)
        }
        return accepted
    }

    override func resignFirstResponder() -> Bool {
        let resigned = super.resignFirstResponder()
        if resigned {
            setRenderingFocus(false)
        }
        return resigned
    }

    override func mouseDown(with event: NSEvent) {
        daemon.focusPane(runtimeID: runtimeID)
        window?.makeFirstResponder(self)
        let point = convert(event.locationInWindow, from: nil)
        sendLeftMouseDown(at: point, modifiers: event.ghosttyModifiers)
    }

    override func mouseUp(with event: NSEvent) {
        sendLeftMouseUp(modifiers: event.ghosttyModifiers)
    }

    override func mouseMoved(with event: NSEvent) {
        let point = convert(event.locationInWindow, from: nil)
        sendPointerPosition(point, modifiers: event.ghosttyModifiers)
    }

    override func mouseEntered(with event: NSEvent) {
        mouseMoved(with: event)
    }

    override func mouseExited(with event: NSEvent) {
        guard NSEvent.pressedMouseButtons == 0 else { return }
        sendPointerPosition(CGPoint(x: -1, y: -1), modifiers: event.ghosttyModifiers)
    }

    override func mouseDragged(with event: NSEvent) {
        mouseMoved(with: event)
    }

    override func scrollWheel(with event: NSEvent) {
        guard let surface else { return }
        let translated = GhosttyScrollTranslation(
            deltaX: event.scrollingDeltaX,
            deltaY: event.scrollingDeltaY,
            precise: event.hasPreciseScrollingDeltas,
            momentumPhase: event.momentumPhase
        )
        ghostty_surface_mouse_scroll(surface, translated.x, translated.y, translated.mods)
    }

    override func keyDown(with event: NSEvent) {
        sendKey(event, action: event.isARepeat ? GHOSTTY_ACTION_REPEAT : GHOSTTY_ACTION_PRESS)
    }

    override func keyUp(with event: NSEvent) {
        sendKey(event, action: GHOSTTY_ACTION_RELEASE)
    }

    @objc func copy(_ sender: Any?) {
        copySelectionToClipboard()
    }

    @objc func paste(_ sender: Any?) {
        pasteFromClipboard()
    }

    @objc override func selectAll(_ sender: Any?) {
        performBindingAction("select_all")
    }

    func processOutput(_ data: Data) {
        guard let surface else { return }
        data.withUnsafeBytes { bytes in
            guard let baseAddress = bytes.baseAddress else { return }
            ghostty_surface_process_output(surface, baseAddress.assumingMemoryBound(to: CChar.self), UInt(bytes.count))
        }
        needsDisplay = true
    }

    func processReplay(_ data: Data) {
        guard let surface else { return }
        data.withUnsafeBytes { bytes in
            guard let baseAddress = bytes.baseAddress else { return }
            ghostty_surface_process_replay(surface, baseAddress.assumingMemoryBound(to: CChar.self), UInt(bytes.count))
        }
        needsDisplay = true
    }

    func typeText(_ text: String) {
        guard let surface else { return }
        text.withCString { pointer in
            ghostty_surface_text(surface, pointer, UInt(text.utf8.count))
        }
    }

    func pressEnter() {
        guard let surface else { return }
        var key = ghostty_input_key_s()
        key.action = GHOSTTY_ACTION_PRESS
        key.keycode = 36
        key.mods = GHOSTTY_MODS_NONE
        key.consumed_mods = GHOSTTY_MODS_NONE
        key.unshifted_codepoint = 0x0D
        key.composing = false
        "\r".withCString { pointer in
            key.text = pointer
            _ = ghostty_surface_key(surface, key)
        }
        key.action = GHOSTTY_ACTION_RELEASE
        key.text = nil
        _ = ghostty_surface_key(surface, key)
    }

    func copySelectionToClipboard() {
        performBindingAction("copy_to_clipboard")
    }

    func pasteFromClipboard() {
        performBindingAction("paste_from_clipboard")
    }

    func readVisibleText() -> String {
        guard let surface else { return "" }
        let selection = ghostty_selection_s(
            top_left: ghostty_point_s(
                tag: GHOSTTY_POINT_VIEWPORT,
                coord: GHOSTTY_POINT_COORD_TOP_LEFT,
                x: 0,
                y: 0
            ),
            bottom_right: ghostty_point_s(
                tag: GHOSTTY_POINT_VIEWPORT,
                coord: GHOSTTY_POINT_COORD_BOTTOM_RIGHT,
                x: 0,
                y: 0
            ),
            rectangle: false
        )
        var result = ghostty_text_s()
        guard ghostty_surface_read_text(surface, selection, &result), let text = result.text else {
            return ""
        }
        defer { ghostty_surface_free_text(surface, &result) }
        return String(decoding: Data(bytes: text, count: Int(result.text_len)), as: UTF8.self)
    }

    func readSelectionText() -> String? {
        guard let surface else { return nil }
        var result = ghostty_text_s()
        guard ghostty_surface_read_selection(surface, &result), let text = result.text else {
            return nil
        }
        defer { ghostty_surface_free_text(surface, &result) }
        return String(decoding: Data(bytes: text, count: Int(result.text_len)), as: UTF8.self)
    }

    func movePointer(toColumn column: Int, row: Int) {
        sendPointerPosition(pointForCell(column: column, row: row), modifiers: GHOSTTY_MODS_NONE)
    }

    func clickCell(column: Int, row: Int) {
        sendLeftMouseDown(at: pointForCell(column: column, row: row), modifiers: GHOSTTY_MODS_NONE)
        sendLeftMouseUp(modifiers: GHOSTTY_MODS_NONE)
    }

    func dragSelection(fromColumn startColumn: Int, row startRow: Int, toColumn endColumn: Int, row endRow: Int) {
        sendLeftMouseDown(at: pointForCell(column: startColumn, row: startRow), modifiers: GHOSTTY_MODS_NONE)
        sendPointerPosition(pointForCell(column: endColumn, row: endRow), modifiers: GHOSTTY_MODS_NONE)
        sendLeftMouseUp(modifiers: GHOSTTY_MODS_NONE)
    }

    func focus() {
        window?.makeFirstResponder(self)
    }

    func requestInputFocus() {
        inputFocusRequested = true
        focus()
    }

    func cancelRequestedInputFocus() {
        inputFocusRequested = false
    }

    func focusForHardwareInput() {
        window?.makeKey()
        focus()
    }

    func setRenderingFocus(_ focused: Bool) {
        isFocusedForRendering = focused
        if let surface {
            ghostty_surface_set_focus(surface, focused)
        }
    }

    func updateReportedCurrentDirectory(_ path: String) {
        guard path.hasPrefix("/") else { return }
        lastReportedCurrentDirectory = path
    }

    func readClipboard(
        location: ghostty_clipboard_e,
        state: UnsafeMutableRawPointer?
    ) -> Bool {
        guard let surface, let text = pasteboard(for: location)?.string(forType: .string) else {
            return false
        }
        completeClipboardRequest(surface: surface, text: text, state: state, confirmed: false)
        return true
    }

    func confirmClipboardRead(
        text: UnsafePointer<CChar>?,
        state: UnsafeMutableRawPointer?,
        request: ghostty_clipboard_request_e
    ) {
        guard let surface else { return }
        switch request {
        case GHOSTTY_CLIPBOARD_REQUEST_PASTE:
            // Protected pastes need a native confirmation UI before they are
            // allowed; until that exists, reject rather than auto-approve.
            completeClipboardRequest(surface: surface, text: "", state: state, confirmed: false)
        case GHOSTTY_CLIPBOARD_REQUEST_OSC_52_READ:
            completeClipboardRequest(surface: surface, text: "", state: state, confirmed: false)
        default:
            break
        }
    }

    func writeClipboard(
        location: ghostty_clipboard_e,
        content: UnsafePointer<ghostty_clipboard_content_s>?,
        count: Int,
        confirm: Bool
    ) {
        guard !confirm,
              let pasteboard = pasteboard(for: location),
              let content,
              count > 0 else {
            return
        }
        for index in 0..<count {
            let item = content[index]
            guard let mime = item.mime,
                  String(cString: mime) == "text/plain",
                  let data = item.data else {
                continue
            }
            pasteboard.clearContents()
            pasteboard.setString(String(cString: data), forType: .string)
            return
        }
    }

    fileprivate func mount(in host: GhosttyTerminalHostView) {
        if mountedHost !== host {
            mountedHost?.cedeTerminalOwnership()
            mountedHost = host
        }
        removeFromSuperview()
        host.addSubview(self)
        frame = host.bounds
    }

    fileprivate func unmount(from host: GhosttyTerminalHostView) {
        if mountedHost === host {
            mountedHost = nil
        }
    }

    fileprivate func forwardInput(_ data: Data) {
        if data.range(of: Data([0x1b, 0x5b, 0x4f])) != nil {
            focusLossWrites += 1
        }
        daemon.sendTerminalInput(runtimeID: runtimeID, data: String(decoding: data, as: UTF8.self))
    }

    fileprivate func forwardResize(columns: UInt16, rows: UInt16) {
        daemon.resizeTerminal(runtimeID: runtimeID, columns: Int(columns), rows: Int(rows))
    }

    private func updateSurfaceSize() {
        guard let surface, bounds.width > 0, bounds.height > 0 else { return }
        let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 2
        ghostty_surface_set_content_scale(surface, scale, scale)
        let backing = convertToBacking(bounds.size)
        ghostty_surface_set_size(surface, UInt32(max(backing.width, 1)), UInt32(max(backing.height, 1)))
        layer?.contentsScale = scale
    }

    private func pointForCell(column: Int, row: Int) -> CGPoint {
        guard let surface else { return .zero }
        let size = ghostty_surface_size(surface)
        let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 2
        let cellWidth = CGFloat(size.cell_width_px) / scale
        let cellHeight = CGFloat(size.cell_height_px) / scale
        return CGPoint(
            x: (CGFloat(max(column, 0)) + 0.25) * cellWidth,
            y: (CGFloat(max(row, 0)) + 0.5) * cellHeight
        )
    }

    private func sendPointerPosition(_ point: CGPoint, modifiers: ghostty_input_mods_e) {
        guard let surface else { return }
        ghostty_surface_mouse_pos(surface, point.x, point.y, modifiers)
    }

    private func sendLeftMouseDown(at point: CGPoint, modifiers: ghostty_input_mods_e) {
        guard let surface else { return }
        sendPointerPosition(point, modifiers: modifiers)
        _ = ghostty_surface_mouse_button(surface, GHOSTTY_MOUSE_PRESS, GHOSTTY_MOUSE_LEFT, modifiers)
    }

    private func sendLeftMouseUp(modifiers: ghostty_input_mods_e) {
        guard let surface else { return }
        _ = ghostty_surface_mouse_button(surface, GHOSTTY_MOUSE_RELEASE, GHOSTTY_MOUSE_LEFT, modifiers)
    }

    private func sendKey(_ event: NSEvent, action: ghostty_input_action_e) {
        guard let surface else { return }
        var key = ghostty_input_key_s()
        key.action = action
        key.keycode = UInt32(event.keyCode)
        key.mods = event.ghosttyModifiers
        key.consumed_mods = event.ghosttyConsumedModifiers
        key.unshifted_codepoint = event.characters(byApplyingModifiers: [])?.unicodeScalars.first?.value ?? 0
        key.composing = false
        guard action != GHOSTTY_ACTION_RELEASE, let text = event.ghosttyText, !text.isEmpty else {
            _ = ghostty_surface_key(surface, key)
            return
        }
        text.withCString { pointer in
            key.text = pointer
            _ = ghostty_surface_key(surface, key)
        }
    }

    private func performBindingAction(_ action: String) {
        guard let surface else { return }
        action.withCString { pointer in
            _ = ghostty_surface_binding_action(surface, pointer, UInt(action.utf8.count))
        }
    }

    private func pasteboard(for location: ghostty_clipboard_e) -> NSPasteboard? {
        switch location {
        case GHOSTTY_CLIPBOARD_STANDARD:
            return .general
        case GHOSTTY_CLIPBOARD_SELECTION:
            return Self.selectionPasteboard
        default:
            return nil
        }
    }

    private func completeClipboardRequest(
        surface: ghostty_surface_t,
        text: String,
        state: UnsafeMutableRawPointer?,
        confirmed: Bool
    ) {
        text.withCString { pointer in
            ghostty_surface_complete_clipboard_request(surface, pointer, state, confirmed)
        }
    }
}

struct GhosttyScrollTranslation {
    let x: Double
    let y: Double
    let mods: ghostty_input_scroll_mods_t

    init(deltaX: Double, deltaY: Double, precise: Bool, momentumPhase: NSEvent.Phase) {
        let multiplier = precise ? 2.0 : 1.0
        x = deltaX * multiplier
        y = deltaY * multiplier

        var rawMods: Int32 = precise ? 0b0000_0001 : 0
        rawMods |= Int32(Self.momentumValue(for: momentumPhase)) << 1
        mods = rawMods
    }

    private static func momentumValue(for phase: NSEvent.Phase) -> UInt8 {
        switch phase {
        case .began: return UInt8(GHOSTTY_MOUSE_MOMENTUM_BEGAN.rawValue)
        case .stationary: return UInt8(GHOSTTY_MOUSE_MOMENTUM_STATIONARY.rawValue)
        case .changed: return UInt8(GHOSTTY_MOUSE_MOMENTUM_CHANGED.rawValue)
        case .ended: return UInt8(GHOSTTY_MOUSE_MOMENTUM_ENDED.rawValue)
        case .cancelled: return UInt8(GHOSTTY_MOUSE_MOMENTUM_CANCELLED.rawValue)
        case .mayBegin: return UInt8(GHOSTTY_MOUSE_MOMENTUM_MAY_BEGIN.rawValue)
        default: return UInt8(GHOSTTY_MOUSE_MOMENTUM_NONE.rawValue)
        }
    }
}

private func ghosttyExternalWrite(
    _ userdata: UnsafeMutableRawPointer?,
    _ bytes: UnsafePointer<CChar>?,
    _ length: UInt
) {
    guard let userdata, let bytes, length > 0 else { return }
    let view = Unmanaged<GhosttyTerminalView>.fromOpaque(userdata).takeUnretainedValue()
    let data = Data(bytes: bytes, count: Int(length))
    DispatchQueue.main.async { [weak view] in
        view?.forwardInput(data)
    }
}

private func ghosttyExternalResize(
    _ userdata: UnsafeMutableRawPointer?,
    _ columns: UInt16,
    _ rows: UInt16,
    _ width: UInt32,
    _ height: UInt32
) {
    guard let userdata else { return }
    let view = Unmanaged<GhosttyTerminalView>.fromOpaque(userdata).takeUnretainedValue()
    DispatchQueue.main.async { [weak view] in
        view?.forwardResize(columns: columns, rows: rows)
    }
}

private extension NSEvent {
    var ghosttyModifiers: ghostty_input_mods_e {
        var value = GHOSTTY_MODS_NONE.rawValue
        if modifierFlags.contains(.shift) { value |= GHOSTTY_MODS_SHIFT.rawValue }
        if modifierFlags.contains(.control) { value |= GHOSTTY_MODS_CTRL.rawValue }
        if modifierFlags.contains(.option) { value |= GHOSTTY_MODS_ALT.rawValue }
        if modifierFlags.contains(.command) { value |= GHOSTTY_MODS_SUPER.rawValue }
        if modifierFlags.contains(.capsLock) { value |= GHOSTTY_MODS_CAPS.rawValue }
        return ghostty_input_mods_e(value)
    }

    var ghosttyConsumedModifiers: ghostty_input_mods_e {
        let translated = modifierFlags.subtracting([.control, .command])
        var value = GHOSTTY_MODS_NONE.rawValue
        if translated.contains(.shift) { value |= GHOSTTY_MODS_SHIFT.rawValue }
        if translated.contains(.option) { value |= GHOSTTY_MODS_ALT.rawValue }
        if translated.contains(.capsLock) { value |= GHOSTTY_MODS_CAPS.rawValue }
        return ghostty_input_mods_e(value)
    }

    var ghosttyText: String? {
        guard let characters else { return nil }
        if characters.unicodeScalars.count == 1, let scalar = characters.unicodeScalars.first {
            if scalar.value < 0x20 {
                return self.characters(byApplyingModifiers: modifierFlags.subtracting(.control))
            }
            if scalar.value >= 0xF700 && scalar.value <= 0xF8FF {
                return nil
            }
        }
        return characters
    }
}
