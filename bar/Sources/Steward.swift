import SwiftUI

/// stewardBinary is the first steward executable found. It is optional in a way
/// pitwall's own binary is not: steward is a separate product and most people
/// running this panel will not have it.
let stewardBinary: String? = {
    var candidates: [String] = []
    if let home = ProcessInfo.processInfo.environment["HOME"] {
        candidates += ["\(home)/go/bin/steward", "\(home)/.local/bin/steward",
                       "\(home)/Desktop/starts/steward/bin/steward"]
    }
    candidates += ["/opt/homebrew/bin/steward", "/usr/local/bin/steward"]
    return candidates.first { FileManager.default.isExecutableFile(atPath: $0) }
}()

struct StewardDecision: Decodable, Equatable, Identifiable {
    let at: Date
    let tool: String
    let subject: String
    let decision: String
    let reason: String
    let rule: String?

    var id: String { "\(at.timeIntervalSince1970)-\(subject)" }

    enum CodingKeys: String, CodingKey { case at, tool, subject, decision, reason, rule }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        if let raw = try? c.decode(String.self, forKey: .at) {
            at = ISO8601DateFormatter.flexible.date(from: raw) ?? Date(timeIntervalSince1970: 0)
        } else {
            at = Date(timeIntervalSince1970: 0)
        }
        tool = (try? c.decode(String.self, forKey: .tool)) ?? ""
        subject = (try? c.decode(String.self, forKey: .subject)) ?? ""
        decision = (try? c.decode(String.self, forKey: .decision)) ?? "defer"
        reason = (try? c.decode(String.self, forKey: .reason)) ?? ""
        rule = try? c.decode(String.self, forKey: .rule)
    }
}

struct StewardStatus: Decodable, Equatable {
    enum CodingKeys: String, CodingKey {
        case installed, settings, counts, total, recent
        case autoAllow = "auto_allow"
    }
    let installed: Bool
    let settings: String
    let autoAllow: Bool
    let counts: [String: Int]
    let total: Int
    let recent: [StewardDecision]?

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        installed = (try? c.decode(Bool.self, forKey: .installed)) ?? false
        settings = (try? c.decode(String.self, forKey: .settings)) ?? ""
        autoAllow = (try? c.decode(Bool.self, forKey: .autoAllow)) ?? false
        counts = (try? c.decode([String: Int].self, forKey: .counts)) ?? [:]
        total = (try? c.decode(Int.self, forKey: .total)) ?? 0
        recent = try? c.decode([StewardDecision].self, forKey: .recent)
    }
}

/// StewardLoader reads steward's status and flips its two switches. Both
/// switches are consequential — one puts a hook in the tool call path, the
/// other lets it answer for you — so each action re-reads the status
/// afterwards rather than assuming it worked.
@MainActor
final class StewardLoader: ObservableObject {
    @Published private(set) var status: StewardStatus?
    @Published private(set) var missing = stewardBinary == nil
    @Published private(set) var working = false
    @Published private(set) var error: String?

    func loadIfNeeded() {
        guard status == nil, !working, !missing else { return }
        reload()
    }

    func reload() { run([ "status", "--json" ]) }

    func setInstalled(_ on: Bool) { run(on ? ["install"] : ["install", "--remove"]) }

    func setAutoAllow(_ on: Bool) {
        run(["install", on ? "--auto-allow" : "--no-auto-allow"])
    }

    private func run(_ args: [String]) {
        guard let bin = stewardBinary else { missing = true; return }
        working = true
        error = nil
        Task.detached(priority: .userInitiated) {
            let result = runSteward(bin, args)
            await MainActor.run {
                self.working = false
                switch result {
                case .success(let s):
                    if let s { self.status = s } else { self.reload() }
                case .failure(let e):
                    self.error = e.message
                }
            }
        }
    }
}

/// runSteward returns a status when the command produced one, and nil when it
/// only performed an action — the caller then re-reads.
func runSteward(_ path: String, _ args: [String]) -> Result<StewardStatus?, LoadError> {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: path)
    process.arguments = args
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
    guard args.first == "status" else { return .success(nil) }
    do {
        return .success(try JSONDecoder().decode(StewardStatus.self, from: data))
    } catch {
        return .failure(LoadError(message: "could not read steward's status"))
    }
}

// MARK: - View

/// StewardSection is the enforcement half of the Rules tab: what is being
/// asked, what steward decided, and the two switches that change its behaviour.
struct StewardSection: View {
    @ObservedObject var steward: StewardLoader
    @Environment(\.uiLanguage) private var lang

