import CoreGraphics

enum WindowPlacementPolicy {
    static let minimumUsefulVisibleWidth: CGFloat = 120
    static let minimumUsefulVisibleHeight: CGFloat = 120

    static func shouldRecover(
        windowFrame: CGRect,
        visibleScreenFrames: [CGRect]
    ) -> Bool {
        guard !visibleScreenFrames.isEmpty else { return false }
        let usefulWidth = min(minimumUsefulVisibleWidth, windowFrame.width)
        let usefulHeight = min(minimumUsefulVisibleHeight, windowFrame.height)
        return !visibleScreenFrames.contains { screenFrame in
            let intersection = windowFrame.intersection(screenFrame)
            return !intersection.isNull &&
                intersection.width >= usefulWidth &&
                intersection.height >= usefulHeight
        }
    }
}
