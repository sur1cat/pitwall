import SwiftUI

struct PromptRule: Decodable, Identifiable, Equatable {
    let id: String
    let title: String
    let evidence: String
    let fix: String
    let delta: Double

    /// helps is true for the one habit that reduced follow-up questions.
    var helps: Bool { delta < 0 }
}

@MainActor
final class PromptRules: ObservableObject {
    @Published private(set) var rules: [PromptRule] = []

    func loadIfNeeded() {
        guard rules.isEmpty else { return }
        let bin = pitwallBinary
        Task.detached(priority: .utility) {
            let p = Process()
            p.executableURL = URL(fileURLWithPath: bin)
            p.arguments = ["lint", "--rules", "--json"]
            let out = Pipe()
            p.standardOutput = out
            p.standardError = Pipe()
            guard (try? p.run()) != nil else { return }
            let data = out.fileHandleForReading.readDataToEndOfFile()
            p.waitUntilExit()
            struct Wrapper: Decodable { let rules: [PromptRule]? }
            let decoded = (try? JSONDecoder().decode(Wrapper.self, from: data))?.rules ?? []
            await MainActor.run { self.rules = decoded.sorted { $0.delta < $1.delta } }
        }
    }
}

/// PromptsTab shows what actually made a prompt land the first time. Every
/// number is from this machine's own history, not general advice.
struct PromptsTab: View {
    @StateObject private var model = PromptRules()
    @Environment(\.uiLanguage) private var lang

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 3) {
                Text(L("How to phrase what you want", lang))
                    .font(.system(size: 12, weight: .medium))
                Text(L("These rules come from your own history: how often prompts shaped a certain way ended in a clarifying question instead of work.", lang))
                    .font(.system(size: 11)).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.horizontal, 14).padding(.top, 12).padding(.bottom, 10)

            VStack(spacing: 6) {
                ForEach(model.rules) { rule in
                    HStack(alignment: .top, spacing: 9) {
                        Image(systemName: rule.helps ? "checkmark.circle.fill" : "exclamationmark.circle.fill")
                            .font(.system(size: 12))
                            .foregroundStyle(rule.helps ? Color.green : Color.orange)
                            .padding(.top, 1)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(rule.helps ? rule.fix : rule.title)
                                .font(.system(size: 11, weight: .medium))
                                .fixedSize(horizontal: false, vertical: true)
                            Text(rule.helps ? rule.title : rule.fix)
                                .font(.system(size: 10)).foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                            Text(rule.evidence)
                                .font(.system(size: 10)).foregroundStyle(.tertiary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                        Spacer(minLength: 4)
                        Text(String(format: "%+.1f", rule.delta) + "pp")
                            .font(.system(size: 10, weight: .medium)).monospacedDigit()
                            .foregroundStyle(rule.helps ? Color.green : Color.orange)
                    }
                    .padding(9)
                    .background(Color.primary.opacity(0.04), in: RoundedRectangle(cornerRadius: 7))
                }
            }
            .padding(.horizontal, 12)

            Text(L("The plugin applies this automatically: when a prompt leaves something unstated, pitwall tells the agent to assume and say so, instead of stopping to ask.", lang))
                .font(.system(size: 10)).foregroundStyle(.tertiary)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.horizontal, 14).padding(.top, 10)

            Spacer(minLength: 10)
        }
        .onAppear { model.loadIfNeeded() }
    }
}
