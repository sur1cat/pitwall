import SwiftUI

@main
struct PitwallBarApp: App {
    @StateObject private var loader = Loader()
    @StateObject private var coach = CoachLoader()
    @StateObject private var perms = PermsLoader()
    @StateObject private var cleaner = TreeCleaner()
    @StateObject private var steward = StewardLoader()
    @StateObject private var health = HealthLoader()
    @StateObject private var effort = EffortLoader()
    @StateObject private var quota = QuotaLoader()
    @AppStorage("barStyle") private var styleRaw = BarStyle.full.rawValue
    @AppStorage("notifications") private var notifications = true
    @AppStorage("language") private var langRaw = Lang.en.rawValue

    private var style: BarStyle { BarStyle(rawValue: styleRaw) ?? .full }

    var body: some Scene {
        MenuBarExtra {
            PanelView(loader: loader, coach: coach, perms: perms, cleaner: cleaner, steward: steward, health: health, effort: effort, quota: quota,
                      selected: $styleRaw, notifications: $notifications,
                      language: $langRaw)
                .frame(width: 470)
                .environment(\.uiLanguage, Lang(rawValue: langRaw) ?? .en)
        } label: {
            BarLabel(snapshot: loader.snapshot, style: style, quota: quota.usage)
                .task {
                    loader.notificationsEnabled = notifications
                    quota.notificationsEnabled = notifications
                    if notifications { Notifier.shared.enable() }
                    loader.start()
                    quota.start()
                }
        }
        .menuBarExtraStyle(.window)
    }
}

enum Tab: String, CaseIterable, Identifiable {
    case status, perms, insights, settings
    var id: String { rawValue }
    func title(_ lang: Lang) -> String { L(rawTitle, lang) }
    var rawTitle: String {
        switch self {
        case .status: return "Status"
        case .perms: return "Rules"
        case .insights: return "Insights"
        case .settings: return "Setup"
        }
    }
}

struct PanelView: View {
    @ObservedObject var loader: Loader
    @ObservedObject var coach: CoachLoader
    @ObservedObject var perms: PermsLoader
    @ObservedObject var cleaner: TreeCleaner
    @ObservedObject var steward: StewardLoader
    @ObservedObject var health: HealthLoader
    @ObservedObject var effort: EffortLoader
    @ObservedObject var quota: QuotaLoader
    @Binding var selected: String
    @Binding var notifications: Bool
    @Binding var language: String
    @State private var tab: Tab = .status
    @State private var insights: InsightsSection = .habits
    @Environment(\.uiLanguage) private var lang

    /// The scrolling area grows with its content between a floor and the
    /// screen. A hard size made every tab as tall as the tallest, which left a
    /// short tab with a large empty space below it; a pure maximum let the
    /// panel collapse to whatever the current tab happened to need.
    static var availableHeight: CGFloat {
        let screen = NSScreen.main?.visibleFrame.height ?? 900
        return max(480, screen - 210)
    }

    /// minHeight keeps the panel substantial even on the shortest tab.
    static var minHeight: CGFloat { min(620, availableHeight) }

