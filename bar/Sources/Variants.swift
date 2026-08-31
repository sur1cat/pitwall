import SwiftUI

/// BarStyle is what pitwall puts in the menu bar itself. The panel behind the
/// click is the same for all of them; only the always-visible part changes.
enum BarStyle: String, CaseIterable, Identifiable {
    case signal
    case context
    case plan
    case spend
    case burn
    case fleet
    case full

    var id: String { rawValue }

    func title(_ lang: Lang) -> String { L(rawTitle, lang) }

    var rawTitle: String {
        switch self {
        case .signal: return "Signal"
        case .context: return "Context left"
        case .plan:   return "Plan left"
        case .spend:  return "Spend"
        case .burn:   return "Burn rate"
        case .fleet:  return "Fleet"
        case .full:   return "Everything"
        }
    }

    func blurb(_ lang: Lang) -> String { L(rawBlurb, lang) }

    var rawBlurb: String {
        switch self {
        case .signal: return "One dot. Amber when an agent needs you."
        case .context: return "How full the fullest context window is."
        case .plan:   return "How much of your plan is used, with a bar."
        case .spend:  return "Today's spend, and the last five hours."
        case .burn:   return "Dollars per hour, with a meter."
        case .fleet:  return "How many agents are waiting, done, working."
        case .full:   return "Who needs you, today's spend, and a meter."
        }
    }

    /// A fixed sample so the picker can preview every style at once.
    static let sample = Snapshot(
        agents: [
            Agent(name: "mds-0e", sessionId: "a", pid: 0, project: "mds", branch: "main",
                  state: "WAITING", idle: 2_460_000_000_000, turnCost: 3.10,
                  question: "Drop the legacy index before backfill?", lastText: nil, pendingTool: nil,
                  context: 0.42, contextTokens: 420_000),
            Agent(name: "fleety-c8", sessionId: "b", pid: 0, project: "fleety", branch: "master",
                  state: "DONE", idle: 3_720_000_000_000, turnCost: 12.40, question: nil,
                  lastText: "Shipped to prod, tests green", pendingTool: nil,
                  context: 0.88, contextTokens: 880_000),
            Agent(name: "starts-0a", sessionId: "c", pid: 0, project: "starts", branch: nil,
                  state: "WORKING", idle: 4_000_000_000, turnCost: 0.9, question: nil, lastText: nil, pendingTool: nil,
                  context: 0.16, contextTokens: 160_000),
        ],
        waiting: 2,
        today: Money(total: 264.89),
        window5h: Money(total: 264.89),
        worktrees: Worktrees(worktrees: 44, byState: ["DEAD": 30, "PRIMARY": 13, "STRANDED": 1],
                             bytes: 2_684_354_560),
        stranded: 1,
        todaySubagent: Money(total: 60.11),
        window5hSubagent: Money(total: 60.11),
        retention: Retention(days: 31, limit: 30, configured: false))
}

/// StateTint is the colour each agent state gets, everywhere it appears.
enum StateTint {
    static func of(_ state: String) -> Color {
        switch state {
        case "WAITING": return .orange
        case "DONE":    return .green
        case "WORKING": return .cyan
        case "STALE":   return .secondary
        default:        return .secondary
        }
    }

    static func glyph(_ state: String) -> String {
        switch state {
        case "WAITING": return "exclamationmark.triangle.fill"
        case "DONE":    return "checkmark.circle.fill"
        case "WORKING": return "arrow.triangle.2.circlepath"
        case "STALE":   return "xmark.circle"
        default:        return "circle"
        }
    }
}

/// BarLabel is the always-visible menu bar item.
///
/// The strip will not draw a SwiftUI view the way the panel does: a label must
/// resolve to a single Text or Image, anything past the first child of a
/// container is dropped, colour is stripped from a template image, and an SF
/// Symbol interpolated into a Text does not come through. All of that was
/// established by screenshotting the real menu bar.
///
/// The way around every one of those at once is to stop handing the strip a
/// view and hand it a picture instead: draw the rich layout with ImageRenderer,
/// and mark the result as not-a-template so its colours survive. One Image goes
/// to the strip, and whatever was drawn into it arrives intact.
struct BarLabel: View {
    let snapshot: Snapshot
    let style: BarStyle
    var quota: QuotaUsage? = nil

    /// barHeight is the drawable height of a macOS menu bar item.
    private static let barHeight: CGFloat = 16

    var body: some View {
        if let image = rendered {
            Image(nsImage: image)
        } else {
            // If rasterising ever fails, a legible fallback beats a blank slot.
            Text(fallbackLine).monospacedDigit()
        }
    }

    /// rendered rasterises the content at the screen's scale. The menu bar
    /// follows the system appearance rather than the app's, so the colour
    /// scheme is taken from the effective appearance before drawing.
    @MainActor private var rendered: NSImage? {
        // NSApp is nil before the application object exists, so ask the shared
        // instance, which creates it on demand.
        let appearance = NSApplication.shared.effectiveAppearance
        let dark = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
        let renderer = ImageRenderer(
            content: content
                .frame(height: Self.barHeight)
                .environment(\.colorScheme, dark ? .dark : .light)
        )
        renderer.scale = NSScreen.main?.backingScaleFactor ?? 2
        guard let cg = renderer.cgImage else { return nil }
        let image = NSImage(cgImage: cg,
                            size: NSSize(width: CGFloat(cg.width) / renderer.scale,
                                         height: CGFloat(cg.height) / renderer.scale))
        // A template image is recoloured by the system; this one keeps its own.
        image.isTemplate = false
        return image
    }

