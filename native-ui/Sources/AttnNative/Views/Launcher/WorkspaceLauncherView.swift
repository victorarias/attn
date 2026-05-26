import AppKit
import SwiftUI

struct WorkspaceLauncherView: View {
    @ObservedObject var model: WorkspaceLauncherModel
    @FocusState private var pathFocused: Bool
    @FocusState private var branchFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            header
            switch model.stage {
            case .location:
                locationStage
            case .destinations:
                destinationsStage
            }
            footer
        }
        .fontDesign(.monospaced)
        .frame(width: 716, height: 416)
        .background {
            ZStack {
                surface
                LinearGradient(
                    colors: [accent.opacity(0.045), .clear, Color.black.opacity(0.13)],
                    startPoint: .topLeading,
                    endPoint: .bottomTrailing
                )
            }
        }
        .overlay {
            RoundedRectangle(cornerRadius: 18)
                .strokeBorder(Color.white.opacity(0.13), lineWidth: 1)
                .overlay(alignment: .top) {
                    RoundedRectangle(cornerRadius: 18)
                        .strokeBorder(accent.opacity(0.13), lineWidth: 1)
                        .mask {
                            LinearGradient(
                                colors: [.white, .clear],
                                startPoint: .top,
                                endPoint: .center
                            )
                        }
                }
        }
        .clipShape(RoundedRectangle(cornerRadius: 18))
        .shadow(color: Color.black.opacity(0.5), radius: 30, y: 16)
        .preferredColorScheme(.dark)
        .onAppear {
            if model.stage == .location {
                focusPathInputAtEnd()
            }
        }
        .onChange(of: model.stage) { _, stage in
            if stage == .location {
                focusPathInputAtEnd()
            } else {
                pathFocused = false
            }
        }
        .onChange(of: model.isCreatingWorktree) { _, isCreating in
            if isCreating {
                requestBranchFocus()
            } else {
                branchFocused = false
            }
        }
        .onExitCommand {
            model.cancel()
        }
    }

    private var header: some View {
        HStack(spacing: 14) {
            VStack(alignment: .leading, spacing: 3) {
                Text(model.title.uppercased())
                    .font(.system(size: 10, weight: .bold, design: .monospaced))
                    .tracking(1.7)
                    .foregroundStyle(.white.opacity(0.86))
                Text(headerContext)
                    .font(.system(size: 8, weight: .medium, design: .monospaced))
                    .tracking(1.25)
                    .foregroundStyle(accent.opacity(0.75))
            }
            .frame(width: 116, alignment: .leading)

            Spacer(minLength: 8)

            HStack(spacing: 7) {
                sectionLabel("TYPE")
                HStack(spacing: 3) {
                    ForEach(WorkspaceLauncherModel.PaneChoice.allCases) { choice in
                        Button {
                            model.perform(.selectPane(choice))
                        } label: {
                            HStack(spacing: 4) {
                                Text(choice.label)
                                Text(choiceShortcutLabel(choice))
                                    .font(.system(size: 7, weight: .bold, design: .monospaced))
                                    .padding(.horizontal, 3)
                                    .frame(height: 13)
                                    .background {
                                        RoundedRectangle(cornerRadius: 4)
                                            .fill(model.paneChoice == choice ? Color.black.opacity(0.12) : Color.white.opacity(0.045))
                                    }
                                    .overlay {
                                        RoundedRectangle(cornerRadius: 4)
                                            .stroke(model.paneChoice == choice ? Color.black.opacity(0.18) : Color.white.opacity(0.09), lineWidth: 1)
                                    }
                            }
                                .foregroundStyle(model.paneChoice == choice ? Color.black : Color.white.opacity(0.54))
                                .font(.system(size: 10, weight: model.paneChoice == choice ? .semibold : .regular, design: .monospaced))
                                .padding(.vertical, 5)
                                .padding(.horizontal, 6)
                                .background {
                                    Capsule().fill(model.paneChoice == choice ? accent : .clear)
                                }
                        }
                        .buttonStyle(.plain)
                        .disabled(!model.isAvailable(choice))
                        .opacity(model.isAvailable(choice) ? 1 : 0.38)
                        .keyboardShortcut(choiceShortcut(choice), modifiers: [.option])
                    }
                }
                .padding(3)
                .background(Capsule().fill(Color.black.opacity(0.3)))
                .overlay { Capsule().stroke(Color.white.opacity(0.1), lineWidth: 1) }
            }

            Button {
                model.perform(.toggleLocalYolo)
            } label: {
                HStack(spacing: 7) {
                    Circle()
                        .fill(accent.opacity(model.yoloMode ? 1 : 0.7))
                        .frame(width: 6, height: 6)
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Text("Local")
                            if model.yoloMode && model.yoloSupported {
                                Text("YOLO")
                                    .font(.system(size: 7, weight: .bold, design: .monospaced))
                                    .foregroundStyle(accent)
                            }
                        }
                        .font(.system(size: 10, weight: .medium, design: .monospaced))
                        .foregroundStyle(.white.opacity(0.88))
                        Text(model.yoloSupported ? "⌥Q toggle" : "this machine")
                            .font(.system(size: 8, design: .monospaced))
                            .foregroundStyle(Color.white.opacity(0.4))
                    }
                }
                .padding(.horizontal, 10)
                .frame(height: 34)
                .background(RoundedRectangle(cornerRadius: 9).fill(accent.opacity(model.yoloMode ? 0.16 : 0.07)))
                .overlay { RoundedRectangle(cornerRadius: 9).stroke(accent.opacity(model.yoloMode ? 0.72 : 0.3), lineWidth: 1) }
            }
            .buttonStyle(.plain)
            .disabled(!model.yoloSupported)
            .keyboardShortcut("q", modifiers: [.option])
        }
        .padding(.horizontal, 16)
        .frame(height: 54)
        .background(Color.black.opacity(0.14))
        .overlay(alignment: .bottom) { Divider().overlay(divider) }
    }

    private var locationStage: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(model.locationHeading)
                    .font(.system(size: 10, weight: .bold, design: .monospaced))
                    .tracking(2)
                    .foregroundStyle(accent)
                Spacer()
                if let direction = model.direction {
                    Text("\(direction.rawValue.uppercased()) SPLIT")
                        .font(.system(size: 10, weight: .medium, design: .monospaced))
                        .foregroundStyle(Color.white.opacity(0.4))
                } else {
                    Text("select a location to open")
                        .foregroundStyle(Color.white.opacity(0.36))
                }
            }
            .padding(.horizontal, 16)
            .padding(.top, 9)
            .padding(.bottom, 6)

            ZStack(alignment: .leading) {
                TextField("", text: Binding(
                    get: { model.path },
                    set: { model.updatePath($0) }
                ))
                .textFieldStyle(.plain)
                .font(.system(size: 15, weight: .regular, design: .monospaced))
                .foregroundStyle(Color.white.opacity(0.94))
                .padding(.horizontal, 11)
                .focused($pathFocused)
                .onAppear {
                    focusPathInputAtEnd()
                }
                .onKeyPress(.return) {
                    model.perform(.acceptLocation)
                    return .handled
                }
                .onKeyPress(.tab) {
                    model.applyCompletion()
                    return .handled
                }
                .onKeyPress(.downArrow) {
                    model.perform(.moveLocation(.down))
                    return .handled
                }
                .onKeyPress(.upArrow) {
                    model.perform(.moveLocation(.up))
                    return .handled
                }
                if let suffix = model.ghostCompletionSuffix {
                    HStack(spacing: 0) {
                        Text(model.path)
                            .foregroundStyle(.clear)
                        Text(suffix)
                            .foregroundStyle(Color.white.opacity(0.27))
                    }
                    .font(.system(size: 15, weight: .regular, design: .monospaced))
                    .padding(.horizontal, 11)
                    .allowsHitTesting(false)
                    .accessibilityHidden(true)
                }
            }
            .frame(height: 36)
            .background(RoundedRectangle(cornerRadius: 8).fill(Color.black.opacity(0.3)))
            .overlay {
                RoundedRectangle(cornerRadius: 8).stroke(accent.opacity(0.9), lineWidth: 1)
            }
            .shadow(color: accent.opacity(0.12), radius: 8, y: 0)
            .padding(.horizontal, 16)

            if let browsedDirectory = model.browsedDirectory {
                Text("Browsing:  \(model.displayPath(browsedDirectory))")
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundStyle(Color.white.opacity(0.36))
                    .padding(.horizontal, 18)
                    .padding(.top, 4)
            }

            Divider().overlay(divider).padding(.top, 6)

            ScrollView {
                VStack(alignment: .leading, spacing: 4) {
                    if !model.visibleLocations.isEmpty {
                        sectionLabel("RECENT").padding(.bottom, 2)
                        ForEach(model.visibleLocations) { location in
                            locationRow(name: location.label, path: model.displayPath(location.path), icon: "clock", highlighted: model.highlightedLocationPath == location.path) {
                                model.selectLocation(location.path)
                                model.confirmLocation()
                            }
                        }
                    }
                    if !model.visibleDirectoryEntries.isEmpty {
                        sectionLabel("DIRECTORIES").padding(.top, 6).padding(.bottom, 2)
                        ForEach(model.visibleDirectoryEntries) { directory in
                            locationRow(name: directory.name, path: model.displayPath(directory.path), icon: "folder", highlighted: model.highlightedLocationPath == directory.path) {
                                model.selectLocation(directory.path)
                                model.confirmLocation()
                            }
                        }
                    }
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 9)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    private var destinationsStage: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                sectionLabel("DESTINATIONS")
                Spacer()
                if let startingFrom = model.worktreeStartingFrom {
                    Text("SOURCE  \(startingFrom)")
                        .font(.system(size: 8, weight: .medium, design: .monospaced))
                        .tracking(0.9)
                        .foregroundStyle(Color.white.opacity(0.3))
                }
            }
            .padding(.horizontal, 16)
            .padding(.top, 9)
            .padding(.bottom, 6)
            Divider().overlay(divider).padding(.horizontal, 16)
            ScrollViewReader { proxy in
                ScrollView {
                    VStack(spacing: 5) {
                        if let repo = model.repoInfo {
                            destinationRow(
                                title: repo.currentBranch,
                                detail: "\(repo.repo)  •  \(String(repo.currentCommitHash.prefix(7)))",
                                symbol: "circle.fill",
                                color: accent,
                                highlighted: model.isHighlightedDestination(0)
                            ) {
                                model.chooseDestination(repo.repo)
                            }
                            .id(destinationScrollID(0))
                            ForEach(Array(repo.worktrees.enumerated()), id: \.element.id) { index, worktree in
                                destinationRow(title: worktree.branch, detail: worktree.path, symbol: "scope", color: .purple, highlighted: model.isHighlightedDestination(index + 1)) {
                                    model.chooseDestination(worktree.path)
                                }
                                .id(destinationScrollID(index + 1))
                            }
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 9)
                }
                .overlay(alignment: .bottom) {
                    if destinationListOverflows {
                        LinearGradient(
                            colors: [
                                surface.opacity(0),
                                surface.opacity(0.8),
                                surface,
                            ],
                            startPoint: .top,
                            endPoint: .bottom
                        )
                        .frame(height: 46)
                        .overlay(alignment: .bottom) {
                            Text("MORE BELOW  ↓")
                                .font(.system(size: 7, weight: .bold, design: .monospaced))
                                .tracking(1)
                                .foregroundStyle(Color.white.opacity(0.26))
                                .padding(.bottom, 5)
                        }
                        .allowsHitTesting(false)
                    }
                }
                .onChange(of: model.highlightedDestinationIndex) { _, index in
                    guard let index,
                          let repo = model.repoInfo,
                          index <= repo.worktrees.count else { return }
                    withAnimation(.easeOut(duration: 0.12)) {
                        proxy.scrollTo(destinationScrollID(index), anchor: .center)
                    }
                }
            }
            if let repo = model.repoInfo {
                Divider().overlay(divider).padding(.horizontal, 16)
                if model.isCreatingWorktree {
                    createWorktreeForm
                        .padding(.horizontal, 16)
                        .padding(.top, 9)
                        .padding(.bottom, 10)
                } else {
                    destinationRow(
                        title: "Create worktree...",
                        detail: "Create a new worktree and open it immediately",
                        symbol: "plus",
                        color: .blue,
                        highlighted: model.isHighlightedDestination(repo.worktrees.count + 1)
                    ) {
                        model.showCreateWorktree()
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 9)
                }
            }
        }
        .background {
            if !model.isCreatingWorktree {
                WorkspaceLauncherDestinationKeyCapture { command in
                    switch command {
                    case .up:
                        model.perform(.moveDestination(.up))
                    case .down:
                        model.perform(.moveDestination(.down))
                    case .accept:
                        model.perform(.acceptDestination)
                    case .cancel:
                        model.cancel()
                    }
                }
                .frame(width: 0, height: 0)
            }
        }
    }

    private func destinationScrollID(_ index: Int) -> String {
        "destination-\(index)"
    }

    private var destinationListOverflows: Bool {
        guard let repo = model.repoInfo else { return false }
        return repo.worktrees.count > 3
    }

    private var createWorktreeForm: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                sectionLabel("CREATE WORKTREE")
                    .foregroundStyle(accent.opacity(0.86))
                Spacer()
                Text("NEW CHECKOUT")
                    .font(.system(size: 8, weight: .bold, design: .monospaced))
                    .tracking(1)
                    .foregroundStyle(Color.white.opacity(0.27))
            }
            HStack(spacing: 7) {
                Image(systemName: "arrow.triangle.branch")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(accent)
                    .frame(width: 30, height: 32)
                    .background(RoundedRectangle(cornerRadius: 7).fill(accent.opacity(0.12)))
                TextField("branch-name", text: $model.newBranch)
                    .textFieldStyle(.plain)
                    .font(.system(size: 12, weight: .medium, design: .monospaced))
                    .padding(.horizontal, 9)
                    .frame(height: 32)
                    .background(RoundedRectangle(cornerRadius: 7).fill(Color.black.opacity(0.28)))
                    .overlay { RoundedRectangle(cornerRadius: 7).stroke(accent.opacity(0.62), lineWidth: 1) }
                    .focused($branchFocused)
                    .onAppear {
                        requestBranchFocus()
                    }
                    .onSubmit { model.createWorktreeAndOpen() }
                    .onKeyPress(.tab) {
                        model.perform(.toggleWorktreeStartBranch)
                        return .handled
                    }
                    .onKeyPress(.escape) {
                        dismissWorktreeComposer()
                        return .handled
                    }
                    .disabled(model.isWorktreeCreationRunning)
            }
            HStack(spacing: 6) {
                worktreeSourceOption(
                    title: model.worktreeSelectedSourceBranch ?? "current branch",
                    subtitle: "selected checkout",
                    selected: !model.worktreeStartFromDefault
                ) {
                    model.worktreeStartFromDefault = false
                }
                worktreeSourceOption(
                    title: model.repoInfo.map { "origin/\($0.defaultBranch)" } ?? "default branch",
                    subtitle: "repository default",
                    selected: model.worktreeStartFromDefault
                ) {
                    model.worktreeStartFromDefault = true
                }
            }
            .disabled(model.isWorktreeCreationRunning)
            HStack(spacing: 6) {
                if model.isWorktreeCreationRunning {
                    ProgressView().tint(accent).controlSize(.small)
                    Text("Creating worktree...")
                        .foregroundStyle(accent.opacity(0.9))
                } else {
                    keyHint("↩", action: "create")
                    keyHint("Tab", action: "toggle source")
                    keyHint("Esc", action: "cancel")
                }
            }
            .font(.system(size: 9, design: .monospaced))
            .foregroundStyle(Color.white.opacity(0.4))
        }
        .padding(10)
        .background {
            RoundedRectangle(cornerRadius: 10)
                .fill(accent.opacity(0.045))
                .overlay {
                    RoundedRectangle(cornerRadius: 10)
                        .stroke(accent.opacity(0.25), lineWidth: 1)
                }
        }
    }

    private func worktreeSourceOption(title: String, subtitle: String, selected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 7) {
                Circle()
                    .stroke(selected ? accent : Color.white.opacity(0.22), lineWidth: 1)
                    .background {
                        Circle()
                            .fill(selected ? accent : .clear)
                            .padding(3)
                    }
                    .frame(width: 12, height: 12)
                VStack(alignment: .leading, spacing: 1) {
                    Text(title)
                        .font(.system(size: 9, weight: .medium, design: .monospaced))
                        .foregroundStyle(selected ? Color.white.opacity(0.9) : Color.white.opacity(0.52))
                        .lineLimit(1)
                    Text(subtitle.uppercased())
                        .font(.system(size: 7, weight: .medium, design: .monospaced))
                        .tracking(0.8)
                        .foregroundStyle(selected ? accent.opacity(0.85) : Color.white.opacity(0.25))
                }
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 8)
            .frame(height: 33)
            .background(RoundedRectangle(cornerRadius: 7).fill(selected ? accent.opacity(0.11) : Color.white.opacity(0.012)))
            .overlay {
                RoundedRectangle(cornerRadius: 7)
                    .stroke(selected ? accent.opacity(0.42) : Color.white.opacity(0.06), lineWidth: 1)
            }
        }
        .buttonStyle(.plain)
    }

    private var footer: some View {
        HStack(spacing: 12) {
            if let operation = model.operation, !model.isWorktreeCreationRunning {
                ProgressView().tint(accent)
                Text(operation).foregroundStyle(accent.opacity(0.9))
            } else if let error = model.error {
                Text(error).foregroundStyle(Color.red.opacity(0.9))
            } else {
                switch model.stage {
                case .location:
                    keyHint("↑↓", action: "navigate")
                    keyHint("Tab", action: "complete")
                    keyHint("↩", action: "select")
                case .destinations:
                    if !model.isCreatingWorktree {
                        keyHint("↑↓", action: "navigate")
                        keyHint("↩", action: "open")
                    }
                }
                keyHint("Esc", action: "back")
            }
            Spacer()
            Text(model.title)
                .tracking(0.8)
                .foregroundStyle(Color.white.opacity(0.32))
        }
        .font(.system(size: 10, design: .monospaced))
        .foregroundStyle(Color.white.opacity(0.4))
        .padding(.horizontal, 16)
        .frame(height: 31)
        .background(Color.black.opacity(0.12))
        .overlay(alignment: .top) { Divider().overlay(divider) }
    }

    private func keyHint(_ key: String, action: String) -> some View {
        HStack(spacing: 5) {
            Text(key)
                .font(.system(size: 8, weight: .semibold, design: .monospaced))
                .foregroundStyle(Color.white.opacity(0.68))
                .padding(.horizontal, 5)
                .frame(height: 17)
                .background(RoundedRectangle(cornerRadius: 4).fill(Color.white.opacity(0.04)))
                .overlay {
                    RoundedRectangle(cornerRadius: 4)
                        .stroke(Color.white.opacity(0.1), lineWidth: 1)
                }
            Text(action)
        }
    }

    private func sectionLabel(_ text: String) -> some View {
        Text(text)
            .font(.system(size: 9, weight: .bold, design: .monospaced))
            .tracking(1.4)
            .foregroundStyle(Color.white.opacity(0.36))
    }

    private func locationRow(name: String, path: String, icon: String, highlighted: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 9) {
                Image(systemName: icon)
                    .font(.system(size: 10, weight: .medium))
                    .foregroundStyle(highlighted ? Color.black.opacity(0.8) : Color.white.opacity(0.65))
                    .frame(width: 22, height: 22)
                    .background(RoundedRectangle(cornerRadius: 6).fill(highlighted ? accent : Color.white.opacity(0.035)))
                VStack(alignment: .leading, spacing: 2) {
                    Text(name).font(.system(size: 11, weight: .semibold, design: .monospaced))
                    Text(path).font(.system(size: 9, design: .monospaced)).foregroundStyle(Color.white.opacity(0.4))
                }
                Spacer()
            }
            .foregroundStyle(Color.white.opacity(0.9))
            .padding(.horizontal, 6)
            .frame(height: 35)
            .background(RoundedRectangle(cornerRadius: 7).fill(highlighted ? accent.opacity(0.105) : .clear))
            .overlay {
                RoundedRectangle(cornerRadius: 7)
                    .stroke(highlighted ? accent.opacity(0.43) : .clear, lineWidth: 1)
            }
        }
        .buttonStyle(.plain)
    }

    private func destinationRow(title: String, detail: String, symbol: String, color: Color, highlighted: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 10) {
                Image(systemName: symbol)
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundStyle(highlighted ? Color.black.opacity(0.82) : color)
                    .frame(width: 24, height: 24)
                    .background(RoundedRectangle(cornerRadius: 6).fill(highlighted ? accent : Color.white.opacity(0.035)))
                Text(title)
                    .font(.system(size: 12, weight: .semibold, design: .monospaced))
                    .foregroundStyle(.white.opacity(0.9))
                Spacer()
                Text(detail)
                    .font(.system(size: 9, design: .monospaced))
                    .foregroundStyle(.white.opacity(0.38))
                    .lineLimit(1)
            }
            .padding(.horizontal, 9)
            .frame(height: 34)
            .background(RoundedRectangle(cornerRadius: 7).fill(highlighted ? accent.opacity(0.105) : Color.white.opacity(0.012)))
            .overlay {
                RoundedRectangle(cornerRadius: 7)
                    .stroke(highlighted ? accent.opacity(0.43) : Color.white.opacity(0.035), lineWidth: 1)
            }
        }
        .buttonStyle(.plain)
    }

    private var headerContext: String {
        if let direction = model.direction {
            return "\(direction.rawValue.uppercased()) SPLIT"
        }
        return "ROOT PANE"
    }

    private var accent: Color { Color(red: 0.98, green: 0.48, blue: 0.28) }
    private var surface: Color { Color(red: 0.045, green: 0.047, blue: 0.055) }
    private var divider: Color { Color.white.opacity(0.09) }

    private func requestBranchFocus() {
        DispatchQueue.main.async {
            guard model.isCreatingWorktree else { return }
            branchFocused = true
        }
    }

    private func focusPathInputAtEnd() {
        DispatchQueue.main.async {
            guard model.stage == .location else { return }
            pathFocused = true
            DispatchQueue.main.async {
                guard model.stage == .location,
                      pathFocused,
                      let editor = NSApp.keyWindow?.firstResponder as? NSTextView,
                      editor.string == model.path else { return }
                editor.setSelectedRange(NSRange(location: editor.string.utf16.count, length: 0))
            }
        }
    }

    private func dismissWorktreeComposer() {
        branchFocused = false
        model.cancel()
    }

    private func choiceShortcut(_ choice: WorkspaceLauncherModel.PaneChoice) -> KeyEquivalent {
        switch choice {
        case .terminal: return "1"
        case .claude: return "2"
        case .codex: return "3"
        case .copilot: return "4"
        case .pi: return "5"
        }
    }

    private func choiceShortcutLabel(_ choice: WorkspaceLauncherModel.PaneChoice) -> String {
        switch choice {
        case .terminal: return "⌥1"
        case .claude: return "⌥2"
        case .codex: return "⌥3"
        case .copilot: return "⌥4"
        case .pi: return "⌥5"
        }
    }
}

