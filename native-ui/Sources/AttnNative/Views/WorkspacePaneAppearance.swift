enum WorkspacePaneAppearance {
    // Ghostty's default unfocused-split-opacity is 0.7, applied as a 0.3 background overlay.
    static let inactiveSplitOverlayOpacity = 0.3

    static func inactiveOverlayOpacity(paneID: String, layout: WorkspaceLayoutSnapshot) -> Double {
        guard layout.panes.count > 1, paneID != layout.activePaneID else { return 0 }
        return inactiveSplitOverlayOpacity
    }
}
