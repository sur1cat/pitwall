import SwiftUI

/// InsightsTab holds the three things you read occasionally rather than watch.
///
/// They used to be three top-level tabs, which put them in the same row as the
/// two things you actually glance at. Six tabs across a 470pt panel also left
/// no room for any of the labels to be legible. Grouping by how often something
/// is read, rather than by what it is about, gets the row down to four.
struct InsightsTab: View {
    @ObservedObject var coach: CoachLoader
    @ObservedObject var effort: EffortLoader
    @Environment(\.uiLanguage) private var lang
    @Binding var section: InsightsSection

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Picker("", selection: $section) {
                ForEach(InsightsSection.allCases) {
                    Text($0.title(lang)).font(.system(size: 11)).tag($0)
                }
            }
            .pickerStyle(.segmented).labelsHidden()
            .padding(.horizontal, 14).padding(.top, 10).padding(.bottom, 4)

            switch section {
            case .habits:   CoachTab(coach: coach)
            case .projects: ProjectsTab(effort: effort)
            case .prompts:  PromptsTab()
            }
        }
        .onChange(of: section) { _, new in
            if new == .habits { coach.loadIfNeeded() }
            if new == .projects { effort.loadIfNeeded() }
        }
    }
}

enum InsightsSection: String, CaseIterable, Identifiable {
    case habits, projects, prompts
    var id: String { rawValue }
    func title(_ lang: Lang) -> String { L(rawTitle, lang) }
    var rawTitle: String {
        switch self {
        case .habits:   return "Habits"
        case .projects: return "Projects"
        case .prompts:  return "Prompts"
        }
    }
}
