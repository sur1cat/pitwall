import SwiftUI

struct QuotaWindow: Decodable, Equatable {
    let utilization: Double
    let resetsAt: Date?

    enum CodingKeys: String, CodingKey {
        case utilization
        case resetsAt = "resets_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        utilization = (try? c.decode(Double.self, forKey: .utilization)) ?? 0
        if let raw = try? c.decode(String.self, forKey: .resetsAt) {
            resetsAt = ISO8601DateFormatter.flexible.date(from: raw)
        } else {
            resetsAt = nil
        }
    }

    init(utilization: Double, resetsAt: Date?) {
        self.utilization = utilization
        self.resetsAt = resetsAt
    }

    var until: TimeInterval { max(resetsAt?.timeIntervalSinceNow ?? 0, 0) }

    /// exhaustedIn projects when this window hits 100% at a given pace, and
    /// stays silent when the window resets first.
    func exhaustedIn(_ pace: QuotaPace) -> TimeInterval? {
        guard pace.ok, pace.perHour > 0, utilization < 100 else { return nil }
        let seconds = (100 - utilization) / pace.perHour * 3600
        if until > 0 && seconds > until { return nil }
        return seconds
    }

    /// average is percentage points per hour since a window of known length
    /// opened. The weekly window resets as a whole, so how much of it has
    /// elapsed is arithmetic — and a rate drawn from days of usage beats one
    /// drawn from a handful of readings taken minutes apart.
    func average(length: TimeInterval) -> QuotaPace? {
        guard let reset = resetsAt else { return nil }
        let elapsed = reset.addingTimeInterval(-length).distance(to: Date())
        guard elapsed >= 3600, elapsed <= length else { return nil }
        return QuotaPace(perHour: utilization / (elapsed / 3600), span: elapsed * 1e9, ok: true)
    }
}

/// QuotaPace is how fast a window is filling, measured by pitwall from its own
/// consecutive readings.
struct QuotaPace: Decodable, Equatable {
    let perHour: Double
    let span: Double   // nanoseconds, as Go encodes a duration
    let ok: Bool

    enum CodingKeys: String, CodingKey {
        case perHour = "per_hour"
        case span, ok
    }

    static let unknown = QuotaPace(perHour: 0, span: 0, ok: false)

    /// The seven-day window's length, from its name and Anthropic's docs.
    static let week: TimeInterval = 7 * 24 * 3600

    /// trustworthy reports whether the reading rests on enough movement to
    /// extrapolate. Utilization arrives in whole percentage points, so a rise
    /// of two points is two ± one after rounding — enough to swing a projection
    /// from twelve hours to thirty-six.
    var trustworthy: Bool {
        ok && span > 0 && perHour * (span / 1e9 / 3600) >= 5
    }

    init(perHour: Double, span: Double, ok: Bool) {
        self.perHour = perHour
        self.span = span
        self.ok = ok
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        perHour = (try? c.decode(Double.self, forKey: .perHour)) ?? 0
        span = (try? c.decode(Double.self, forKey: .span)) ?? 0
        ok = (try? c.decode(Bool.self, forKey: .ok)) ?? false
    }

    var spanSeconds: TimeInterval { span / 1_000_000_000 }
}

struct QuotaUsage: Decodable, Equatable {
    let fiveHour: QuotaWindow
    let sevenDay: QuotaWindow
    let fiveHourPace: QuotaPace
    let sevenDayPace: QuotaPace
    let cached: Bool?

    enum CodingKeys: String, CodingKey {
        case fiveHour = "five_hour"
        case sevenDay = "seven_day"
        case fiveHourPace = "five_hour_pace"
        case sevenDayPace = "seven_day_pace"
        case cached
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        fiveHour = try c.decode(QuotaWindow.self, forKey: .fiveHour)
        sevenDay = try c.decode(QuotaWindow.self, forKey: .sevenDay)
        fiveHourPace = (try? c.decode(QuotaPace.self, forKey: .fiveHourPace)) ?? .unknown
        sevenDayPace = (try? c.decode(QuotaPace.self, forKey: .sevenDayPace)) ?? .unknown
        cached = try? c.decode(Bool.self, forKey: .cached)
    }

    /// tightest is the window closest to its limit — the one worth showing.
    var tightest: (label: String, window: QuotaWindow, pace: QuotaPace) {
        sevenDay.utilization > fiveHour.utilization
            ? ("7d", sevenDay, sevenDay.average(length: QuotaPace.week) ?? sevenDayPace)
            : ("5h", fiveHour, fiveHourPace)
    }
}

extension ISO8601DateFormatter {
    static let flexible: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()
}

/// QuotaLoader reads Anthropic's own view of the plan. It polls slowly: the
/// usage endpoint rate limits hard, and pitwall caches for three minutes.
@MainActor
final class QuotaLoader: ObservableObject {
    @Published private(set) var usage: QuotaUsage?
    @Published private(set) var error: String?

