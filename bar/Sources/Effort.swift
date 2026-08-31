import SwiftUI

struct EffortLevelStat: Decodable, Equatable {
    let level: String
    let prompts: Int
    let spend: Double
    let edits: Int
    var perEdit: Double { edits == 0 ? 0 : spend / Double(edits) }
}

struct EffortProject: Decodable, Equatable, Identifiable {
    var id: String { repo }
    let name: String
    let repo: String
    let current: String
    let source: String
    let suggest: String?
    let reason: String?
    let spend: Double
    let levels: [EffortLevelStat]?
    let primed: Bool
    let sessions: Int
    let openingCost: Double

    enum CodingKeys: String, CodingKey {
        case name, repo, current, source, suggest, reason, spend, levels, primed, sessions
        case openingCost = "opening_cost"
    }

    var hasSuggestion: Bool {
        guard let s = suggest, !s.isEmpty else { return false }
        return s != current
    }
}

/// EffortLoader reads and writes the per-project effort defaults, so the whole
/// thing can be done from the panel instead of a terminal.
@MainActor
final class EffortLoader: ObservableObject {
    @Published private(set) var projects: [EffortProject] = []
    @Published private(set) var loading = false
    @Published private(set) var error: String?
    @Published private(set) var busy: Set<String> = []

    func loadIfNeeded() {
        guard projects.isEmpty, !loading else { return }
        reload()
    }

    func reload() {
        loading = true
        error = nil
        let bin = pitwallBinary
        Task.detached(priority: .userInitiated) {
            let result = runEffort(bin)
            await MainActor.run {
                self.loading = false
                switch result {
                case .success(let list): self.projects = list
                case .failure(let e): self.error = e.message
                }
            }
        }
    }

    /// set pins one project, or clears the pin when level is nil.
    func set(_ project: EffortProject, to level: String?) {
        busy.insert(project.repo)
        let bin = pitwallBinary
        let args = level.map { ["effort", "--set", $0, project.repo] } ?? ["effort", "--clear", project.repo]
        Task.detached(priority: .userInitiated) {
            let p = Process()
            p.executableURL = URL(fileURLWithPath: bin)
            p.arguments = args
            p.standardOutput = Pipe()
            p.standardError = Pipe()
            try? p.run()
            p.waitUntilExit()
            await MainActor.run {
                self.busy.remove(project.repo)
                self.reload()
            }
        }
    }

    /// writePrimer drafts a CLAUDE.md for a project that has none.
    func writePrimer(_ project: EffortProject) {
        busy.insert(project.repo)
        let bin = pitwallBinary
        Task.detached(priority: .userInitiated) {
            let p = Process()
            p.executableURL = URL(fileURLWithPath: bin)
            p.arguments = ["primer", project.repo, "--write"]
            p.standardOutput = Pipe()
            p.standardError = Pipe()
            try? p.run()
            p.waitUntilExit()
            await MainActor.run {
                self.busy.remove(project.repo)
                self.reload()
            }
        }
    }

    func applyAllSuggestions() {
        let bin = pitwallBinary
        loading = true
        Task.detached(priority: .userInitiated) {
            let p = Process()
            p.executableURL = URL(fileURLWithPath: bin)
            p.arguments = ["effort", "--apply"]
            p.standardOutput = Pipe()
            p.standardError = Pipe()
            try? p.run()
            p.waitUntilExit()
            await MainActor.run { self.reload() }
        }
    }
}

func runEffort(_ path: String) -> Result<[EffortProject], LoadError> {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: path)
    process.arguments = ["effort", "--json"]
    let out = Pipe(), err = Pipe()
    process.standardOutput = out
    process.standardError = err
    do { try process.run() } catch {
        return .failure(LoadError(message: "cannot run \(path)"))
    }
    let data = out.fileHandleForReading.readDataToEndOfFile()
    _ = err.fileHandleForReading.readDataToEndOfFile()
    process.waitUntilExit()
    struct Wrapper: Decodable { let projects: [EffortProject]? }
    do {
        let w = try JSONDecoder().decode(Wrapper.self, from: data)
        return .success(w.projects ?? [])
    } catch {
        return .failure(LoadError(message: "could not read the effort report"))
    }
}

let allEffortLevels = ["low", "medium", "high", "xhigh", "max"]

// MARK: - Tab

struct ProjectsTab: View {
    @ObservedObject var effort: EffortLoader
    @Environment(\.uiLanguage) private var lang

    private var interesting: [EffortProject] {
        effort.projects
            .filter { $0.spend >= 50 || $0.hasSuggestion || $0.source != "user" }
            .sorted { $0.spend > $1.spend }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 3) {
                Text(L("What makes each project cheaper", lang))
                    .font(.system(size: 12, weight: .medium))
                Text(L("Two things decide what a session here costs: whether it starts with any context, and what effort it launches at. Both are set once, per project.", lang))
                    .font(.system(size: 11)).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.horizontal, 14).padding(.top, 12).padding(.bottom, 10)