enum WorkspaceLauncherDestinationKeyCommand {
    case up
    case down
    case accept
    case cancel
}

struct WorkspaceLauncherDestinationKeyCapture: NSViewRepresentable {
    let onCommand: (WorkspaceLauncherDestinationKeyCommand) -> Void

    func makeNSView(context: Context) -> WorkspaceLauncherDestinationKeyResponder {
        WorkspaceLauncherDestinationKeyResponder(onCommand: onCommand)
    }

    func updateNSView(_ nsView: WorkspaceLauncherDestinationKeyResponder, context: Context) {
        nsView.onCommand = onCommand
        DispatchQueue.main.async {
            guard nsView.window?.firstResponder !== nsView else { return }
            nsView.window?.makeFirstResponder(nsView)
        }
    }
}

final class WorkspaceLauncherDestinationKeyResponder: NSView {
    var onCommand: (WorkspaceLauncherDestinationKeyCommand) -> Void

    init(onCommand: @escaping (WorkspaceLauncherDestinationKeyCommand) -> Void) {
        self.onCommand = onCommand
        super.init(frame: .zero)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    override var acceptsFirstResponder: Bool { true }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            self.window?.makeFirstResponder(self)
        }
    }

    override func keyDown(with event: NSEvent) {
        switch event.keyCode {
        case 126:
            onCommand(.up)
        case 125:
            onCommand(.down)
        case 36, 76:
            onCommand(.accept)
        case 53:
            onCommand(.cancel)
        default:
            super.keyDown(with: event)
        }
    }
}
