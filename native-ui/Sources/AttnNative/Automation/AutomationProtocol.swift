import Foundation

enum JSONValue: Codable, Equatable {
    case null
    case bool(Bool)
    case number(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Double.self) {
            self = .number(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([JSONValue].self) {
            self = .array(value)
        } else {
            self = .object(try container.decode([String: JSONValue].self))
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .null:
            try container.encodeNil()
        case .bool(let value):
            try container.encode(value)
        case .number(let value):
            try container.encode(value)
        case .string(let value):
            try container.encode(value)
        case .array(let value):
            try container.encode(value)
        case .object(let value):
            try container.encode(value)
        }
    }

    subscript(key: String) -> JSONValue? {
        guard case .object(let object) = self else { return nil }
        return object[key]
    }

    var stringValue: String? {
        guard case .string(let value) = self else { return nil }
        return value
    }

    var boolValue: Bool? {
        guard case .bool(let value) = self else { return nil }
        return value
    }
}

struct AutomationRequest: Decodable, Equatable {
    let id: String?
    let token: String
    let action: String
    let payload: JSONValue?
}

struct AutomationResponse: Encodable, Equatable {
    let id: String
    let ok: Bool
    let result: JSONValue?
    let error: String?

    static func success(id: String, result: JSONValue) -> AutomationResponse {
        AutomationResponse(id: id, ok: true, result: result, error: nil)
    }

    static func failure(id: String, error: String) -> AutomationResponse {
        AutomationResponse(id: id, ok: false, result: nil, error: error)
    }
}

struct AutomationManifest: Codable, Equatable {
    let enabled: Bool
    let port: UInt16
    let token: String
    let pid: Int32
    let started_at: String
}

struct AutomationActionError: Error, Equatable {
    let message: String

    init(_ message: String) {
        self.message = message
    }
}

typealias AutomationActionResult = Result<JSONValue, AutomationActionError>

extension Result where Failure == AutomationActionError {
    static func failure(_ message: String) -> Result<Success, AutomationActionError> {
        .failure(AutomationActionError(message))
    }
}

enum AutomationProtocol {
    static func process(
        line: Data,
        token: String,
        sequence: UInt64,
        dispatch: (String, JSONValue) async -> AutomationActionResult
    ) async -> AutomationResponse {
        let fallbackID = "ui-automation-\(sequence)"
        let request: AutomationRequest
        do {
            request = try JSONDecoder().decode(AutomationRequest.self, from: line)
        } catch {
            return .failure(id: fallbackID, error: "invalid request json: \(error.localizedDescription)")
        }
        let id = request.id ?? fallbackID
        guard constantTimeEquals(request.token, token) else {
            return .failure(id: id, error: "invalid token")
        }
        switch await dispatch(request.action, request.payload ?? .null) {
        case .success(let result):
            return .success(id: id, result: result)
        case .failure(let error):
            return .failure(id: id, error: error.message)
        }
    }

    private static func constantTimeEquals(_ left: String, _ right: String) -> Bool {
        let leftBytes = Array(left.utf8)
        let rightBytes = Array(right.utf8)
        guard leftBytes.count == rightBytes.count else { return false }
        return zip(leftBytes, rightBytes).reduce(0) { $0 | ($1.0 ^ $1.1) } == 0
    }
}
