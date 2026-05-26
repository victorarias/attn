import Foundation
import XCTest
@testable import AttnNative

final class AutomationProfileTests: XCTestCase {
    func testDevBundleEnablesAutomationWithoutLaunchEnvironment() {
        let profile = AutomationProfile.current(
            environment: [:],
            bundleIdentifier: "com.attn.native.dev",
            homeDirectory: URL(fileURLWithPath: "/tmp/attn-home")
        )

        XCTAssertEqual(profile.name, "dev")
        XCTAssertTrue(profile.automationEnabled)
        XCTAssertTrue(profile.manifestURL.path.hasSuffix(
            "/Library/Application Support/com.attn.native.dev/debug/ui-automation.json"
        ))
    }

    func testExplicitDisableOverridesDevelopmentDefault() {
        let profile = AutomationProfile.current(
            environment: [
                "ATTN_PROFILE": "dev",
                "ATTN_AUTOMATION": "0",
                "ATTN_AUTOMATION_BACKGROUND": "1",
            ],
            bundleIdentifier: "com.attn.native.dev"
        )

        XCTAssertFalse(profile.automationEnabled)
        XCTAssertFalse(profile.backgroundWindow)
    }

    func testBackgroundModeRequiresEnabledAutomation() {
        let profile = AutomationProfile.current(
            environment: [
                "ATTN_AUTOMATION": "1",
                "ATTN_AUTOMATION_BACKGROUND": "1",
                "ATTN_AUTOMATION_RESTORE_FOREGROUND_PID": "4321",
            ],
            bundleIdentifier: "com.attn.native"
        )

        XCTAssertTrue(profile.automationEnabled)
        XCTAssertTrue(profile.backgroundWindow)
        XCTAssertEqual(profile.restoreForegroundProcessID, 4321)
    }
}
