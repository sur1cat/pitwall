import Foundation

// Agent mirrors one entry of `pitwall --json`.
struct Agent: Decodable, Identifiable, Equatable {
    enum CodingKeys: String, CodingKey {
        case name, project, branch, state, idle, question, pid
        case sessionId = "session_id"
        case turnCost = "turn_cost"
        case context
        case contextTokens = "context_tokens"
        case lastText = "last_text"
        case pendingTool = "pending_tool"
    }

    var id: String { sessionId }
    let name: String
    let sessionId: String
    let pid: Int
    let project: String
    let branch: String?
    let state: String
    let idle: Int64
    let turnCost: Double          // nanoseconds
    let question: String?
    let lastText: String?
    let pendingTool: String?
    /// How full this session's context window is, 0 to 1. Optional because a
    /// model pitwall does not know the window size of gets no reading at all,
    /// and an invented bar would be acted on.
    let context: Double?
    let contextTokens: Int64?

    var needsYou: Bool { state == "WAITING" || state == "DONE" }

    /// A short line describing why this agent is on screen.
    var detail: String {
        switch state {
        case "WAITING":
            if let q = question, !q.isEmpty { return q }
            if let p = pendingTool, !p.isEmpty { return "blocked on \(p)" }
            return "waiting for you"
        case "WORKING":
            return "running"
        default:
            return Agent.plain(lastText ?? "")
        }
    }

    /// plain flattens an assistant message into one readable line. The raw text
    /// is markdown and often ends in a fenced block of git output, which turned
    /// the summary row into "``` 25b7f73 ← main = origin/main". Headings,
    /// fences, list bullets and emphasis are stripped rather than rendered,
    /// because a single truncated line has no room to render them anyway.
    static func plain(_ raw: String) -> String {
        var lines: [String] = []
        var inFence = false
        for rawLine in raw.split(separator: "\n", omittingEmptySubsequences: false) {
            var line = rawLine.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("```") || line.hasPrefix("~~~") {
                inFence.toggle()
                continue
            }
            if inFence || line.isEmpty { continue }
            while line.hasPrefix("#") { line.removeFirst() }
            if line.hasPrefix("- ") || line.hasPrefix("* ") { line.removeFirst(2) }
            if line.hasPrefix("> ") { line.removeFirst(2) }
            line = line.trimmingCharacters(in: .whitespaces)
            line = line.replacingOccurrences(of: "**", with: "")
            line = line.replacingOccurrences(of: "`", with: "")
            if !line.isEmpty { lines.append(line) }
        }
        let joined = lines.joined(separator: " · ")
        return joined.isEmpty ? raw.replacingOccurrences(of: "\n", with: " ") : joined
    }

    var idleText: String { Format.duration(seconds: Double(idle) / 1_000_000_000) }
}

struct Money: Decodable, Equatable {
    let total: Double
}

struct Worktrees: Decodable, Equatable {
    enum CodingKeys: String, CodingKey {
        case worktrees, bytes
        case byState = "by_state"
    }

    let worktrees: Int
    let byState: [String: Int]?
    let bytes: Int64

    var removable: Int { (byState?["DEAD"] ?? 0) + (byState?["ORPHAN"] ?? 0) }
}

/// Retention is how far back transcripts still reach. Claude Code deletes them
/// past cleanupPeriodDays, so the coach's conclusions narrow silently over time.
struct Retention: Decodable, Equatable {
    let days: Double
    let limit: Int
    let configured: Bool
}

struct Snapshot: Decodable, Equatable {
    enum CodingKeys: String, CodingKey {
        case agents, waiting, today, worktrees, stranded
        case window5h = "window_5h"
        case todaySubagent = "today_subagent"
        case window5hSubagent = "window_5h_subagent"
        case retention
    }

    let agents: [Agent]
    let waiting: Int
    let today: Money
    let window5h: Money
    let worktrees: Worktrees?
    let stranded: Int?
    /// Optional so an older pitwall binary still decodes — the whole snapshot
    /// failing over one missing key is a bug this panel has already had twice.
    let todaySubagent: Money?
    let window5hSubagent: Money?
    /// Present only while transcripts are actually being deleted.
    let retention: Retention?