    private var snap: Snapshot { loader.snapshot }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()

            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    switch tab {
                    case .status:   StatusTab(loader: loader, quota: quota, cleaner: cleaner)
                    case .perms:    PermsTab(perms: perms, steward: steward)
                    case .insights: InsightsTab(coach: coach, effort: effort, section: $insights)
                    case .settings: SettingsTab(health: health, selected: $selected,
                                                notifications: $notifications,
                                                language: $language,
                                                snapshot: snap, loader: loader,
                                                quota: quota)
                    }
                }
            }
            .frame(minHeight: PanelView.minHeight, maxHeight: PanelView.availableHeight)

            Divider()
            footer
        }
        .onChange(of: tab) { _, newValue in
            if newValue == .perms { perms.loadIfNeeded(); steward.loadIfNeeded() }
            if newValue == .settings { health.loadIfNeeded() }
            if newValue == .insights {
                if insights == .habits { coach.loadIfNeeded() }
                if insights == .projects { effort.loadIfNeeded() }
            }
        }
    }

    private var header: some View {
        VStack(spacing: 8) {
            HStack(spacing: 6) {
                Text("pitwall").font(.system(size: 13, weight: .semibold))
                Spacer()
                if let u = loader.updated {
                    Text(u, format: .dateTime.hour().minute())
                        .font(.caption2).foregroundStyle(.tertiary).monospacedDigit()
                }
            }
            Picker("", selection: $tab) {
                ForEach(Tab.allCases) { Text($0.title(lang)).font(.system(size: 11)).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
        }
        .padding(.horizontal, 14).padding(.top, 10).padding(.bottom, 9)
    }

    private var footer: some View {
        HStack(spacing: 12) {
            switch tab {
            case .status:
                Button(L("Refresh", lang)) { loader.refresh(includeTree: true) }
            case .perms:
                Button(L("Rescan", lang)) { perms.reload() }
            case .insights:
                Button(L("Rescan", lang)) {
                    insights == .projects ? effort.reload() : coach.reload()
                }
            case .settings:
                Button(L("Rescan", lang)) { health.reload() }
            }
            Spacer()
            Button(L("Quit", lang)) { NSApplication.shared.terminate(nil) }
        }
        .buttonStyle(.borderless)
        .font(.system(size: 12))
        .padding(.horizontal, 14).padding(.vertical, 9)
    }
}

// MARK: - Shared pieces

/// SectionLabel is the small uppercase heading every block sits under.
struct SectionLabel: View {
    let text: String
    var body: some View {
        Text(text)
            .font(.system(size: 10, weight: .semibold))
            .textCase(.uppercase)
            .foregroundStyle(.tertiary)
            .tracking(0.5)
    }
}

struct Block<Content: View>: View {
    let title: String
    @Environment(\.uiLanguage) private var lang
    @ViewBuilder let content: () -> Content
    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            SectionLabel(text: L(title, lang))
            content()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 14).padding(.top, 10).padding(.bottom, 4)
    }
}

// MARK: - Status

struct StatusTab: View {
    @ObservedObject var loader: Loader
    @ObservedObject var quota: QuotaLoader
    @ObservedObject var cleaner: TreeCleaner
    @Environment(\.uiLanguage) private var lang
    private var snap: Snapshot { loader.snapshot }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let error = loader.error {
                errorBox(error)
            } else if !loader.loaded {
                HStack(spacing: 7) {
                    ProgressView().controlSize(.small)
                    Text(L("Reading your Claude Code data…", lang)).font(.callout).foregroundStyle(.secondary)
                }
                .padding(14)
            } else {
                if snap.agents.isEmpty {
                    Block(title: "Agents") {
                        Text(L("None running", lang)).font(.callout).foregroundStyle(.secondary)
                    }
                } else {
                    ForEach(AgentGroup.groups(from: snap.agents)) { group in
                        Block(title: group.title) {
                            VStack(alignment: .leading, spacing: 2) {
                                ForEach(group.agents) { AgentRow(agent: $0) }
                            }
                        }
                    }
                }
                if let q = quota.usage {
                    Block(title: "Plan left") { QuotaBlock(usage: q) }
                }
                Block(title: "Spent today") {
                    VStack(alignment: .leading, spacing: 1) {
                        Text(Format.money(snap.today.total))
                            .font(.system(size: 24, weight: .medium, design: .rounded))
                            .monospacedDigit()
                        Text(lang == .ru
                     ? "\(Format.money(snap.burnRate)) в час за последние 5 часов"
                     : "\(Format.money(snap.burnRate)) per hour over the last 5 hours")
                            .font(.system(size: 11)).foregroundStyle(.secondary)
                        if let share = snap.subagentShare, let sub = snap.todaySubagent, sub.total > 0 {
                            Text(lang == .ru
                     ? "из них \(Format.money(sub.total)) — субагенты (\(Int(share * 100))%)"
                     : "\(Format.money(sub.total)) of it went to subagents (\(Int(share * 100))%)")
                                .font(.system(size: 11)).foregroundStyle(.secondary)
                        }
                    }
                }
                if let w = snap.worktrees, w.worktrees > 0 {
                    Block(title: "Worktrees") { git(w) }
                }
                if let r = snap.retention {
                    HStack(alignment: .top, spacing: 6) {
                        Image(systemName: "clock.arrow.circlepath")
                            .font(.system(size: 10)).foregroundStyle(.orange)
                        Text(lang == .ru
                     ? "История ограничена \(Int(r.days)) днями — Claude Code удаляет транскрипты старше \(r.limit). Поднимите cleanupPeriodDays в ~/.claude/settings.json, иначе выводы Coach будут считаться по всё более узкому окну."
                     : "History reaches back only \(Int(r.days)) days — Claude Code deletes transcripts older than \(r.limit). Raise cleanupPeriodDays in ~/.claude/settings.json, or the Coach tab keeps narrowing its window.")
                            .font(.system(size: 10)).foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    .padding(.horizontal, 14)
                }
            }
            Spacer(minLength: 6)
        }
    }

    private func git(_ w: Worktrees) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 5) {
                Text("\(w.worktrees)").font(.system(size: 13, weight: .medium)).monospacedDigit()
                Text(L("checkouts", lang)).font(.system(size: 12)).foregroundStyle(.secondary)
                if w.removable > 0 {
                    Text("·").foregroundStyle(.tertiary)
                    Text("\(w.removable) " + L("removable", lang)).font(.system(size: 12)).foregroundStyle(.secondary)
                    if w.bytes > 0 {
                        Text(Format.bytes(w.bytes)).font(.system(size: 12)).foregroundStyle(.tertiary)
                    }
                }
            }
            if snap.strandedCount > 0 {
                Label(lang == .ru
                      ? "\(snap.strandedCount) держит незакоммиченную работу"
                      : "\(snap.strandedCount) holds work that was never committed",
                      systemImage: "exclamationmark.triangle.fill")
                    .font(.system(size: 11)).foregroundStyle(.orange)
            }
            WorktreeCleanup(cleaner: cleaner, removable: w.removable)
                .padding(.top, 2)
        }
    }

    private func errorBox(_ message: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Label(L("pitwall could not read your data", lang), systemImage: "exclamationmark.triangle.fill")
                .font(.system(size: 12, weight: .medium)).foregroundStyle(.orange)
            Text(message).font(.system(size: 11)).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Button(L("Try again", lang)) { loader.refresh(includeTree: true) }
                .buttonStyle(.borderless).font(.system(size: 11))
        }
        .padding(14)
    }
}

