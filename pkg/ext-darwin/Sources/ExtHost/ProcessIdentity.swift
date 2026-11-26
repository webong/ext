import CExtLedger
import Foundation

public enum LedgerProcessIdentityError: Error {
    case invalidPID
    case unavailable
}

/// Reads the ledger's kernel process birth token. Surface guests use this exact
/// value when revalidating a window selected from the ledger's observation.
public enum LedgerProcessIdentity {
    public static func startID(pid: Int) throws -> String {
        guard pid > 0 else { throw LedgerProcessIdentityError.invalidPID }
        let input = Data("{\"pid\":\(pid)}".utf8)
        var output: UnsafeMutablePointer<CChar>?
        var length = 0
        let code = input.withUnsafeBytes { raw in
            ext_ledger_call(0, "process_start", raw.bindMemory(to: CChar.self).baseAddress,
                            raw.count, &output, &length)
        }
        defer { if let output { ext_ledger_free(output) } }
        guard code == Int32(EXT_LEDGER_OK.rawValue), let output, length > 0 else {
            throw LedgerProcessIdentityError.unavailable
        }
        struct Result: Decodable {
            let startID: String
            enum CodingKeys: String, CodingKey { case startID = "start_id" }
        }
        guard let value = try? JSONDecoder().decode(Result.self, from: Data(bytes: output, count: length)),
              !value.startID.isEmpty else { throw LedgerProcessIdentityError.unavailable }
        return value.startID
    }
}
