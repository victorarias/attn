import Darwin
import Foundation

@_silgen_name("flock")
private func processFileLock(_ descriptor: Int32, _ operation: Int32) -> Int32

enum ClientInstanceLeaseResult {
    case acquired(ClientInstanceLease)
    case occupied(ownerProcessID: pid_t?)
}

final class ClientInstanceLease {
    private var descriptor: Int32?

    private init(descriptor: Int32) {
        self.descriptor = descriptor
    }

    deinit {
        release()
    }

    static func acquire(
        profile: AutomationProfile,
        processID: pid_t = ProcessInfo.processInfo.processIdentifier,
        fileManager: FileManager = .default
    ) throws -> ClientInstanceLeaseResult {
        let applicationSupportURL = profile.manifestURL
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
        let profileName = profile.name ?? "default"
        let lockURL = applicationSupportURL
            .appendingPathComponent("com.attn/client-instances", isDirectory: true)
            .appendingPathComponent("\(profileName).lock")
        try fileManager.createDirectory(
            at: lockURL.deletingLastPathComponent(),
            withIntermediateDirectories: true
        )

        let descriptor = lockURL.path.withCString {
            Darwin.open($0, O_RDWR | O_CREAT, S_IRUSR | S_IWUSR)
        }
        guard descriptor >= 0 else {
            throw POSIXLeaseError(operation: "open", code: errno)
        }

        guard processFileLock(descriptor, LOCK_EX | LOCK_NB) == 0 else {
            let lockError = errno
            if lockError == EWOULDBLOCK {
                let ownerProcessID = readProcessID(from: descriptor)
                Darwin.close(descriptor)
                return .occupied(ownerProcessID: ownerProcessID)
            }
            Darwin.close(descriptor)
            throw POSIXLeaseError(operation: "flock", code: lockError)
        }

        do {
            try write(processID: processID, to: descriptor)
            return .acquired(ClientInstanceLease(descriptor: descriptor))
        } catch {
            _ = processFileLock(descriptor, LOCK_UN)
            Darwin.close(descriptor)
            throw error
        }
    }

    func release() {
        guard let descriptor else { return }
        self.descriptor = nil
        _ = processFileLock(descriptor, LOCK_UN)
        Darwin.close(descriptor)
    }

    private static func readProcessID(from descriptor: Int32) -> pid_t? {
        guard Darwin.lseek(descriptor, 0, SEEK_SET) >= 0 else { return nil }
        var buffer = [UInt8](repeating: 0, count: 32)
        let bytesRead = Darwin.read(descriptor, &buffer, buffer.count)
        guard bytesRead > 0 else { return nil }
        let value = String(decoding: buffer.prefix(Int(bytesRead)), as: UTF8.self)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return pid_t(value)
    }

    private static func write(processID: pid_t, to descriptor: Int32) throws {
        let data = Data("\(processID)\n".utf8)
        guard Darwin.ftruncate(descriptor, 0) == 0,
              Darwin.lseek(descriptor, 0, SEEK_SET) >= 0 else {
            throw POSIXLeaseError(operation: "truncate", code: errno)
        }
        let written = data.withUnsafeBytes { bytes in
            Darwin.write(descriptor, bytes.baseAddress, bytes.count)
        }
        guard written == data.count else {
            throw POSIXLeaseError(operation: "write", code: errno)
        }
    }
}

private struct POSIXLeaseError: LocalizedError {
    let operation: String
    let code: Int32

    var errorDescription: String? {
        "\(operation) client instance lease: \(String(cString: strerror(code)))"
    }
}