    /// content is drawn freely — stacks, colours, shapes and symbols all work,
    /// because what reaches the strip is a bitmap of it.
    @ViewBuilder private var content: some View {
        switch style {
        case .signal:
            Image(systemName: signalGlyph)
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(signalTint)
        case .context:
            HStack(spacing: 4) {
                Meter(fraction: fullestContext, tint: contextTint, segments: 5)
                Text(contextLabel).font(barFont).foregroundStyle(contextTint)
            }
        case .plan:
            HStack(spacing: 4) {
                Meter(fraction: planFraction, tint: planTint, segments: 4)
                Text(planLine).font(barFont).foregroundStyle(planTint)
            }
        case .spend:
            HStack(spacing: 4) {
                Image(systemName: "dollarsign.circle.fill")
                    .font(.system(size: 11)).foregroundStyle(.secondary)
                Text("\(Format.money(snapshot.today.total)) · 5h \(Format.money(snapshot.window5h.total))")
                    .font(barFont)
            }
        case .burn:
            HStack(spacing: 4) {
                Text("\(Format.money(snapshot.burnRate))/h").font(barFont)
                Meter(fraction: meterFraction, tint: meterTint, segments: 5)
            }
        case .fleet:
            HStack(spacing: 6) {
                ForEach(["WAITING", "DONE", "WORKING"], id: \.self) { state in
                    let n = snapshot.counts[state] ?? 0
                    if n > 0 {
                        HStack(spacing: 2) {
                            Image(systemName: StateTint.glyph(state)).font(.system(size: 10))
                            Text("\(n)").font(barFont)
                        }
                        .foregroundStyle(StateTint.of(state))
                    }
                }
                if snapshot.counts.values.allSatisfy({ $0 == 0 }) || snapshot.agents.isEmpty {
                    Image(systemName: "moon.zzz").font(.system(size: 11)).foregroundStyle(.secondary)
                }
            }
        case .full:
            HStack(spacing: 5) {
                if snapshot.waiting > 0 {
                    HStack(spacing: 2) {
                        Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 10))
                        Text("\(snapshot.waiting)").font(barFont)
                    }
                    .foregroundStyle(.orange)
                }
                Text(Format.money(snapshot.today.total)).font(barFont)
                Meter(fraction: meterFraction, tint: meterTint, segments: 4)
            }
        }
    }

    private var barFont: Font { .system(size: 12, weight: .medium).monospacedDigit() }

    /// fullestContext is the most-used context window across running agents —
    /// the one that will be compacted first.
    private var fullestContext: Double {
        snapshot.agents.compactMap(\.context).max() ?? 0
    }

    private var contextLabel: String {
        fullestContext > 0 ? "\(Int(fullestContext * 100))%" : "—"
    }

    private var contextTint: Color {
        fullestContext >= 0.85 ? .red : fullestContext >= 0.6 ? .orange : .green
    }

    private var planLine: String {
        guard let q = quota else { return "plan" }
        let t = q.tightest
        return "\(t.label) \(Int(t.window.utilization))%"
    }

    private var planFraction: Double {
        min(max((quota?.tightest.window.utilization ?? 0) / 100, 0), 1)
    }

    private var planTint: Color {
        let u = quota?.tightest.window.utilization ?? 0
        return u >= 90 ? .red : u >= 70 ? .orange : .green
    }

    /// fallbackLine is plain text, used only if rasterising fails.
    private var fallbackLine: String {
        switch style {
        case .signal, .fleet: return "\(snapshot.agents.count) agents"
        case .plan:           return planLine
        case .burn:           return "\(Format.money(snapshot.burnRate))/h"
        default:              return Format.money(snapshot.today.total)
        }
    }

    private var signalGlyph: String {
        if snapshot.waiting > 0 { return "largecircle.fill.circle" }
        if snapshot.counts["WORKING", default: 0] > 0 { return "circle.dotted" }
        return "circle"
    }

    private var signalTint: Color {
        if (snapshot.counts["WAITING"] ?? 0) > 0 { return .orange }
        if (snapshot.counts["DONE"] ?? 0) > 0 { return .green }
        if (snapshot.counts["WORKING"] ?? 0) > 0 { return .cyan }
        return .secondary
    }

    /// The meter fills against PITWALL_LIMIT when it is set, and against a
    /// $100/h reference otherwise — a scale, never a claim about your plan's
    /// limits, which pitwall does not know.
    private var meterFraction: Double {
        let reference = Double(ProcessInfo.processInfo.environment["PITWALL_LIMIT"] ?? "") ?? 100
        return min(max(snapshot.burnRate / max(reference, 1), 0), 1)
    }

    private var meterTint: Color {
        if meterFraction >= 0.9 { return .red }
        if meterFraction >= 0.65 { return .orange }
        return .green
    }
}

/// Meter is a small segmented bar, drawn rather than typed so it cannot fall
/// back to a missing glyph.
struct Meter: View {
    let fraction: Double
    let tint: Color
    var segments: Int = 5

    var body: some View {
        let filled = Int((fraction * Double(segments)).rounded())
        HStack(spacing: 1.5) {
            ForEach(0..<segments, id: \.self) { i in
                RoundedRectangle(cornerRadius: 1)
                    .fill(i < filled ? tint : tint.opacity(0.25))
                    .frame(width: 3, height: 10)
            }
        }
    }
}
