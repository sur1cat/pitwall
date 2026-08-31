import SwiftUI

struct PermsPlan: Decodable, Equatable, Identifiable {
    enum CodingKeys: String, CodingKey {
        case file, scope, remove, kept
        case removeBy = "remove_by"
        case rewrite
    }
    struct Rewrite: Decodable, Equatable, Hashable {
        let from: String
        let to: String
    }
    let file: String
    let scope: String
    let remove: Int
    let removeBy: String
    let rewrite: [Rewrite]
    let kept: Int

    var id: String { file }
    /// The last two path components are what identifies a project at a glance.
    var shortFile: String {
        let parts = file.split(separator: "/")
        return parts.count > 2 ? parts.suffix(3).joined(separator: "/") : file
    }
}

struct PermsReport: Decodable, Equatable {
    enum CodingKeys: String, CodingKey {
        case files, rules, remove, rewrite, plans, secrets, applied
        case byCategory = "by_category"
    }
    let files: Int
    let rules: Int
    let remove: Int
    let rewrite: Int
    let byCategory: [String: Int]
    let plans: [PermsPlan]
    let secrets: Int
    /// Present only after a write.
    let applied: Int?

    static let empty = PermsReport(files: 0, rules: 0, remove: 0, rewrite: 0,
                                   byCategory: [:], plans: [], secrets: 0, applied: nil)
}

/// PermsLoader runs the audit and, when asked, the cleanup. Writing is a
/// separate call that the view only makes after the user confirms, so no code
/// path can rewrite a settings file as a side effect of opening a tab.
@MainActor
final class PermsLoader: ObservableObject {
    @Published private(set) var report: PermsReport?
    @Published private(set) var loading = false
    @Published private(set) var error: String?
    @Published private(set) var lastWrite: PermsReport?

    func loadIfNeeded() {
        guard report == nil, !loading else { return }
        reload()
    }

    func reload() { run(write: false) }

    func applyCleanup() { run(write: true) }

    private func run(write: Bool) {
        loading = true
        error = nil
        let bin = pitwallBinary
        Task.detached(priority: .userInitiated) {
            let result = runPerms(bin, write: write)
            await MainActor.run {
                self.loading = false
                switch result {
                case .success(let r):
                    if write {
                        self.lastWrite = r
                        self.reload()   // re-audit so the numbers reflect the new files
                    } else {
                        self.report = r
                    }
                case .failure(let e):
                    self.error = e.message
                }
            }
        }
    }
}

func runPerms(_ path: String, write: Bool) -> Result<PermsReport, LoadError> {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: path)
    process.arguments = write ? ["perms", "fix", "--write", "--json"] : ["perms", "fix", "--json"]
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
        return .success(try JSONDecoder().decode(PermsReport.self, from: data))
    } catch {
        return .failure(LoadError(message: "could not read the audit: \(error.localizedDescription)"))
    }
}

/// categoryBlurb explains a finding in one line, in the panel's language.
func categoryBlurb(_ key: String, _ lang: Lang) -> String {
    switch (key, lang) {
    case ("secret", .ru):          return "в тексте правила лежит пароль или токен"
    case ("secret", _):            return "a credential is stored in the rule text"
    case ("fragment", .ru):        return "не команда — комментарий или кусок многострочного блока"
    case ("fragment", _):          return "not a command — a comment or part of a multi-line block"
    case ("unmatchable", .ru):     return "содержит разделитель команд, совпасть не может"
    case ("unmatchable", _):       return "contains a shell separator, so it can never match"
    case ("never-consulted", .ru): return "правило грузится, но доступ по нему не проверяется"
    case ("never-consulted", _):   return "loads, but file access is never checked against it"
    case ("repairable", .ru):      return "сломано, но чинится без выдачи новых прав"
    case ("repairable", _):        return "broken, but repairable without granting anything new"
    case ("wildcard-inside", .ru): return "звёздочка не в конце — совпадает шире, чем кажется"
    case ("wildcard-inside", _):   return "a * before the end matches more than it looks like"
    case ("shadowed", .ru):        return "более широкое правило это уже покрывает"
    case ("shadowed", _):          return "a broader rule already covers it"
    case ("one-off", .ru):         return "без подстановки — совпадёт с одной командой навсегда"
    case ("one-off", _):           return "no wildcard, so it matches one command forever"
    case ("duplicate", .ru):       return "такое правило уже есть"
    case ("duplicate", _):         return "the same rule is already present"
    case ("ignored", .ru):         return "Claude Code пропускает правило при загрузке"
    case ("ignored", _):           return "Claude Code skips the rule when it loads settings"
    default:                       return key
    }
}

/// Categories the cleanup removes, as opposed to the ones it only reports.
let removedCategories: Set<String> = ["secret", "fragment", "unmatchable", "shadowed", "duplicate", "ignored"]

// MARK: - Tab

