import Foundation
import XCTest
@testable import AttnNative

final class ClientInstanceLeaseTests: XCTestCase {
    func testSecondClientForSameProfileSeesCurrentOwnerUntilLeaseReleases() throws {
        let homeDirectory = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("attn-native-instance-\(UUID().uuidString)", isDirectory: true)
        let profile = AutomationProfile.current(
            environment: ["ATTN_PROFILE": "test-profile"],
            bundleIdentifier: "com.attn.native.dev",
            homeDirectory: homeDirectory
        )

        let first = try ClientInstanceLease.acquire(profile: profile, processID: 4312)
        guard case .acquired(let owner) = first else {
            return XCTFail("expected first client to acquire its profile lease")
        }

        let second = try ClientInstanceLease.acquire(profile: profile, processID: 4313)
        guard case .occupied(let ownerProcessID) = second else {
            return XCTFail("expected second client for the same profile to be rejected")
        }
        XCTAssertEqual(ownerProcessID, 4312)

        owner.release()

        let third = try ClientInstanceLease.acquire(profile: profile, processID: 4314)
        guard case .acquired(let replacement) = third else {
            return XCTFail("expected replacement client to acquire released profile lease")
        }
        replacement.release()
    }

    func testDifferentProfilesCanRunConcurrently() throws {
        let homeDirectory = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("attn-native-instance-\(UUID().uuidString)", isDirectory: true)
        let firstProfile = AutomationProfile.current(
            environment: ["ATTN_PROFILE": "profile-a"],
            bundleIdentifier: "com.attn.native.dev",
            homeDirectory: homeDirectory
        )
        let secondProfile = AutomationProfile.current(
            environment: ["ATTN_PROFILE": "profile-b"],
            bundleIdentifier: "com.attn.native.dev",
            homeDirectory: homeDirectory
        )

        let first = try ClientInstanceLease.acquire(profile: firstProfile, processID: 4312)
        let second = try ClientInstanceLease.acquire(profile: secondProfile, processID: 4313)
        guard case .acquired(let firstOwner) = first,
              case .acquired(let secondOwner) = second else {
            return XCTFail("expected independent profile leases to coexist")
        }
        firstOwner.release()
        secondOwner.release()
    }
}
