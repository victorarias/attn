import AppKit
import GhosttyKit

/// Owns Ghostty's application runtime. The daemon still owns all PTYs; this
/// runtime exists only for Ghostty parsing, native input encoding and Metal rendering.
@MainActor
final class GhosttyRuntime {
    static let shared = GhosttyRuntime()

    private(set) var app: ghostty_app_t?

    private init() {
        guard ghostty_init(0, nil) == 0 else {
            fatalError("ghostty_init failed")
        }
        guard let configuration = ghostty_config_new() else {
            fatalError("ghostty_config_new failed")
        }
        ghostty_config_load_default_files(configuration)
        ghostty_config_load_recursive_files(configuration)
        ghostty_config_finalize(configuration)
        defer { ghostty_config_free(configuration) }

        var runtime = ghostty_runtime_config_s(
            userdata: Unmanaged.passUnretained(self).toOpaque(),
            supports_selection_clipboard: true,
            wakeup_cb: { userdata in
                guard let userdata else { return }
                let runtime = Unmanaged<GhosttyRuntime>.fromOpaque(userdata).takeUnretainedValue()
                DispatchQueue.main.async {
                    runtime.tick()
                }
            },
            action_cb: { _, target, action in
                guard action.tag == GHOSTTY_ACTION_PWD,
                      target.tag == GHOSTTY_TARGET_SURFACE,
                      let surface = target.target.surface,
                      let userdata = ghostty_surface_userdata(surface),
                      let pwd = action.action.pwd.pwd else {
                    return false
                }
                let path = String(cString: pwd)
                let terminal = Unmanaged<GhosttyTerminalView>.fromOpaque(userdata).takeUnretainedValue()
                DispatchQueue.main.async { [weak terminal] in
                    terminal?.updateReportedCurrentDirectory(path)
                }
                return true
            },
            read_clipboard_cb: { userdata, location, state in
                guard let userdata else { return false }
                let terminal = Unmanaged<GhosttyTerminalView>.fromOpaque(userdata).takeUnretainedValue()
                return MainActor.assumeIsolated {
                    terminal.readClipboard(location: location, state: state)
                }
            },
            confirm_read_clipboard_cb: { userdata, text, state, request in
                guard let userdata else { return }
                let terminal = Unmanaged<GhosttyTerminalView>.fromOpaque(userdata).takeUnretainedValue()
                MainActor.assumeIsolated {
                    terminal.confirmClipboardRead(text: text, state: state, request: request)
                }
            },
            write_clipboard_cb: { userdata, location, content, count, confirm in
                guard let userdata else { return }
                let terminal = Unmanaged<GhosttyTerminalView>.fromOpaque(userdata).takeUnretainedValue()
                MainActor.assumeIsolated {
                    terminal.writeClipboard(
                        location: location,
                        content: content,
                        count: count,
                        confirm: confirm
                    )
                }
            },
            close_surface_cb: { _, _ in }
        )
        guard let app = ghostty_app_new(&runtime, configuration) else {
            fatalError("ghostty_app_new failed")
        }
        self.app = app
        ghostty_app_set_focus(app, NSApplication.shared.isActive)

        NotificationCenter.default.addObserver(
            forName: NSApplication.didBecomeActiveNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor in
                guard let app = self?.app else { return }
                ghostty_app_set_focus(app, true)
            }
        }
        NotificationCenter.default.addObserver(
            forName: NSApplication.didResignActiveNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor in
                guard let app = self?.app else { return }
                ghostty_app_set_focus(app, false)
            }
        }
    }

    deinit {
        NotificationCenter.default.removeObserver(self)
        if let app {
            ghostty_app_free(app)
        }
    }

    func tick() {
        guard let app else { return }
        ghostty_app_tick(app)
    }
}