    var body: some View {
        Block(title: "Enforcement") {
            if steward.missing {
                notInstalled
            } else if let s = steward.status {
                VStack(alignment: .leading, spacing: 8) {
                    switches(s)
                    if let recent = s.recent, !recent.isEmpty {
                        activity(s, recent)
                    } else if s.installed {
                        Text(lang == .ru
                             ? "Пока ничего не спрашивали. Записи появятся, когда Claude Code задаст вопрос о вызове инструмента."
                             : "Nothing asked yet. Entries appear when Claude Code asks about a tool call.")
                            .font(.system(size: 10)).foregroundStyle(.tertiary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    if let e = steward.error {
                        Text(e).font(.system(size: 10)).foregroundStyle(.orange)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            } else if steward.working {
                ProgressView().controlSize(.small)
            }
        }
    }

    private var notInstalled: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(lang == .ru
                 ? "steward не установлен. Он решает, что агенту можно выполнять, и записывает каждое решение."
                 : "steward is not installed. It decides what your agent may execute, and records every decision.")
                .font(.system(size: 11)).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Text("brew install sur1cat/tap/steward")
                .font(.system(size: 10, design: .monospaced)).foregroundStyle(.tertiary)
                .textSelection(.enabled)
        }
    }

    private func switches(_ s: StewardStatus) -> some View {
        VStack(alignment: .leading, spacing: 7) {
            Toggle(isOn: Binding(
                get: { s.installed },
                set: { steward.setInstalled($0) }
            )) {
                VStack(alignment: .leading, spacing: 1) {
                    Text(lang == .ru ? "Следить за запросами прав" : "Watch permission requests")
                        .font(.system(size: 12))
                    Text(lang == .ru
                         ? "Хук в ~/.claude/settings.json. Ничего не меняет — только записывает."
                         : "A hook in ~/.claude/settings.json. Changes nothing — it only records.")
                        .font(.system(size: 10)).foregroundStyle(.tertiary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .toggleStyle(.switch).controlSize(.small).disabled(steward.working)

            Toggle(isOn: Binding(
                get: { s.autoAllow },
                set: { steward.setAutoAllow($0) }
            )) {
                VStack(alignment: .leading, spacing: 1) {
                    Text(lang == .ru ? "Отвечать за меня" : "Answer for me")
                        .font(.system(size: 12))
                    Text(lang == .ru
                         ? "Одобрять вызов, который твои правила уже покрывают. Запрет отменить нельзя, составная команда одобряется только целиком."
                         : "Approve a call your own rules already cover. It cannot override a deny rule, and a compound command is approved only if every part is covered.")
                        .font(.system(size: 10)).foregroundStyle(.tertiary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .toggleStyle(.switch).controlSize(.small)
            .disabled(steward.working || !s.installed)
        }
    }

    private func activity(_ s: StewardStatus, _ recent: [StewardDecision]) -> some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 10) {
                tally(s.counts["allow"] ?? 0, lang == .ru ? "одобрено" : "allowed", .green)
                tally(s.counts["deny"] ?? 0, lang == .ru ? "отказано" : "denied", .red)
                tally(s.counts["defer"] ?? 0, lang == .ru ? "спрошено" : "asked", .secondary)
                Spacer(minLength: 0)
                Text(lang == .ru ? "за сутки" : "in 24h")
                    .font(.system(size: 10)).foregroundStyle(.tertiary)
            }
            VStack(spacing: 2) {
                ForEach(recent.suffix(6).reversed()) { d in
                    HStack(alignment: .top, spacing: 6) {
                        Circle().fill(tint(d.decision)).frame(width: 5, height: 5).padding(.top, 4)
                        VStack(alignment: .leading, spacing: 0) {
                            Text(d.subject).font(.system(size: 11, design: .monospaced))
                                .lineLimit(1).truncationMode(.middle)
                            if let r = d.rule, !r.isEmpty {
                                Text(r).font(.system(size: 9)).foregroundStyle(.tertiary).lineLimit(1)
                            }
                        }
                        Spacer(minLength: 0)
                        Text(d.at, format: .dateTime.hour().minute())
                            .font(.system(size: 9)).foregroundStyle(.tertiary).monospacedDigit()
                    }
                }
            }
        }
    }

    private func tally(_ n: Int, _ label: String, _ colour: Color) -> some View {
        HStack(spacing: 3) {
            Text("\(n)").font(.system(size: 12, weight: .medium)).monospacedDigit()
                .foregroundStyle(n > 0 ? colour : .secondary)
            Text(label).font(.system(size: 10)).foregroundStyle(.tertiary)
        }
    }

    private func tint(_ decision: String) -> Color {
        switch decision {
        case "allow": return .green
        case "deny":  return .red
        default:      return .secondary
        }
    }
}
