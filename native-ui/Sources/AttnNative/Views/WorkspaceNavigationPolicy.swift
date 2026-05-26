import Foundation

enum WorkspaceNavigationDirection: String, Equatable {
    case left
    case right
    case up
    case down

    var workspaceOffset: Int {
        switch self {
        case .left, .up:
            return -1
        case .right, .down:
            return 1
        }
    }
}

enum WorkspaceNavigationIntent: Equatable {
    case focusPane(workspaceID: String, paneID: String)
    case selectWorkspace(String)
    case none
}

enum WorkspaceNavigationPolicy {
    static func intent(
        direction: WorkspaceNavigationDirection,
        selectedWorkspaceID: String?,
        workspaces: [WorkspaceSnapshot]
    ) -> WorkspaceNavigationIntent {
        guard let selectedWorkspaceID,
              let index = workspaces.firstIndex(where: { $0.id == selectedWorkspaceID }) else {
            return .none
        }
        if let layout = workspaces[index].layout,
           let root = layout.rootNode,
           let nextPaneID = pane(in: root, from: layout.activePaneID, direction: direction) {
            return .focusPane(workspaceID: selectedWorkspaceID, paneID: nextPaneID)
        }
        guard workspaces.count > 1 else { return .none }
        let nextIndex = (index + direction.workspaceOffset + workspaces.count) % workspaces.count
        return .selectWorkspace(workspaces[nextIndex].id)
    }

    private static func pane(
        in root: WorkspaceLayoutNode,
        from paneID: String,
        direction: WorkspaceNavigationDirection
    ) -> String? {
        var bounds: [String: Bounds] = [:]
        collectBounds(node: root, within: Bounds(left: 0, top: 0, right: 1, bottom: 1), into: &bounds)
        guard let current = bounds[paneID] else { return nil }

        return bounds
            .filter { $0.key != paneID }
            .compactMap { candidateID, candidate -> Candidate? in
                switch direction {
                case .left:
                    return horizontalCandidate(
                        id: candidateID,
                        candidate: candidate,
                        current: current,
                        primary: current.left - candidate.right
                    )
                case .right:
                    return horizontalCandidate(
                        id: candidateID,
                        candidate: candidate,
                        current: current,
                        primary: candidate.left - current.right
                    )
                case .up:
                    return verticalCandidate(
                        id: candidateID,
                        candidate: candidate,
                        current: current,
                        primary: current.top - candidate.bottom
                    )
                case .down:
                    return verticalCandidate(
                        id: candidateID,
                        candidate: candidate,
                        current: current,
                        primary: candidate.top - current.bottom
                    )
                }
            }
            .sorted {
                if $0.primary != $1.primary { return $0.primary < $1.primary }
                if $0.secondary != $1.secondary { return $0.secondary < $1.secondary }
                return $0.id < $1.id
            }
            .first?
            .id
    }

    private static func horizontalCandidate(
        id: String,
        candidate: Bounds,
        current: Bounds,
        primary: Double
    ) -> Candidate? {
        guard primary >= -0.000_001,
              overlap(current.top, current.bottom, candidate.top, candidate.bottom) > 0 else {
            return nil
        }
        return Candidate(id: id, primary: primary, secondary: abs(current.centerY - candidate.centerY))
    }

    private static func verticalCandidate(
        id: String,
        candidate: Bounds,
        current: Bounds,
        primary: Double
    ) -> Candidate? {
        guard primary >= -0.000_001,
              overlap(current.left, current.right, candidate.left, candidate.right) > 0 else {
            return nil
        }
        return Candidate(id: id, primary: primary, secondary: abs(current.centerX - candidate.centerX))
    }

    private static func overlap(_ firstStart: Double, _ firstEnd: Double, _ secondStart: Double, _ secondEnd: Double) -> Double {
        max(0, min(firstEnd, secondEnd) - max(firstStart, secondStart))
    }

    private static func collectBounds(
        node: WorkspaceLayoutNode,
        within bounds: Bounds,
        into result: inout [String: Bounds]
    ) {
        switch node {
        case .pane(let paneID):
            result[paneID] = bounds
        case .split(let direction, let ratio, let children):
            guard children.count >= 2 else { return }
            let resolvedRatio = ratio > 0 && ratio < 1 ? ratio : 0.5
            if direction == .vertical {
                let split = bounds.left + (bounds.right - bounds.left) * resolvedRatio
                collectBounds(
                    node: children[0],
                    within: Bounds(left: bounds.left, top: bounds.top, right: split, bottom: bounds.bottom),
                    into: &result
                )
                collectBounds(
                    node: children[1],
                    within: Bounds(left: split, top: bounds.top, right: bounds.right, bottom: bounds.bottom),
                    into: &result
                )
            } else {
                let split = bounds.top + (bounds.bottom - bounds.top) * resolvedRatio
                collectBounds(
                    node: children[0],
                    within: Bounds(left: bounds.left, top: bounds.top, right: bounds.right, bottom: split),
                    into: &result
                )
                collectBounds(
                    node: children[1],
                    within: Bounds(left: bounds.left, top: split, right: bounds.right, bottom: bounds.bottom),
                    into: &result
                )
            }
        }
    }

    private struct Bounds {
        let left: Double
        let top: Double
        let right: Double
        let bottom: Double

        var centerX: Double { (left + right) / 2 }
        var centerY: Double { (top + bottom) / 2 }
    }

    private struct Candidate {
        let id: String
        let primary: Double
        let secondary: Double
    }
}
