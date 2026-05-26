import AppKit
import GhosttyKit
import XCTest
@testable import AttnNative

final class GhosttyScrollTranslationTests: XCTestCase {
    func testPreciseTrackpadScrollIsMarkedAsPreciseWithMomentum() {
        let translation = GhosttyScrollTranslation(
            deltaX: 1.25,
            deltaY: -4,
            precise: true,
            momentumPhase: .changed
        )

        XCTAssertEqual(translation.x, 2.5)
        XCTAssertEqual(translation.y, -8)
        XCTAssertEqual(translation.mods & 0b1, 0b1)
        XCTAssertEqual((translation.mods >> 1) & 0b111, Int32(GHOSTTY_MOUSE_MOMENTUM_CHANGED.rawValue))
    }

    func testDiscreteWheelScrollIsNotMarkedAsPreciseOrArtificiallyScaled() {
        let translation = GhosttyScrollTranslation(
            deltaX: 0,
            deltaY: -3,
            precise: false,
            momentumPhase: []
        )

        XCTAssertEqual(translation.x, 0)
        XCTAssertEqual(translation.y, -3)
        XCTAssertEqual(translation.mods, 0)
    }
}