/// SubagentList shows the delegated runs going on under an agent: what each was
/// asked to do, how long it has been at it, and what it has spent. Nothing else
/// surfaces this — the panel showed one busy line while several agents worked
/// underneath, which on this machine is 23% of the spend.
struct SubagentList: View {
    let subs: [Sub]
    @Environment(\.uiLanguage) private var lang

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 5) {
                Image(systemName: "arrow.triangle.branch")
                    .font(.system(size: 9)).foregroundStyle(.cyan)
                Text(lang == .ru
                     ? "\(subs.count) в работе · \(subs.reduce(0) { $0 + $1.tools }) вызовов · \(Format.money(subs.reduce(0) { $0 + $1.cost }))"
                     : "\(subs.count) working · \(subs.reduce(0) { $0 + $1.tools }) tool calls · \(Format.money(subs.reduce(0) { $0 + $1.cost }))")
                    .font(.system(size: 10)).foregroundStyle(.cyan)
            }
            ForEach(subs.prefix(4)) { sub in
                HStack(alignment: .top, spacing: 5) {
                    Text(sub.elapsed)
                        .font(.system(size: 9)).monospacedDigit().foregroundStyle(.tertiary)
                        .frame(width: 34, alignment: .trailing)
                    Text(sub.task ?? sub.name)
                        .font(.system(size: 10)).foregroundStyle(.secondary)
                        .lineLimit(1).truncationMode(.tail)
                    Spacer(minLength: 0)
                    Text(Format.money(sub.cost))
                        .font(.system(size: 9)).monospacedDigit().foregroundStyle(.tertiary)
                }
            }
            if subs.count > 4 {
                Text(lang == .ru ? "и ещё \(subs.count - 4)" : "and \(subs.count - 4) more")
                    .font(.system(size: 9)).foregroundStyle(.tertiary).padding(.leading, 39)
            }
        }
        .padding(.top, 1)
    }
}

/// ContextBar shows how full a session's context window is. This is the
/// resource that runs out first and least visibly: money is recoverable and
/// quota resets on a schedule, but a conversation forced into a compaction
/// loses what it knew, and nothing in Claude Code warns before that happens.
struct ContextBar: View {
    let fraction: Double
    var tokens: Int64? = nil
    @Environment(\.uiLanguage) private var lang

    var body: some View {
        HStack(spacing: 6) {
            GeometryReader { geo in
                ZStack(alignment: .leading) {
                    Capsule().fill(Color.primary.opacity(0.10))
                    Capsule().fill(tint)
                        .frame(width: max(geo.size.width * min(fraction, 1), 2))
                }
            }
            .frame(height: 4)
            Text("\(Int(fraction * 100))%")
                .font(.system(size: 10)).monospacedDigit().foregroundStyle(tint)
                .frame(width: 30, alignment: .trailing)
        }
        .help(helpText)
    }

