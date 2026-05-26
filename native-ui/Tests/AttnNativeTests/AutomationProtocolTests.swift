import Foundation
import XCTest
@testable import AttnNative

final class AutomationProtocolTests: XCTestCase {
    func testAcceptedRequestDispatchesActionAndPayload() async {
        let line = Data(#"{"id":"client-1","token":"secret","action":"get_state","payload":{"include":true}}"#.utf8)

        let response = await AutomationProtocol.process(line: line, token: "secret", sequence: 7) { action, payload in
            XCTAssertEqual(action, "get_state")
            XCTAssertEqual(payload["include"], .bool(true))
            return .success(.object(["ready": .bool(true)]))
        }

        XCTAssertEqual(
            response,
            .success(id: "client-1", result: .object(["ready": .bool(true)]))
        )
    }

    func testInvalidTokenDoesNotDispatch() async {
        let line = Data(#"{"token":"wrong","action":"ping"}"#.utf8)
        var dispatched = false

        let response = await AutomationProtocol.process(line: line, token: "secret", sequence: 2) { _, _ in
            dispatched = true
            return .success(.null)
        }

        XCTAssertFalse(dispatched)
        XCTAssertEqual(response, .failure(id: "ui-automation-2", error: "invalid token"))
    }
}
