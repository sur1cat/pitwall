import SwiftUI

// MARK: - Models

struct DoctorCheck: Decodable, Equatable, Identifiable {
    let name: String
    let state: String
    let detail: String
    let fix: String?
    var id: String { name }
}

struct Tuning: Decodable, Equatable, Identifiable {
    enum CodingKeys: String, CodingKey {
        case key, because, tradeoff
        case alreadySet = "already_set"
    }
    let key: String
    let because: String
    let tradeoff: String
    let alreadySet: Bool
    var id: String { key }
}

struct GateMove: Decodable, Equatable {
    enum CodingKeys: String, CodingKey {
        case turnedOn = "TurnedOn"
        case turnedOff = "TurnedOff"
        case appeared = "Appeared"
    }
    let turnedOn: [String]?
    let turnedOff: [String]?
    let appeared: [String]?

    var moved: [(String, String)] {
        (turnedOn ?? []).map { ("on", $0) }
            + (turnedOff ?? []).map { ("off", $0) }
            + (appeared ?? []).map { ("new", $0) }
    }
}

struct Drift: Decodable, Equatable {
    enum CodingKeys: String, CodingKey {
        case gatesOn = "gates_on"
        case gatesTotal = "gates_total"
        case sinceLast = "since_last"
    }
    let gatesOn: Int
    let gatesTotal: Int
    let sinceLast: GateMove?
}

/// HealthLoader reads the three things that describe the state of the setup
/// rather than the work: what pitwall can read, which settings are worth
/// changing, and which server-side switches have moved.
@MainActor
final class HealthLoader: ObservableObject {
    @Published private(set) var checks: [DoctorCheck] = []
    @Published private(set) var tunings: [Tuning] = []
    @Published private(set) var drift: Drift?
    @Published private(set) var loading = false
    @Published private(set) var applying = false
    @Published private(set) var error: String?

    func loadIfNeeded() {
        guard checks.isEmpty, !loading else { return }
        reload()
    }

    func reload() {
        loading = true
        error = nil
        let bin = pitwallBinary
        Task.detached(priority: .utility) {
            let d = decode([DoctorCheck].self, from: runJSON(bin, ["doctor", "--json"]), key: "checks")
            let t = decode([Tuning].self, from: runJSON(bin, ["tune", "--json"]), key: "suggestions")
            // Reading the gates records a snapshot, so this runs once per load
            // rather than on a timer: a reading per poll would bury the
            // changes the history exists to show.
            let g = decodeWhole(Drift.self, from: runJSON(bin, ["drift", "--json"]))
            await MainActor.run {
                self.loading = false
                self.checks = d ?? []
                self.tunings = t ?? []
                self.drift = g
            }
        }
    }

    /// apply writes the suggested settings. It is the one action here that
    /// changes anything, so it re-reads afterwards rather than assuming.
    func apply() {
        guard !applying else { return }
        applying = true
        let bin = pitwallBinary
        Task.detached(priority: .userInitiated) {
            let err = runQuietly(bin, ["tune", "--write"])
            await MainActor.run {
                self.applying = false
                if let err { self.error = err }
                self.reload()
            }
        }
    }
}

/// runJSON runs a command and returns its output, or nil when it failed.
func runJSON(_ path: String, _ args: [String]) -> Data? {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: path)
    process.arguments = args
    let out = Pipe()
    process.standardOutput = out
    process.standardError = Pipe()
    do { try process.run() } catch { return nil }
    let data = out.fileHandleForReading.readDataToEndOfFile()
    process.waitUntilExit()
    return process.terminationStatus == 0 ? data : nil
}

/// decode pulls one array out of a JSON object by key.
func decode<T: Decodable>(_ type: T.Type, from data: Data?, key: String) -> T? {
    guard let data,
          let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          let inner = obj[key],
          let raw = try? JSONSerialization.data(withJSONObject: inner) else { return nil }
    return try? JSONDecoder().decode(T.self, from: raw)
}

func decodeWhole<T: Decodable>(_ type: T.Type, from data: Data?) -> T? {
    guard let data else { return nil }
    return try? JSONDecoder().decode(T.self, from: data)
}

// MARK: - Views