    private var tint: Color {
        fraction >= 0.85 ? .red : fraction >= 0.6 ? .orange : .secondary
    }

    private var helpText: String {
        let n = tokens.map { Format.tokens($0) } ?? "—"
        return lang == .ru
            ? "\(n) в контекстном окне. За 85% начинается сжатие, и разговор теряет то, что знал."
            : "\(n) in the context window. Past 85% a compaction is close, and the conversation loses what it knew."
    }
}

/// AgentRow is clickable: it brings that agent's terminal tab to the front.
struct AgentRow: View {
    let agent: Agent
    @State private var hovering = false

    var body: some View {
        Button { focusAgent(agent) } label: {
            HStack(alignment: .top, spacing: 7) {
                Image(systemName: StateTint.glyph(agent.state))
                    .font(.system(size: 11))
                    .foregroundStyle(StateTint.of(agent.state))
                    .frame(width: 13).padding(.top, 1)
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(agent.name).font(.system(size: 12, weight: .medium))
                        Text(agent.project).font(.system(size: 11)).foregroundStyle(.tertiary)
                        Spacer(minLength: 4)
                        if hovering {
                            Image(systemName: "arrow.up.forward.app")
                                .font(.system(size: 10)).foregroundStyle(.tertiary)
                        }
                        if agent.turnCost > 0.005 {
                            Text(Format.money(agent.turnCost))
                                .font(.system(size: 11, weight: .medium))
                                .foregroundStyle(.secondary).monospacedDigit()
                                .help("What this exchange has cost so far")
                        }
                        Text(agent.idleText)
                            .font(.system(size: 11)).foregroundStyle(.tertiary).monospacedDigit()
                    }
                    if let ctx = agent.context, ctx > 0 {
                        ContextBar(fraction: ctx, tokens: agent.contextTokens)
                    }
                    if let subs = agent.subs, !subs.isEmpty {
                        SubagentList(subs: subs)
                    }
                    if !agent.detail.isEmpty {
                        Text(agent.detail)
                            .font(.system(size: 11))
                            .foregroundStyle(agent.state == "WAITING" ? Color.orange : .secondary)
                            .lineLimit(2).fixedSize(horizontal: false, vertical: true)
                    }
                }
            }
            .padding(.vertical, 3).padding(.horizontal, 4)
            .background(hovering ? Color.primary.opacity(0.07) : .clear,
                        in: RoundedRectangle(cornerRadius: 5))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help("Bring \(agent.name)'s terminal tab to the front")
    }
}

// MARK: - Coach

