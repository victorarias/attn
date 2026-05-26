import SwiftUI

struct RootWindowView: View {
    @ObservedObject var daemon: DaemonConnection
    @ObservedObject var launcher: WorkspaceLauncherModel

    var body: some View {
        HSplitView {
            VStack(alignment: .leading, spacing: 12) {
                Text("Workspaces")
                    .font(.headline)
                    .foregroundStyle(.secondary)
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 4) {
                        ForEach(daemon.workspaces) { workspace in
                            workspaceRow(workspace)
                        }
                    }
                }
                Spacer()
                Text(daemon.state.label)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
            }
            .padding(18)
            .frame(minWidth: 220, idealWidth: 250, maxWidth: 280, alignment: .topLeading)
            .background(Color(nsColor: .windowBackgroundColor))

            if let layout = daemon.selectedWorkspace?.layout, let root = layout.rootNode {
                WorkspacePaneTreeView(
                    node: root,
                    layout: layout,
                    daemon: daemon,
                    allowsActiveFocusClaim: !launcher.isPresented
                )
                    .background(Color.black)
            } else {
                if daemon.state == .ready {
                    ZStack {
                        Color.black
                        VStack(spacing: 10) {
                            Text("No panes open")
                                .font(.callout.monospaced())
                                .foregroundStyle(.secondary)
                            Text("Cmd+Shift+N  New Workspace")
                                .font(.caption.monospaced())
                                .foregroundStyle(.tertiary)
                        }
                    }
                } else {
                    Text("Connecting to daemon...")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .background(Color(nsColor: .textBackgroundColor))
                }
            }
        }
        .frame(minWidth: 900, minHeight: 560)
        .overlay {
            if launcher.isPresented {
                ZStack {
                    Color.black.opacity(0.48)
                        .contentShape(Rectangle())
                        .onTapGesture {
                            launcher.cancel()
                        }

                    WorkspaceLauncherView(model: launcher)
                }
                .ignoresSafeArea()
                .transition(.opacity)
            }
        }
        .animation(.easeOut(duration: 0.12), value: launcher.isPresented)
        .onChange(of: launcher.isPresented) { _, isPresented in
            guard !isPresented else { return }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
                guard let layout = daemon.selectedWorkspace?.layout,
                      let runtimeID = layout.panes.first(where: { $0.paneID == layout.activePaneID })?.runtimeID else { return }
                daemon.terminalSurface(runtimeID: runtimeID)?.focus()
            }
        }
    }

    @ViewBuilder
    private func workspaceRow(_ workspace: WorkspaceSnapshot) -> some View {
        let selected = daemon.selectedWorkspaceID == workspace.id
        Button {
            daemon.selectWorkspace(workspace.id)
        } label: {
            VStack(alignment: .leading, spacing: 7) {
                HStack {
                    Text(workspace.title.isEmpty ? workspace.directory : workspace.title)
                        .font(.body.weight(.medium))
                        .lineLimit(1)
                    Spacer()
                    Circle()
                        .fill(statusColor(workspace.status))
                        .frame(width: 7, height: 7)
                }
                ForEach(workspace.layout?.panes ?? []) { pane in
                    HStack(spacing: 7) {
                        Circle()
                            .fill(pane.kind == "shell" ? Color.secondary : Color.orange)
                            .frame(width: 6, height: 6)
                        Text(pane.title)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(selected ? Color.accentColor.opacity(0.16) : Color.clear)
            .clipShape(RoundedRectangle(cornerRadius: 8))
        }
        .buttonStyle(.plain)
    }

    private func statusColor(_ status: String) -> Color {
        switch status {
        case "working": return .orange
        case "waiting_input", "pending_approval": return .yellow
        default: return .secondary.opacity(0.6)
        }
    }
}
