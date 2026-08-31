import SwiftUI

/// TreePlan is what `pitwall tree gc --dry-run` would do: how many worktrees
/// go, how much disk that returns, and how many hold uncommitted work that has
/// to be rescued first.
struct TreePlan: Decodable, Equatable {
    let count: Int
    let bytes: Int64
    let salvage: Int

    static let empty = TreePlan(count: 0, bytes: 0, salvage: 0)
}

/// TreeCleaner previews and runs the worktree cleanup. Preview and run are
/// separate calls: opening the panel must never remove anything.
@MainActor
final class TreeCleaner: ObservableObject {
    @Published private(set) var plan: TreePlan?
    @Published private(set) var working = false
    @Published private(set) var error: String?
    @Published private(set) var freed: Int64?

    func preview() {
        guard !working else { return }
        working = true
        error = nil
        let bin = pitwallBinary
        Task.detached(priority: .userInitiated) {
            let result = runTree(bin, apply: false)
            await MainActor.run {
                self.working = false
                switch result {
                case .success(let p): self.plan = p
                case .failure(let e): self.error = e.message
                }
            }
        }
    }

    func clean() {
        guard !working else { return }
        working = true
        error = nil
        let bin = pitwallBinary
        let expected = plan?.bytes ?? 0
        Task.detached(priority: .userInitiated) {
            let result = runTree(bin, apply: true)
            await MainActor.run {
                self.working = false
                switch result {
                case .success:
                    self.freed = expected
                    self.plan = nil
                case .failure(let e):
                    self.error = e.message
                }
            }
        }
    }
}

func runTree(_ path: String, apply: Bool) -> Result<TreePlan, LoadError> {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: path)
    // Salvage stays on: a worktree holding uncommitted work is committed to its
    // own branch and archived before anything is removed.
    process.arguments = apply
        ? ["tree", "gc", "--yes", "--json"]
        : ["tree", "gc", "--dry-run", "--json"]
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
    if apply { return .success(.empty) }
    do {
        return .success(try JSONDecoder().decode(TreePlan.self, from: data))
    } catch {
        return .failure(LoadError(message: "could not read the plan: \(error.localizedDescription)"))
    }
}

/// WorktreeCleanup is the button and its confirmation, shown under the
/// worktree counts on the Status tab.
struct WorktreeCleanup: View {
    @ObservedObject var cleaner: TreeCleaner
    let removable: Int
    @Environment(\.uiLanguage) private var lang
    @State private var confirming = false

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            if let freed = cleaner.freed {
                Text(lang == .ru
                     ? "Очищено, освобождено \(Format.bytes(freed))"
                     : "Cleaned up, \(Format.bytes(freed)) freed")
                    .font(.system(size: 11)).foregroundStyle(.green)
            } else if let error = cleaner.error {
                Text(error).font(.system(size: 10)).foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            } else if confirming, let plan = cleaner.plan {
                Text(plan.salvage > 0
                     ? (lang == .ru
                        ? "Удалить \(plan.count) веток и освободить \(Format.bytes(plan.bytes))? В \(plan.salvage) есть незакоммиченная работа — она будет закоммичена в свою ветку и заархивирована перед удалением."
                        : "Remove \(plan.count) worktrees and free \(Format.bytes(plan.bytes))? \(plan.salvage) holds uncommitted work, which is committed to its own branch and archived first.")
                     : (lang == .ru
                        ? "Удалить \(plan.count) веток и освободить \(Format.bytes(plan.bytes))? Все они полностью влиты."
                        : "Remove \(plan.count) worktrees and free \(Format.bytes(plan.bytes))? Every one is fully merged."))
                    .font(.system(size: 11)).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                HStack(spacing: 8) {
                    Button(L("Clean up", lang)) {
                        confirming = false
                        cleaner.clean()
                    }
                    .buttonStyle(.borderedProminent).controlSize(.small)
                    Button(L("Cancel", lang)) { confirming = false }
                        .buttonStyle(.bordered).controlSize(.small)
                }
            } else {
                HStack(spacing: 8) {
                    Button(L("Clean up", lang)) {
                        confirming = true
                        cleaner.preview()
                    }
                    .buttonStyle(.bordered).controlSize(.small)
                    .disabled(removable == 0 || cleaner.working)
                    if cleaner.working { ProgressView().controlSize(.small) }
                }
            }
        }
    }
}