struct CoachTab: View {
    @ObservedObject var coach: CoachLoader
    @State private var expanded: String?
    @Environment(\.uiLanguage) private var lang

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if coach.loading && coach.report == nil {
                VStack(alignment: .leading, spacing: 6) {
                    HStack(spacing: 7) {
                        ProgressView().controlSize(.small)
                        Text(L("Reading your transcripts…", lang)).font(.callout)
                    }
                    Text(L("The first pass reads everything Claude Code kept. Later ones are instant.", lang))
                        .font(.system(size: 11)).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(14)
            } else if let error = coach.error {
                Text(error).font(.system(size: 11)).foregroundStyle(.orange).padding(14)
            } else if let r = coach.report {
                headline(r)
                bar(r)
                if !r.findings.isEmpty {
                    VStack(alignment: .leading, spacing: 5) {
                        SectionLabel(text: L("Biggest leaks", lang))
                        VStack(spacing: 3) {
                            ForEach(r.findings) { finding in
                                LeakRow(finding: finding, total: r.spend,
                                        expanded: expanded == finding.id,
                                        toggle: {
                                            withAnimation(.easeInOut(duration: 0.12)) {
                                                expanded = expanded == finding.id ? nil : finding.id
                                            }
                                        },
                                        runAct: { coach.apply($0) },
                                        busy: coach.applying)
                            }
                        }
                    }
                    .padding(.horizontal, 14).padding(.top, 12)
                }
            }
            Spacer(minLength: 10)
        }
    }

    private func headline(_ r: CoachReport) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(Format.money(r.spend))
                .font(.system(size: 30, weight: .medium, design: .rounded)).monospacedDigit()
            Text(lang == .ru
                 ? "\(r.prompts) твоих промптов · \(CoachReport.day(r.from)) — \(CoachReport.day(r.to))"
                 : "\(r.prompts) prompts you typed · \(CoachReport.day(r.from)) to \(CoachReport.day(r.to))")
                .font(.system(size: 11)).foregroundStyle(.secondary)
        }
        .padding(.horizontal, 14).padding(.top, 12).padding(.bottom, 10)
    }

    /// bar shows, in one glance, how much of the money changed code.
    private func bar(_ r: CoachReport) -> some View {
        let parts: [(String, String, Color)] = [
            ("execute", L("changed code", lang), Color.green),
            ("investigate", L("looked around", lang), Color.cyan),
            ("talk", L("just talked", lang), Color.secondary),
        ]
        let values = parts.map { r.byClass[$0.0]?.spend ?? 0 }
        let total = max(values.reduce(0, +), 0.0001)
        return VStack(alignment: .leading, spacing: 6) {
            GeometryReader { geo in
                HStack(spacing: 2) {
                    ForEach(Array(parts.enumerated()), id: \.offset) { i, part in
                        RoundedRectangle(cornerRadius: 3)
                            .fill(part.2)
                            .frame(width: max(geo.size.width * values[i] / total - 2, 0))
                    }
                }
            }
            .frame(height: 10)
            HStack(spacing: 10) {
                ForEach(Array(parts.enumerated()), id: \.offset) { i, part in
                    HStack(spacing: 4) {
                        Circle().fill(part.2).frame(width: 6, height: 6)
                        Text(part.1).font(.system(size: 10)).foregroundStyle(.secondary)
                        Text(String(format: "%.0f%%", values[i] / total * 100))
                            .font(.system(size: 10, weight: .medium)).monospacedDigit()
                    }
                }
            }
        }
        .padding(.horizontal, 14)
    }
}

/// LeakRow is one finding: the money on the left, one line of why, and the
/// evidence only when you ask for it.
struct LeakRow: View {
    let finding: Finding
    @Environment(\.uiLanguage) private var lang
    let total: Double
    let expanded: Bool
    let toggle: () -> Void
    var runAct: ((FindingAct) -> Void)? = nil
    var busy: Bool = false

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Button(action: toggle) {
                HStack(alignment: .top, spacing: 9) {
                    VStack(alignment: .trailing, spacing: 0) {
                        Text(Format.money(finding.amount))
                            .font(.system(size: 13, weight: .semibold, design: .rounded))
                            .foregroundStyle(.orange).monospacedDigit()
                        Text(share).font(.system(size: 9)).foregroundStyle(.tertiary).monospacedDigit()
                    }
                    .frame(width: 58, alignment: .trailing)
                    Text(finding.title)
                        .font(.system(size: 11))
                        .fixedSize(horizontal: false, vertical: true)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.system(size: 8, weight: .semibold)).foregroundStyle(.tertiary)
                        .padding(.top, 3)
                }
                .padding(.vertical, 6).padding(.horizontal, 8)
                .background(Color.primary.opacity(0.04), in: RoundedRectangle(cornerRadius: 7))
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if expanded {
                VStack(alignment: .leading, spacing: 3) {
                    ForEach(finding.detail ?? [], id: \.self) { line in
                        Text("· " + line).font(.system(size: 10)).foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    if finding.correlated {
                        Text(L("association, not a controlled test", lang))
                            .font(.system(size: 9)).foregroundStyle(.tertiary)
                    }
                    if let action = finding.action, !action.isEmpty {
                        Text(action).font(.system(size: 10)).foregroundStyle(.green)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    // The remedy sits next to the finding. Without this the
                    // Coach tab is the one place in the panel where reading
                    // something leads nowhere: you have to remember it, switch
                    // tabs and do it yourself, which is why nobody does.
                    if let act = finding.act, let run = runAct {
                        Button(act.label) { run(act) }
                            .buttonStyle(.bordered).controlSize(.small)
                            .disabled(busy)
                            .padding(.top, 2)
                    }
                }
                .padding(.leading, 12).padding(.bottom, 4)
            }
        }
    }

    private var share: String {
        total == 0 ? "" : String(format: "%.0f%%", finding.amount / total * 100)
    }
}

// MARK: - Settings

