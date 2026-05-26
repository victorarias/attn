import XCTest
@testable import AttnNative

final class AppEnvironmentTests: XCTestCase {
    func testDefaultsToIsolatedDevDaemon() {
        XCTAssertEqual(
            AppEnvironment.daemonURL(environment: [:]).absoluteString,
            "ws://localhost:29849/ws"
        )
    }

    func testAcceptsDaemonURLOverride() {
        XCTAssertEqual(
            AppEnvironment.daemonURL(environment: ["ATTN_NATIVE_WS_URL": "ws://localhost:4000/ws"]).port,
            4000
        )
    }

    func testAcceptsPositiveLaunchGuardProcessID() {
        XCTAssertEqual(
            AppEnvironment.launchGuardProcessID(environment: ["ATTN_NATIVE_LAUNCH_GUARD_PID": " 4312 "]),
            4312
        )
        XCTAssertNil(AppEnvironment.launchGuardProcessID(environment: ["ATTN_NATIVE_LAUNCH_GUARD_PID": "0"]))
        XCTAssertNil(AppEnvironment.launchGuardProcessID(environment: ["ATTN_NATIVE_LAUNCH_GUARD_PID": "not-a-pid"]))
    }
}