/// HealthSection is the state of the setup, shown in Setup: what pitwall can
/// read, what is worth changing, and what moved on Anthropic's side.
struct HealthSection: View {
    @ObservedObject var health: HealthLoader
    @Environment(\.uiLanguage) private var lang

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let d = health.drift { driftBlock(d) }
            if !health.tunings.isEmpty { tuneBlock() }
            if !health.checks.isEmpty { doctorBlock() }
            if let e = health.error {
                Text(e).font(.system(size: 10)).foregroundStyle(.orange)
                    .padding(.horizontal, 14).padding(.top, 4)
            }
        }
    }

    /// driftBlock reports the switches Anthropic turns on per account. When one
    /// moves, behaviour changes for no visible reason — this is the only local
    /// evidence that it happened.
    private func driftBlock(_ d: Drift) -> some View {
        Block(title: "What changed under you") {
            VStack(alignment: .leading, spacing: 4) {
                let moved = d.sinceLast?.moved ?? []
                if moved.isEmpty {
                    Text(lang == .ru
                         ? "\(d.gatesOn) из \(d.gatesTotal) серверных переключателей включены. С прошлой проверки ничего не сдвинулось."
                         : "\(d.gatesOn) of \(d.gatesTotal) server-side switches are on. Nothing has moved since the last reading.")
                        .font(.system(size: 11)).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                } else {
                    Text(lang == .ru ? "Сдвинулось с прошлой проверки:" : "Moved since the last reading:")
                        .font(.system(size: 11)).foregroundStyle(.orange)
                    ForEach(moved.prefix(6), id: \.1) { how, name in
                        HStack(spacing: 6) {
                            Text(how).font(.system(size: 9, weight: .medium))
                                .foregroundStyle(how == "off" ? Color.red : .green)
                                .frame(width: 22, alignment: .leading)
                            Text(name).font(.system(size: 10, design: .monospaced))
                                .lineLimit(1).truncationMode(.middle)
                        }
                    }
                    Text(lang == .ru
                         ? "Что переключатель делает — отсюда не узнать. Это говорит, что он сдвинулся, и когда."
                         : "What a switch does is not knowable from here. This says one moved, and when.")
                        .font(.system(size: 9)).foregroundStyle(.tertiary)
                        .fixedSize(horizontal: false, vertical: true).padding(.top, 1)
                }
            }
        }
    }

    private func tuneBlock() -> some View {
        Block(title: "Settings worth changing") {
            VStack(alignment: .leading, spacing: 6) {
                ForEach(health.tunings) { t in
                    VStack(alignment: .leading, spacing: 1) {
                        HStack(spacing: 5) {
                            Image(systemName: t.alreadySet ? "checkmark.circle.fill" : "plus.circle")
                                .font(.system(size: 10))
                                .foregroundStyle(t.alreadySet ? Color.secondary : .green)
                            Text(t.key).font(.system(size: 11, weight: .medium, design: .monospaced))
                        }
                        Text(t.because).font(.system(size: 10)).foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                        Text((lang == .ru ? "стоит: " : "costs you: ") + t.tradeoff)
                            .font(.system(size: 9)).foregroundStyle(.tertiary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                if health.tunings.contains(where: { !$0.alreadySet }) {
                    HStack(spacing: 8) {
                        Button(lang == .ru ? "Применить" : "Apply") { health.apply() }
                            .buttonStyle(.bordered).controlSize(.small).disabled(health.applying)
                        if health.applying { ProgressView().controlSize(.small) }
                        Text(lang == .ru ? "файл настроек копируется перед записью"
                                         : "the settings file is copied first")
                            .font(.system(size: 9)).foregroundStyle(.tertiary)
                    }
                }
            }
        }
    }

    private func doctorBlock() -> some View {
        Block(title: "What pitwall can read") {
            VStack(alignment: .leading, spacing: 3) {
                ForEach(health.checks) { c in
                    HStack(alignment: .top, spacing: 6) {
                        Image(systemName: c.state == "fail" ? "xmark.circle.fill"
                              : c.state == "warn" ? "exclamationmark.circle" : "checkmark.circle")
                            .font(.system(size: 9))
                            .foregroundStyle(c.state == "fail" ? Color.red
                                             : c.state == "warn" ? .orange : .green)
                            .padding(.top, 1)
                        VStack(alignment: .leading, spacing: 0) {
                            Text(c.name).font(.system(size: 10, weight: .medium))
                            Text(c.detail).font(.system(size: 10)).foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                            if let fix = c.fix, !fix.isEmpty, c.state != "ok" {
                                Text(fix).font(.system(size: 9)).foregroundStyle(.tertiary)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        }
                    }
                }
            }
        }
    }
}
