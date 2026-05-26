import SwiftUI

struct WorkspacePaneTreeView: View {
    let node: WorkspaceLayoutNode
    let layout: WorkspaceLayoutSnapshot
    @ObservedObject var daemon: DaemonConnection
    let allowsActiveFocusClaim: Bool

    var body: some View {
        switch node {
        case .pane(let paneID):
            paneLeaf(paneID)
        case .split(let direction, let ratio, let children):
            if children.count == 2 {
                RatioSplitView(direction: direction, initialRatio: ratio) {
                    WorkspacePaneTreeView(node: children[0], layout: layout, daemon: daemon, allowsActiveFocusClaim: allowsActiveFocusClaim)
                } second: {
                    WorkspacePaneTreeView(node: children[1], layout: layout, daemon: daemon, allowsActiveFocusClaim: allowsActiveFocusClaim)
                }
            } else {
                EmptyView()
            }
        }
    }

    @ViewBuilder
    private func paneLeaf(_ paneID: String) -> some View {
        if let pane = layout.panes.first(where: { $0.paneID == paneID }),
           let runtimeID = pane.runtimeID {
            let inactiveOverlayOpacity = WorkspacePaneAppearance.inactiveOverlayOpacity(paneID: paneID, layout: layout)
            GhosttyTerminalRepresentable(
                runtimeID: runtimeID,
                shouldClaimFocus: allowsActiveFocusClaim && paneID == layout.activePaneID,
                daemon: daemon
            )
                .id(runtimeID)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(Color.black)
                .overlay {
                    ZStack {
                        if inactiveOverlayOpacity > 0 {
                            Rectangle()
                                .fill(Color.black)
                                .opacity(inactiveOverlayOpacity)
                                .allowsHitTesting(false)
                        }
                        Rectangle()
                            .stroke(paneID == layout.activePaneID ? Color.orange.opacity(0.7) : Color.clear, lineWidth: 1)
                    }
                }
        } else {
            Color.black
        }
    }
}

private struct RatioSplitView<First: View, Second: View>: View {
    let direction: SplitDirection
    let initialRatio: Double
    let first: First
    let second: Second

    @State private var draggedRatio: CGFloat?
    @State private var dragStartRatio: CGFloat?

    private let dividerThickness: CGFloat = 1
    private let dividerHitWidth: CGFloat = 9

    init(
        direction: SplitDirection,
        initialRatio: Double,
        @ViewBuilder first: () -> First,
        @ViewBuilder second: () -> Second
    ) {
        self.direction = direction
        self.initialRatio = initialRatio
        self.first = first()
        self.second = second()
    }

    private var resolvedRatio: CGFloat {
        let initial = CGFloat(initialRatio)
        return min(max(draggedRatio ?? initial, 0.1), 0.9)
    }

    var body: some View {
        GeometryReader { geometry in
            if direction == .vertical {
                horizontalSplit(size: geometry.size)
            } else {
                verticalSplit(size: geometry.size)
            }
        }
    }

    @ViewBuilder
    private func horizontalSplit(size: CGSize) -> some View {
        let available = max(size.width - dividerThickness, 0)
        let firstWidth = available * resolvedRatio
        HStack(spacing: 0) {
            first.frame(width: firstWidth, height: size.height)
            divider
                .frame(width: dividerThickness, height: size.height)
                .gesture(horizontalDrag(total: available))
            second.frame(width: available - firstWidth, height: size.height)
        }
        .frame(width: size.width, height: size.height, alignment: .leading)
    }

    @ViewBuilder
    private func verticalSplit(size: CGSize) -> some View {
        let available = max(size.height - dividerThickness, 0)
        let firstHeight = available * resolvedRatio
        VStack(spacing: 0) {
            first.frame(width: size.width, height: firstHeight)
            divider
                .frame(width: size.width, height: dividerThickness)
                .gesture(verticalDrag(total: available))
            second.frame(width: size.width, height: available - firstHeight)
        }
        .frame(width: size.width, height: size.height, alignment: .top)
    }

    private var divider: some View {
        Rectangle()
            .fill(Color.white.opacity(0.15))
            .contentShape(Rectangle().inset(by: -dividerHitWidth / 2))
    }

    private func horizontalDrag(total: CGFloat) -> some Gesture {
        DragGesture(minimumDistance: 0)
            .onChanged { value in
                guard total > 0 else { return }
                if dragStartRatio == nil {
                    dragStartRatio = resolvedRatio
                }
                draggedRatio = min(max((dragStartRatio ?? resolvedRatio) + value.translation.width / total, 0.1), 0.9)
            }
            .onEnded { _ in
                dragStartRatio = nil
            }
    }

    private func verticalDrag(total: CGFloat) -> some Gesture {
        DragGesture(minimumDistance: 0)
            .onChanged { value in
                guard total > 0 else { return }
                if dragStartRatio == nil {
                    dragStartRatio = resolvedRatio
                }
                draggedRatio = min(max((dragStartRatio ?? resolvedRatio) + value.translation.height / total, 0.1), 0.9)
            }
            .onEnded { _ in
                dragStartRatio = nil
            }
    }
}