struct SettingsTab: View {
    @ObservedObject var health: HealthLoader
    @Binding var selected: String
    @Binding var notifications: Bool
    @Binding var language: String
    @Environment(\.uiLanguage) private var lang
    let snapshot: Snapshot
    @ObservedObject var loader: Loader
    @ObservedObject var quota: QuotaLoader

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Block(title: "Menu bar shows") {
                VStack(spacing: 0) {
                    ForEach(BarStyle.allCases) { option in
                        styleOption(option)
                    }
                }
                .padding(.top, 2)
            }
            HealthSection(health: health)
            Block(title: "Language") {
                Picker("", selection: $language) {
                    ForEach(Lang.allCases) { Text($0.title).tag($0.rawValue) }
                }
                .pickerStyle(.segmented).labelsHidden()
            }
            Block(title: "Notifications") {
                VStack(alignment: .leading, spacing: 4) {
                    Toggle(L("Tell me when an agent needs me", lang), isOn: $notifications)
                        .toggleStyle(.switch).controlSize(.small)
                        .font(.system(size: 12))
                        .onChange(of: notifications) { _, on in
                            loader.notificationsEnabled = on
                            quota.notificationsEnabled = on
                            if on { Notifier.shared.enable() }
                        }
                    Text(L("One notification when an agent asks a question or finishes a turn. Nothing while it is working.", lang))
                        .font(.system(size: 11)).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            Block(title: "About") {
                Text(L("pitwall reads what Claude Code already wrote to disk: running sessions, token usage, and the git worktrees your agents left behind. It sends nothing anywhere.", lang))
                    .font(.system(size: 11)).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 8)
        }
    }

    private func styleOption(_ option: BarStyle) -> some View {
        let isSelected = selected == option.rawValue
        // The preview used to share one line with the description, which left
        // the wider styles squeezed into whatever space the text did not take.
        // Title and preview get the first line; the description gets its own.
        return Button { selected = option.rawValue } label: {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 8) {
                    Image(systemName: isSelected ? "checkmark.circle.fill" : "circle")
                        .font(.system(size: 12))
                        .foregroundStyle(isSelected ? Color.accentColor : Color.secondary.opacity(0.5))
                    Text(option.title(lang)).font(.system(size: 12))
                    Spacer(minLength: 10)
                    BarLabel(snapshot: snapshot, style: option, quota: quota.usage)
                        .font(.system(size: 11))
                        .lineLimit(1)
                        .padding(.horizontal, 7).padding(.vertical, 4)
                        .background(Color.primary.opacity(0.08), in: RoundedRectangle(cornerRadius: 6))
                }
                Text(option.blurb(lang)).font(.system(size: 10)).foregroundStyle(.tertiary)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.leading, 20)
            }
            .padding(.vertical, 5).padding(.horizontal, 6)
            .background(isSelected ? Color.accentColor.opacity(0.10) : .clear,
                        in: RoundedRectangle(cornerRadius: 6))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

// MARK: - Grouping and actions

struct AgentGroup: Identifiable {
    let id: String
    let title: String
    let agents: [Agent]

    static func groups(from agents: [Agent]) -> [AgentGroup] {
        let order: [(String, String)] = [
            ("WAITING", "Needs you"), ("DONE", "Finished"),
            ("WORKING", "Running"), ("IDLE", "Idle"), ("STALE", "Stale"),
        ]
        return order.compactMap { state, title in
            let matching = agents.filter { $0.state == state }
            return matching.isEmpty ? nil : AgentGroup(id: state, title: title, agents: matching)
        }
    }
}

/// focusAgent asks pitwall to bring that session's terminal tab forward.
func focusAgent(_ agent: Agent) {
    guard agent.pid > 0 else { return }
    let p = Process()
    p.executableURL = URL(fileURLWithPath: pitwallBinary)
    p.arguments = ["focus", String(agent.pid)]
    try? p.run()
}

func revealClaudeFolder() {
    let home = ProcessInfo.processInfo.environment["HOME"] ?? NSHomeDirectory()
    let p = Process()
    p.executableURL = URL(fileURLWithPath: "/usr/bin/open")
    p.arguments = ["\(home)/.claude"]
    try? p.run()
}

func shellQuote(_ s: String) -> String { "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'" }

/// openInTerminal runs a command in a new Terminal window, for the few things
/// that genuinely belong in a terminal.
func openInTerminal(_ command: String) {
    let escaped = command.replacingOccurrences(of: "\"", with: "\\\"")
    let script = """
    tell application "Terminal"
        activate
        do script "\(escaped)"
    end tell
    """
    let p = Process()
    p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
    p.arguments = ["-e", script]
    try? p.run()
}