    private var timer: Timer?
    private var announced: Set<String> = []
    var notificationsEnabled = false

    func start() {
        refresh()
        timer = Timer.scheduledTimer(withTimeInterval: 180, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refresh() }
        }
    }

    func refresh() {
        let bin = pitwallBinary
        Task.detached(priority: .utility) {
            let p = Process()
            p.executableURL = URL(fileURLWithPath: bin)
            p.arguments = ["quota", "--json"]
            let out = Pipe(), err = Pipe()
            p.standardOutput = out
            p.standardError = err
            guard (try? p.run()) != nil else { return }
            let data = out.fileHandleForReading.readDataToEndOfFile()
            let errData = err.fileHandleForReading.readDataToEndOfFile()
            p.waitUntilExit()
            let decoded = try? JSONDecoder().decode(QuotaUsage.self, from: data)
            let message = String(data: errData, encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines)
            await MainActor.run {
                if let decoded {
                    self.usage = decoded
                    self.error = nil
                    self.announce(decoded)
                } else if let message, !message.isEmpty {
                    self.error = message
                }
            }
        }
    }

    /// announce warns once per window per threshold crossing, and forgets the
    /// warning when the window drops back.
    private func announce(_ u: QuotaUsage) {
        guard notificationsEnabled else { return }
        for (label, window) in [("5-hour", u.fiveHour), ("rolling", u.sevenDay)] {
            let key = label
            if window.utilization >= 85 {
                if !announced.contains(key) {
                    announced.insert(key)
                    Notifier.shared.post(
                        title: "\(Int(window.utilization))% of your \(label) limit used",
                        body: "resets in " + Format.duration(seconds: window.until))
                }
            } else if window.utilization < 75 {
                announced.remove(key)
            }
        }
    }
}

/// QuotaBlock is the panel's view of the plan: both windows, and a warning
/// when the current pace runs one out before it resets.
struct QuotaBlock: View {
    let usage: QuotaUsage
    @Environment(\.uiLanguage) private var lang

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            row(L("5 hours", lang), usage.fiveHour, usage.fiveHourPace)
            // The weekly window's own elapsed time is a far longer baseline
            // than pitwall's readings, so it is preferred when available.
            row(L("7 days", lang), usage.sevenDay,
                usage.sevenDay.average(length: QuotaPace.week) ?? usage.sevenDayPace)
        }
    }

    private func row(_ label: String, _ w: QuotaWindow, _ pace: QuotaPace) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 6) {
                Text(label).font(.system(size: 11)).foregroundStyle(.secondary)
                    .frame(width: 52, alignment: .leading)
                GeometryReader { geo in
                    ZStack(alignment: .leading) {
                        Capsule().fill(Color.primary.opacity(0.10))
                        Capsule().fill(tint(w.utilization))
                            .frame(width: max(geo.size.width * min(w.utilization, 100) / 100, 2))
                    }
                }
                .frame(height: 7)
                Text("\(Int(w.utilization))%")
                    .font(.system(size: 11, weight: .medium)).monospacedDigit()
                    // A window that will fill before it resets is not "fine"
                    // however low the number looks, so the projection decides
                    // the colour rather than the percentage alone.
                    .foregroundStyle(w.exhaustedIn(pace) != nil && pace.trustworthy
                                     ? Color.orange : tint(w.utilization))
                    .frame(width: 34, alignment: .trailing)
            }
            HStack(spacing: 6) {
                Spacer().frame(width: 52)
                if !pace.ok {
                    Text(lang == .ru ? "измеряю темп" : "measuring the pace")
                        .font(.system(size: 10)).foregroundStyle(.tertiary)
                } else if !pace.trustworthy {
                    Text(lang == .ru ? "движения пока мало для прогноза" : "too little movement to project")
                        .font(.system(size: 10)).foregroundStyle(.tertiary)
                } else if let out = w.exhaustedIn(pace) {
                    Text((lang == .ru ? "заполнится через " : "full in ")
                         + Format.duration(seconds: out >= 6 * 3600 ? (out / 3600).rounded() * 3600 : out))
                        .font(.system(size: 10)).foregroundStyle(.orange)
                    Text((lang == .ru ? "по темпу за " : "at your rate over ")
                         + Format.duration(seconds: pace.spanSeconds))
                        .font(.system(size: 10)).foregroundStyle(.tertiary)
                }
                Text((lang == .ru ? "сброс через " : "resets in ")
                     + Format.duration(seconds: w.until))
                    .font(.system(size: 10)).foregroundStyle(.tertiary)
            }
        }
    }

    private func tint(_ v: Double) -> Color {
        if v >= 90 { return .red }
        if v >= 70 { return .orange }
        return .green
    }
}
