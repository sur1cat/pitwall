import Foundation

// MARK: - Report types, mirroring `pitwall coach --json`

struct ClassStat: Decodable, Equatable {
    let prompts: Int
    let spend: Double
}

struct Finding: Decodable, Equatable, Identifiable {
    let id: String
    let title: String
    let amount: Double
    let share: Double
    let detail: [String]?
    let action: String?
    let correlated: Bool
    /// The remedy, when there is one that can be carried out. Present only for
    /// findings whose fix is a thing pitwall can do; the rest are advice.
    let act: FindingAct?
}

/// FindingAct is a remedy the panel can run, so a finding is not a dead end.
struct FindingAct: Decodable, Equatable {
    let kind: String
    let target: String?
    let value: String?
    let label: String
}

struct ProjectStat: Decodable, Equatable, Identifiable {
    var id: String { repo }
    let repo: String
    let name: String
    let primed: Bool
    let sessions: Int
    let spend: Double
    let openingTurns: Int
    let openingSpend: Double

    enum CodingKeys: String, CodingKey {
        case repo, name, primed, sessions, spend
        case openingTurns = "opening_turns"
        case openingSpend = "opening_spend"
    }

    var openingCost: Double { openingTurns == 0 ? 0 : openingSpend / Double(openingTurns) }
}

struct CoachReport: Decodable, Equatable {
    let from: String
    let to: String
    let prompts: Int
    let spend: Double
    let byClass: [String: ClassStat]
    let projects: [ProjectStat]
    let findings: [Finding]

    enum CodingKeys: String, CodingKey {
        case from, to, prompts, spend, projects, findings
        case byClass = "by_class"
    }

    /// day trims an RFC3339 timestamp down to the date, which is all the
    /// header needs.
    static func day(_ stamp: String) -> String { String(stamp.prefix(10)) }
}

/// CoachLoader keeps the report out of the status path: the first scan reads
/// the whole transcript corpus, later ones come from pitwall's cache.
@MainActor
final class CoachLoader: ObservableObject {
    @Published private(set) var report: CoachReport?
    @Published private(set) var loading = false
    @Published private(set) var error: String?
    @Published private(set) var applying = false

    /// apply carries out a finding's remedy. Each one shells out to the same
    /// command the finding names in its text, so what the button does and what
    /// the text says can never drift apart.
    func apply(_ act: FindingAct) {
        guard !applying else { return }
        applying = true
        error = nil
        let bin = pitwallBinary
        let args: [String]
        switch act.kind {
        case "primer":
            guard let target = act.target, !target.isEmpty else { applying = false; return }
            args = ["primer", target, "--write"]
        case "effort-apply":
            args = ["effort", "--apply"]
        default:
            applying = false
            return
        }
        Task.detached(priority: .userInitiated) {
            let err = runQuietly(bin, args)
            await MainActor.run {
                self.applying = false
                if let err { self.error = err }
                self.reload()
            }
        }
    }

    func loadIfNeeded() {
        guard report == nil, !loading else { return }
        reload()
    }

    func reload() {
        loading = true
        error = nil
        let bin = pitwallBinary
        Task.detached(priority: .userInitiated) {
            let result = runCoach(bin)
            await MainActor.run {
                self.loading = false
                switch result {
                case .success(let r): self.report = r
                case .failure(let e): self.error = e.message
                }
            }
        }
    }
}

func runCoach(_ path: String) -> Result<CoachReport, LoadError> {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: path)
    process.arguments = ["coach", "--json"]
    let out = Pipe(), err = Pipe()
    process.standardOutput = out
    process.standardError = err
    do {
        try process.run()
    } catch {
        return .failure(LoadError(message: "cannot run \(path)"))
    }
    let data = out.fileHandleForReading.readDataToEndOfFile()
    let errData = err.fileHandleForReading.readDataToEndOfFile()
    process.waitUntilExit()
    guard process.terminationStatus == 0 else {
        let msg = String(data: errData, encoding: .utf8) ?? "exit \(process.terminationStatus)"
        return .failure(LoadError(message: msg.trimmingCharacters(in: .whitespacesAndNewlines)))
    }
    do {
        return .success(try JSONDecoder().decode(CoachReport.self, from: data))
    } catch {
        return .failure(LoadError(message: "could not read the report: \(error.localizedDescription)"))
    }
}


/// runQuietly executes a command for its effect and returns an error message
/// when it fails. The output is not parsed: what matters is whether it worked.
func runQuietly(_ path: String, _ args: [String]) -> String? {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: path)
    process.arguments = args
    let err = Pipe()
    process.standardOutput = Pipe()
    process.standardError = err
    do {
        try process.run()
    } catch {
        return "cannot run \(path)"
    }
    let data = err.fileHandleForReading.readDataToEndOfFile()
    process.waitUntilExit()
    if process.terminationStatus == 0 { return nil }
    let msg = String(data: data, encoding: .utf8) ?? "exit \(process.terminationStatus)"
    return msg.trimmingCharacters(in: .whitespacesAndNewlines)
}