    static let empty = Snapshot(agents: [], waiting: 0, today: Money(total: 0),
                                window5h: Money(total: 0), worktrees: nil, stranded: 0,
                                todaySubagent: nil, window5hSubagent: nil, retention: nil)

    /// strandedCount is the last known number, treating "not scanned" as zero.
    var strandedCount: Int { stranded ?? 0 }

    var counts: [String: Int] {
        agents.reduce(into: [:]) { $0[$1.state, default: 0] += 1 }
    }
    /// Dollars per hour over the trailing five-hour window.
    var burnRate: Double { window5h.total / 5 }

    /// Share of today's spend that went to subagents, or nil when there is
    /// nothing to divide by.
    var subagentShare: Double? {
        guard let sub = todaySubagent?.total, today.total > 0 else { return nil }
        return sub / today.total
    }
}

enum Format {
    static func money(_ v: Double) -> String {
        if v >= 1000 { return String(format: "$%.0f", v) }
        if v >= 100 { return String(format: "$%.0f", v) }
        return String(format: "$%.2f", v)
    }

    static func duration(seconds: Double) -> String {
        if seconds < 60 { return String(format: "%.0fs", max(seconds, 0)) }
        if seconds < 3600 { return String(format: "%.0fm", seconds / 60) }
        if seconds < 86400 { return String(format: "%.0fh", seconds / 3600) }
        return String(format: "%.0fd", seconds / 86400)
    }

    /// tokens renders a token count the way a person reads one.
    static func tokens(_ n: Int64) -> String {
        if n >= 1_000_000 { return String(format: "%.1fM", Double(n) / 1_000_000) }
        if n >= 1_000 { return String(format: "%.0fK", Double(n) / 1_000) }
        return "\(n)"
    }

    static func bytes(_ n: Int64) -> String {
        let units = ["B", "KB", "MB", "GB", "TB"]
        var v = Double(n), i = 0
        while v >= 1024, i < units.count - 1 { v /= 1024; i += 1 }
        return String(format: v >= 100 || i == 0 ? "%.0f %@" : "%.1f %@", v, units[i])
    }
}

/// pitwallBinary is the first pitwall executable found: bundled beside this
/// app, then the usual install locations, then whatever is on PATH.
let pitwallBinary: String = {
    var candidates = [Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/pitwall").path]
    if let home = ProcessInfo.processInfo.environment["HOME"] {
        candidates += ["\(home)/go/bin/pitwall", "\(home)/.local/bin/pitwall",
                       "\(home)/Desktop/starts/pitwall/bin/pitwall"]
    }
    candidates += ["/opt/homebrew/bin/pitwall", "/usr/local/bin/pitwall"]
    return candidates.first { FileManager.default.isExecutableFile(atPath: $0) } ?? "pitwall"
}()

/// runPitwall executes the binary and decodes one snapshot.
func runPitwall(_ path: String, _ args: [String]) -> Result<Snapshot, LoadError> {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: path)
    process.arguments = args
    let out = Pipe(), err = Pipe()
    process.standardOutput = out
    process.standardError = err
    do {
        try process.run()
    } catch {
        return .failure(LoadError(message: "cannot run \(path) — install it with: go install github.com/sur1cat/pitwall@latest"))
    }
    let data = out.fileHandleForReading.readDataToEndOfFile()
    let errData = err.fileHandleForReading.readDataToEndOfFile()
    process.waitUntilExit()
    guard process.terminationStatus == 0 else {
        let msg = String(data: errData, encoding: .utf8) ?? "exit \(process.terminationStatus)"
        return .failure(LoadError(message: msg.trimmingCharacters(in: .whitespacesAndNewlines)))
    }
    let decoder = JSONDecoder()
    do {
        return .success(try decoder.decode(Snapshot.self, from: data))
    } catch {
        return .failure(LoadError(message: "could not read pitwall output: \(error.localizedDescription)"))
    }
}

/// LoadError carries a human-readable reason the snapshot could not be read.
struct LoadError: Error { let message: String }