struct PermsTab: View {
    @ObservedObject var perms: PermsLoader
    @ObservedObject var steward: StewardLoader
    @State private var confirming = false
    @Environment(\.uiLanguage) private var lang

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if perms.loading && perms.report == nil {
                HStack(spacing: 7) {
                    ProgressView().controlSize(.small)
                    Text(L("Reading your settings files…", lang)).font(.callout)
                }
                .padding(14)
            } else if let error = perms.error {
                Text(error).font(.system(size: 11)).foregroundStyle(.orange).padding(14)
            } else if let r = perms.report {
                headline(r)
                if r.secrets > 0 { secretBanner(r) }
                categories(r)
                if !r.plans.isEmpty { files(r) }
                action(r)
                StewardSection(steward: steward)
            }
            Spacer(minLength: 10)
        }
    }

    private func headline(_ r: PermsReport) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text("\(r.remove)")
                .font(.system(size: 24, weight: .medium, design: .rounded)).monospacedDigit()
            Text(lang == .ru
                 ? "правил из \(r.rules) не могут сработать никогда"
                 : "of \(r.rules) rules can never match")
                .font(.system(size: 11)).foregroundStyle(.secondary)
            if let w = perms.lastWrite, let applied = w.applied, applied > 0 {
                Text(lang == .ru
                     ? "очищено: \(w.remove) правил в \(applied) файлах, копии сохранены"
                     : "cleaned \(w.remove) rules in \(applied) files, originals backed up")
                    .font(.system(size: 11)).foregroundStyle(.green).padding(.top, 3)
            }
        }
        .padding(.horizontal, 14).padding(.top, 12)
    }

    private func secretBanner(_ r: PermsReport) -> some View {
        HStack(alignment: .top, spacing: 6) {
            Image(systemName: "key.slash").font(.system(size: 11)).foregroundStyle(.orange)
            Text(lang == .ru
                 ? "В \(r.secrets) правилах записаны токены и пароли открытым текстом. Очистка уберёт их из настроек, но значения останутся в резервных копиях и в старых транскриптах — их нужно ротировать."
                 : "\(r.secrets) rules hold credentials in plaintext. Cleaning removes them from the settings files, but the values remain in the backups and in old transcripts — rotate them.")
                .font(.system(size: 10)).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.horizontal, 14).padding(.top, 10)
    }

    private func categories(_ r: PermsReport) -> some View {
        Block(title: "What is wrong") {
            VStack(spacing: 3) {
                ForEach(r.byCategory.sorted { $0.value > $1.value }, id: \.key) { key, count in
                    HStack(spacing: 7) {
                        Circle()
                            .fill(removedCategories.contains(key) ? Color.orange : Color.secondary.opacity(0.4))
                            .frame(width: 5, height: 5)
                        Text("\(count)")
                            .font(.system(size: 12, weight: .medium)).monospacedDigit()
                            .frame(width: 34, alignment: .trailing)
                        Text(categoryBlurb(key, lang))
                            .font(.system(size: 11)).foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                        Spacer(minLength: 0)
                    }
                }
                HStack(spacing: 7) {
                    Circle().fill(Color.orange).frame(width: 5, height: 5)
                    Text(lang == .ru ? "оранжевое — то, что уберёт очистка" : "orange is what the cleanup removes")
                        .font(.system(size: 10)).foregroundStyle(.tertiary)
                    Spacer(minLength: 0)
                }
                .padding(.top, 2)
            }
        }
    }

    private func files(_ r: PermsReport) -> some View {
        Block(title: "Files it would change") {
            VStack(spacing: 4) {
                ForEach(r.plans.prefix(8)) { plan in
                    VStack(alignment: .leading, spacing: 1) {
                        HStack(spacing: 6) {
                            Text(plan.shortFile)
                                .font(.system(size: 11, design: .monospaced)).lineLimit(1).truncationMode(.head)
                            Spacer(minLength: 4)
                            if plan.remove > 0 {
                                Text("−\(plan.remove)").font(.system(size: 11, weight: .medium))
                                    .monospacedDigit().foregroundStyle(.orange)
                            }
                            if !plan.rewrite.isEmpty {
                                Text("~\(plan.rewrite.count)").font(.system(size: 11, weight: .medium))
                                    .monospacedDigit().foregroundStyle(.green)
                            }
                        }
                        if !plan.removeBy.isEmpty {
                            Text(plan.removeBy).font(.system(size: 10)).foregroundStyle(.tertiary)
                                .lineLimit(1).truncationMode(.tail)
                        }
                    }
                }
                if r.plans.count > 8 {
                    Text(lang == .ru ? "и ещё \(r.plans.count - 8)" : "and \(r.plans.count - 8) more")
                        .font(.system(size: 10)).foregroundStyle(.tertiary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
        }
    }

    private func action(_ r: PermsReport) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            if confirming {
                Text(lang == .ru
                     ? "Удалить \(r.remove) правил в \(r.plans.count) файлах? Каждый файл сначала копируется в ~/.claude/pitwall/perms-backups. Рабочие правила остаются, права не расширяются."
                     : "Remove \(r.remove) rules across \(r.plans.count) files? Every file is copied to ~/.claude/pitwall/perms-backups first. Working rules stay, and no permission is widened.")
                    .font(.system(size: 11)).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                HStack(spacing: 8) {
                    Button(L("Clean up", lang)) {
                        confirming = false
                        perms.applyCleanup()
                    }
                    .buttonStyle(.borderedProminent).controlSize(.small)
                    Button(L("Cancel", lang)) { confirming = false }
                        .buttonStyle(.bordered).controlSize(.small)
                }
            } else {
                HStack(spacing: 8) {
                    Button(L("Clean up", lang)) { confirming = true }
                        .buttonStyle(.bordered).controlSize(.small)
                        .disabled(r.remove == 0 && r.rewrite == 0 || perms.loading)
                    Button(L("Re-check", lang)) { perms.reload() }
                        .buttonStyle(.bordered).controlSize(.small)
                        .disabled(perms.loading)
                    if perms.loading { ProgressView().controlSize(.small) }
                }
                Text(lang == .ru
                     ? "Ничего не записывается, пока не нажмёшь."
                     : "Nothing is written until you press it.")
                    .font(.system(size: 10)).foregroundStyle(.tertiary)
            }
        }
        .padding(.horizontal, 14).padding(.top, 12)
    }
}