            if effort.loading && effort.projects.isEmpty {
                HStack(spacing: 7) {
                    ProgressView().controlSize(.small)
                    Text(L("Reading your history…", lang)).font(.callout).foregroundStyle(.secondary)
                }
                .padding(.horizontal, 14).padding(.bottom, 10)
            } else if let error = effort.error {
                Text(error).font(.system(size: 11)).foregroundStyle(.orange).padding(14)
            } else if interesting.isEmpty {
                Text(L("Not enough history yet.", lang))
                    .font(.system(size: 11)).foregroundStyle(.secondary).padding(14)
            } else {
                let suggested = interesting.filter { $0.hasSuggestion }
                if !suggested.isEmpty {
                    Button { effort.applyAllSuggestions() } label: {
                        HStack(spacing: 6) {
                            Image(systemName: "wand.and.stars").font(.system(size: 11))
                            Text(lang == .ru
                             ? "Применить все \(suggested.count) рекомендации"
                             : "Apply all \(suggested.count) effort suggestions")
                                .font(.system(size: 12, weight: .medium))
                        }
                        .padding(.vertical, 7).frame(maxWidth: .infinity)
                        .background(Color.accentColor.opacity(0.16), in: RoundedRectangle(cornerRadius: 7))
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .padding(.horizontal, 14).padding(.bottom, 10)
                }
                VStack(spacing: 8) {
                    ForEach(interesting) { ProjectCard(project: $0, effort: effort) }
                }
                .padding(.horizontal, 12)
            }
            Spacer(minLength: 10)
        }
    }
}

struct ProjectCard: View {
    let project: EffortProject
    @Environment(\.uiLanguage) private var lang
    @ObservedObject var effort: EffortLoader

    private var working: Bool { effort.busy.contains(project.repo) }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Text(project.name).font(.system(size: 12, weight: .semibold))
                if project.source != "user" {
                    Text(L("pinned", lang)).font(.system(size: 9))
                        .padding(.horizontal, 4).padding(.vertical, 1)
                        .background(Color.accentColor.opacity(0.2), in: Capsule())
                }
                Spacer()
                if working { ProgressView().controlSize(.small) }
                Text(Format.money(project.spend))
                    .font(.system(size: 11, weight: .medium)).foregroundStyle(.secondary).monospacedDigit()
            }

            lever(icon: "doc.text",
                  label: L("primer", lang),
                  value: project.primed ? L("written", lang) : L("none", lang),
                  valueTint: project.primed ? Color.green : Color.orange,
                  note: project.openingCost > 0
                        ? (lang == .ru
                           ? "\(Format.money(project.openingCost)) за стартовый промпт"
                           : "\(Format.money(project.openingCost)) per opening prompt") : "",
                  action: project.primed ? nil : L("Write it", lang),
                  run: { effort.writePrimer(project) })

            lever(icon: "dial.medium",
                  label: L("effort", lang),
                  value: project.current,
                  valueTint: .primary,
                  // Without a suggestion the row used to show a level, a gap
                  // and a lone menu icon, which reads as something failing to
                  // render. Say why there is nothing to recommend instead.
                  note: project.hasSuggestion
                        ? (project.reason ?? "")
                        : (lang == .ru
                           ? "мало данных, чтобы сравнить уровни"
                           : "not enough history to compare levels"),
                  action: project.hasSuggestion
                          ? (lang == .ru ? "Поставить \(project.suggest ?? "")" : "Set \(project.suggest ?? "")")
                          : nil,
                  run: { if let s = project.suggest { effort.set(project, to: s) } },
                  trailing: AnyView(levelMenu))
        }
        .padding(10)
        .background(Color.primary.opacity(0.04), in: RoundedRectangle(cornerRadius: 8))
    }

    private var levelMenu: some View {
        Menu {
            ForEach(allEffortLevels, id: \.self) { level in
                Button {
                    effort.set(project, to: level)
                } label: {
                    if level == project.current { Label(level, systemImage: "checkmark") }
                    else { Text(level) }
                }
            }
            Divider()
            Button(L("Use my default", lang)) { effort.set(project, to: nil) }
        } label: {
            Image(systemName: "slider.horizontal.3").font(.system(size: 10))
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .disabled(working)
    }

    @ViewBuilder
    private func lever(icon: String, label: String, value: String, valueTint: Color,
                       note: String, action: String?, run: @escaping () -> Void,
                       trailing: AnyView? = nil) -> some View {
        HStack(alignment: .center, spacing: 7) {
            Image(systemName: icon).font(.system(size: 10)).foregroundStyle(.tertiary).frame(width: 12)
            Text(label).font(.system(size: 10)).foregroundStyle(.tertiary).frame(width: 38, alignment: .leading)
            Text(value).font(.system(size: 11, weight: .medium)).foregroundStyle(valueTint)
            if !note.isEmpty {
                Text(note).font(.system(size: 10)).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer(minLength: 4)
            // Both levers reserve the same button width and the same trailing
            // slot, so the blue buttons form one column instead of sitting at
            // three different distances from the edge.
            Group {
                if let action {
                    Button(action: run) {
                        Text(action).font(.system(size: 10, weight: .medium))
                            .lineLimit(1).frame(maxWidth: .infinity)
                            .padding(.vertical, 3)
                            .background(Color.accentColor.opacity(0.18), in: RoundedRectangle(cornerRadius: 5))
                    }
                    .buttonStyle(.plain).disabled(working)
                } else {
                    Color.clear
                }
            }
            .frame(width: 84, height: 20)

            Group {
                if let trailing { trailing } else { Color.clear }
            }
            .frame(width: 16)
        }
    }
}
