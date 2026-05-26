import AppKit
import SwiftUI
import Darwin

@main
struct AttnNativeApp: App {
    @NSApplicationDelegateAdaptor(AttnNativeAppDelegate.self) private var appDelegate

    var body: some Scene {
        WindowGroup {
            RootWindowView(
                daemon: appDelegate.controller.daemon,
                launcher: appDelegate.controller.launcher
            )
        }
        .windowStyle(.hiddenTitleBar)
        .defaultSize(width: 1240, height: 760)
        .commands {
            CommandGroup(replacing: .pasteboard) {
                Button("Copy") {
                    NSApp.sendAction(#selector(NSText.copy(_:)), to: nil, from: nil)
                }
                .keyboardShortcut("c", modifiers: [.command])

                Button("Paste") {
                    NSApp.sendAction(#selector(NSText.paste(_:)), to: nil, from: nil)
                }
                .keyboardShortcut("v", modifiers: [.command])

                Button("Select All") {
                    NSApp.sendAction(#selector(NSText.selectAll(_:)), to: nil, from: nil)
                }
                .keyboardShortcut("a", modifiers: [.command])
            }

            CommandMenu("Workspace") {
                Button("New Workspace...") {
                    appDelegate.controller.launcher.openNewWorkspace()
                }
                .keyboardShortcut("n", modifiers: [.command, .shift])

                Divider()

                Button("Add Pane Vertically...") {
                    appDelegate.controller.launcher.openAddPane(direction: .vertical)
                }
                .keyboardShortcut("n", modifiers: [.command])

                Button("Add Pane Horizontally...") {
                    appDelegate.controller.launcher.openAddPane(direction: .horizontal)
                }
                .keyboardShortcut("n", modifiers: [.command, .option])

                Divider()

                Button("Split Terminal Vertically") {
                    appDelegate.controller.launcher.quickSplit(.vertical)
                }
                .keyboardShortcut("d", modifiers: [.command])

                Button("Split Terminal Horizontally") {
                    appDelegate.controller.launcher.quickSplit(.horizontal)
                }
                .keyboardShortcut("d", modifiers: [.command, .shift])

                Divider()

                Button("Focus Pane Left") {
                    appDelegate.controller.navigate(.left)
                }
                .keyboardShortcut(.leftArrow, modifiers: [.command, .option])

                Button("Focus Pane Right") {
                    appDelegate.controller.navigate(.right)
                }
                .keyboardShortcut(.rightArrow, modifiers: [.command, .option])

                Button("Focus Pane Up") {
                    appDelegate.controller.navigate(.up)
                }
                .keyboardShortcut(.upArrow, modifiers: [.command, .option])

                Button("Focus Pane Down") {
                    appDelegate.controller.navigate(.down)
                }
                .keyboardShortcut(.downArrow, modifiers: [.command, .option])
            }
        }
    }
}

@MainActor
final class AttnNativeAppDelegate: NSObject, NSApplicationDelegate {
    let controller = NativeAppController()
    private var clientInstanceLease: ClientInstanceLease?
    private var launchGuardTimer: Timer?

    func applicationWillFinishLaunching(_ notification: Notification) {
        do {
            switch try ClientInstanceLease.acquire(profile: AutomationProfile.current()) {
            case .acquired(let lease):
                clientInstanceLease = lease
                monitorLaunchGuardIfNeeded()
                controller.start()
            case .occupied(let ownerProcessID):
                if let ownerProcessID,
                   let runningApplication = NSRunningApplication(processIdentifier: ownerProcessID) {
                    runningApplication.activate()
                }
                NSApplication.shared.terminate(nil)
            }
        } catch {
            NSLog("Unable to acquire native-client instance lease: \(error.localizedDescription)")
            NSApplication.shared.terminate(nil)
        }
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        guard clientInstanceLease != nil else { return }
        let profile = AutomationProfile.current()
        controller.recoverWindowPlacementIfNeeded(profile: profile)
        guard let processID = profile.restoreForegroundProcessID,
              let application = NSRunningApplication(processIdentifier: processID),
              application.processIdentifier != ProcessInfo.processInfo.processIdentifier else {
            return
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            application.activate()
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        controller.dismissLauncherForShutdown()
        return true
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        controller.dismissLauncherForShutdown()
        return .terminateNow
    }

    private func monitorLaunchGuardIfNeeded() {
        guard let processID = AppEnvironment.launchGuardProcessID() else { return }
        let timer = Timer(timeInterval: 0.25, repeats: true) { _ in
            if Darwin.kill(processID, 0) != 0, errno != EPERM {
                NSApplication.shared.terminate(nil)
            }
        }
        launchGuardTimer = timer
        RunLoop.main.add(timer, forMode: .common)
    }
}

@MainActor
final class NativeAppController: ObservableObject {
    let daemon = DaemonConnection()
    lazy var launcher = WorkspaceLauncherModel(daemon: daemon)

    private var automationServer: NativeAutomationServer?
    private var closeShortcutMonitor: Any?
    private var isClosingContent = false
    private var started = false

    func navigate(_ direction: WorkspaceNavigationDirection) {
        guard !launcher.isPresented else { return }
        daemon.navigate(direction)
    }

    func dismissLauncherForShutdown() {
        launcher.close()
    }

    func recoverWindowPlacementIfNeeded(profile: AutomationProfile, remainingAttempts: Int = 20) {
        guard !profile.backgroundWindow else { return }
        guard let window = NSApplication.shared.windows.first else {
            guard remainingAttempts > 0 else { return }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) { [weak self] in
                self?.recoverWindowPlacementIfNeeded(
                    profile: profile,
                    remainingAttempts: remainingAttempts - 1
                )
            }
            return
        }
        let visibleFrames = NSScreen.screens.map(\.visibleFrame)
        guard WindowPlacementPolicy.shouldRecover(
            windowFrame: window.frame,
            visibleScreenFrames: visibleFrames
        ) else { return }
        window.center()
    }

    func start() {
        guard !started else { return }
        started = true
        daemon.start()
        installCloseShortcutMonitor()

        let profile = AutomationProfile.current()
        guard profile.automationEnabled else { return }

        let actions = NativeAutomationActions(
            daemon: daemon,
            launcher: launcher,
            profile: profile,
            closeSelectedContent: { [weak self] in self?.closeSelectedContent() }
        )
        let server = NativeAutomationServer(profile: profile, actions: actions)
        automationServer = server
        server.start()
        actions.applyInitialWindowModeWhenAvailable()
    }

    deinit {
        if let closeShortcutMonitor {
            NSEvent.removeMonitor(closeShortcutMonitor)
        }
        automationServer?.stop()
    }

    private func installCloseShortcutMonitor() {
        guard closeShortcutMonitor == nil else { return }
        closeShortcutMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard event.modifierFlags.intersection(.deviceIndependentFlagsMask) == .command,
                  event.charactersIgnoringModifiers?.lowercased() == "w",
                  let self else {
                return event
            }
            if self.launcher.isPresented {
                self.launcher.cancel()
                return nil
            }
            guard WorkspaceClosePolicy.intent(for: self.daemon.selectedWorkspace) != .closeWindow else {
                return event
            }
            self.closeSelectedContent()
            return nil
        }
    }

    func closeSelectedContent() {
        guard !isClosingContent else { return }
        let intent = WorkspaceClosePolicy.intent(for: daemon.selectedWorkspace)
        isClosingContent = true
        Task {
            defer { isClosingContent = false }
            do {
                switch intent {
                case .closePane(let workspaceID, let paneID):
                    try await daemon.closePane(ClosePaneCommand(workspaceID: workspaceID, paneID: paneID))
                case .closeWorkspace(let workspaceID):
                    try await daemon.unregisterWorkspace(workspaceID)
                case .closeWindow:
                    break
                }
            } catch {
                // A failed close leaves the current pane visible; later UI can surface daemon errors.
            }
        }
    }
}
