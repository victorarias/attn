import CoreGraphics
import XCTest
@testable import AttnNative

final class WindowPlacementPolicyTests: XCTestCase {
    private let screen = CGRect(x: 0, y: 0, width: 1728, height: 1117)
    private let windowSize = CGSize(width: 1240, height: 760)

    func testRecoversHarnessParkedWindowWithOnlyAnEdgeStripVisible() {
        let parked = CGRect(
            x: screen.maxX - 20,
            y: 100,
            width: windowSize.width,
            height: windowSize.height
        )

        XCTAssertTrue(
            WindowPlacementPolicy.shouldRecover(windowFrame: parked, visibleScreenFrames: [screen])
        )
    }

    func testPreservesNormallyPositionedVisibleWindow() {
        let visible = CGRect(x: 100, y: 100, width: windowSize.width, height: windowSize.height)

        XCTAssertFalse(
            WindowPlacementPolicy.shouldRecover(windowFrame: visible, visibleScreenFrames: [screen])
        )
    }

    func testPreservesWindowVisibleOnAnotherDisplay() {
        let secondScreen = CGRect(x: screen.maxX, y: 0, width: 1728, height: 1117)
        let visibleOnSecond = CGRect(x: screen.maxX + 80, y: 80, width: windowSize.width, height: windowSize.height)

        XCTAssertFalse(
            WindowPlacementPolicy.shouldRecover(
                windowFrame: visibleOnSecond,
                visibleScreenFrames: [screen, secondScreen]
            )
        )
    }
}