/// Loader runs the pitwall binary and decodes its JSON. The worktree scan is
/// far slower than the rest, so it runs on its own, much lazier schedule.
@MainActor
final class Loader: ObservableObject {
    @Published private(set) var snapshot: Snapshot = .empty
    @Published private(set) var updated: Date?
    @Published private(set) var error: String?
    /// loaded stays false until the first successful read, so the panel can
    /// say "reading" instead of showing a confident $0.00.
    @Published private(set) var loaded = false

    private var fastTimer: Timer?
    private var slowTimer: Timer?
    private var cachedTree: Worktrees?
    private var cachedStranded = 0
    private var lastStates: [String: String] = [:]
    /// Sessions already warned about their context, so the alert fires on the
    /// crossing rather than on every poll.
    private var contextWarned: Set<String> = []

    /// notificationsEnabled mirrors the setting; the loader owns the
    /// transition detection because it is the only thing that sees both the
    /// previous and the next snapshot.
    var notificationsEnabled = false


    func start() {
        refresh(includeTree: true)
        fastTimer = Timer.scheduledTimer(withTimeInterval: 8, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refresh(includeTree: false) }
        }
        slowTimer = Timer.scheduledTimer(withTimeInterval: 300, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refresh(includeTree: true) }
        }
    }

    func refresh(includeTree: Bool) {
        let bin = pitwallBinary
        Task.detached(priority: .utility) {
            var args = ["--json"]
            if !includeTree { args.append("--no-tree") }
            let result = runPitwall(bin, args)
            await MainActor.run { self.apply(result, includedTree: includeTree) }
        }
    }

    private func apply(_ result: Result<Snapshot, LoadError>, includedTree: Bool) {
        switch result {
        case .success(var snap):
            if includedTree {
                cachedTree = snap.worktrees
                cachedStranded = snap.strandedCount
            } else if snap.worktrees == nil {
                // A fast refresh skips the git scan: keep the last picture
                // rather than blanking the row.
                snap = Snapshot(agents: snap.agents, waiting: snap.waiting,
                                today: snap.today, window5h: snap.window5h,
                                worktrees: cachedTree, stranded: cachedStranded,
                                todaySubagent: snap.todaySubagent,
                                window5hSubagent: snap.window5hSubagent,
                                retention: snap.retention)
            }
            announce(snap)
            snapshot = snap
            updated = Date()
            error = nil
            loaded = true
        case .failure(let failure):
            error = failure.message
        }
    }


    /// announce posts one notification per agent that just started needing
    /// you. The first snapshot only records state, so launching pitwall never
    /// produces a burst of notifications about things you already know.
    /// contextAlert is the level at which a compaction becomes likely. Past it
    /// the conversation is about to be summarised and lose what it knew, and
    /// nothing else says so: on the corpus this was built against, 41 of 207
    /// sessions crossed it, and the 37 compactions that followed dropped a
    /// median of 986,000 tokens each.
    private static let contextAlert = 0.85

    private func announce(_ next: Snapshot) {
        let seeded = !lastStates.isEmpty
        var states: [String: String] = [:]
        var crossed: Set<String> = []
        for agent in next.agents {
            states[agent.sessionId] = agent.state

            // Warned once per session per crossing, not on every poll: an
            // alert that repeats every eight seconds is an alert people turn
            // off, and then it protects nothing.
            if let ctx = agent.context, ctx >= Loader.contextAlert {
                crossed.insert(agent.sessionId)
                if seeded, notificationsEnabled, !contextWarned.contains(agent.sessionId) {
                    Notifier.shared.post(
                        title: "\(agent.name) is at \(Int(ctx * 100))% context",
                        body: "a compaction is close — it will summarise and lose detail")
                }
            }

            guard seeded, notificationsEnabled else { continue }
            let before = lastStates[agent.sessionId]
            guard before != agent.state, agent.needsYou else { continue }
            switch agent.state {
            case "WAITING":
                Notifier.shared.post(title: "\(agent.name) needs you",
                                     body: agent.question ?? "waiting on a decision")
            case "DONE":
                Notifier.shared.post(title: "\(agent.name) finished",
                                     body: agent.detail.isEmpty ? agent.project : agent.detail)
            default:
                break
            }
        }
        lastStates = states
        // Dropping back below the line rearms the warning, so a session that
        // is compacted and fills up again is flagged a second time.
        contextWarned = crossed
    }
}
