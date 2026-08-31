import SwiftUI

enum Lang: String, CaseIterable, Identifiable {
    case en, ru
    var id: String { rawValue }
    var title: String { self == .en ? "English" : "Русский" }
}

private struct LanguageKey: EnvironmentKey {
    static let defaultValue: Lang = .en
}

extension EnvironmentValues {
    var uiLanguage: Lang {
        get { self[LanguageKey.self] }
        set { self[LanguageKey.self] = newValue }
    }
}

/// ru holds the Russian text for every user-facing string, keyed by the
/// English original so the code reads the same in both languages.
private let ru: [String: String] = [
    // tabs and chrome
    "Status": "Сейчас",
    "Coach": "Разбор",
    "Projects": "Проекты",
    "Setup": "Настройки",
    "Refresh": "Обновить",
    "Rescan": "Пересчитать",
    "Quit": "Выход",
    "Open ~/.claude": "Открыть ~/.claude",
    "Try again": "Ещё раз",
    "Prompts": "Промпты",

    // status
    "Needs you": "Ждут тебя",
    "Finished": "Закончили",
    "Running": "Работают",
    "Idle": "Простаивают",
    "Stale": "Мертвы",
    "Agents": "Агенты",
    "None running": "Никто не запущен",
    "Spent today": "Потрачено сегодня",
    "Rules": "Права",
    "Insights": "Разбор",
    "Habits": "Привычки",
    "7 days": "7 дней",
    "Reading your settings files…": "Читаю файлы настроек…",
    "What is wrong": "Что не так",
    "Enforcement": "Применение",
    "Files it would change": "Какие файлы изменит",
    "Clean up": "Очистить",
    "Re-check": "Проверить снова",
    "Cancel": "Отмена",
    "Worktrees": "Worktree'ы",
    "checkouts": "рабочих копий",
    "removable": "можно удалить",
    "Reading your Claude Code data…": "Читаю данные Claude Code…",
    "pitwall could not read your data": "pitwall не смог прочитать данные",

    // coach
    "Reading your transcripts…": "Читаю транскрипты…",
    "The first pass reads everything Claude Code kept. Later ones are instant.":
        "Первый проход читает всё, что сохранил Claude Code. Дальше — мгновенно.",
    "Biggest leaks": "Куда утекает больше всего",
    "changed code": "изменили код",
    "looked around": "только смотрели",
    "just talked": "просто разговор",
    "association, not a controlled test": "корреляция, а не контролируемый опыт",

    // projects
    "What makes each project cheaper": "Что делает проект дешевле",
    "Two things decide what a session here costs: whether it starts with any context, and what effort it launches at. Both are set once, per project.":
        "Стоимость сессии решают две вещи: с каким контекстом она стартует и на каком effort запускается. И то, и другое ставится один раз на проект.",
    "Reading your history…": "Читаю историю…",
    "Not enough history yet.": "Пока мало истории.",
    "primer": "праймер",
    "effort": "effort",
    "written": "есть",
    "none": "нет",
    "Write it": "Написать",
    "pinned": "закреплён",
    "Use my default": "Как по умолчанию",

    // settings
    "Menu bar shows": "В менюбаре",
    "Notifications": "Уведомления",
    "Tell me when an agent needs me": "Сообщать, когда агент ждёт меня",
    "One notification when an agent asks a question or finishes a turn. Nothing while it is working.":
        "Одно уведомление, когда агент задал вопрос или закончил ход. Пока работает — молчит.",
    "Language": "Язык",
    "About": "О программе",
    "pitwall reads what Claude Code already wrote to disk: running sessions, token usage, and the git worktrees your agents left behind. It sends nothing anywhere.":
        "pitwall читает то, что Claude Code уже записал на диск: запущенные сессии, расход токенов и git-worktree'ы, оставленные агентами. Наружу ничего не отправляет.",

    // bar styles
    "Signal": "Сигнал",
    "Spend": "Расход",
    "Burn rate": "Скорость трат",
    "Fleet": "Флот",
    "Everything": "Всё сразу",
    "One dot. Amber when an agent needs you.": "Одна точка. Оранжевая, когда агент ждёт.",
    "Today's spend, and the last five hours.": "Трата за сегодня и за последние пять часов.",
    "Dollars per hour, with a meter.": "Доллары в час.",
    "How many agents are waiting, done, working.": "Сколько агентов ждёт, работает, закончило.",
    "Who needs you, today's spend, and a meter.": "Кто ждёт и сколько потрачено за день.",

    // plan
    "Plan left": "Остаток плана",
    "5 hours": "5 часов",
    "rolling": "скользящее",
    "Plan left ": "Остаток плана",
    "How much of your plan is used, with a bar.": "Сколько плана израсходовано, по данным Anthropic.",

    // prompts tab
    "How to phrase what you want": "Как формулировать задачу",
    "These rules come from your own history: how often prompts shaped a certain way ended in a clarifying question instead of work.":
        "Правила выведены из твоей истории: как часто промпты определённой формы заканчивались уточняющим вопросом вместо работы.",
    "fewer follow-ups": "меньше переспросов",
    "more follow-ups": "больше переспросов",
    "The plugin applies this automatically: when a prompt leaves something unstated, pitwall tells the agent to assume and say so, instead of stopping to ask.":
        "Плагин применяет это сам: если промпт что-то недоговаривает, pitwall просит агента сделать разумное допущение и назвать его, а не останавливаться с вопросом.",
]

/// L returns the string in the current language, falling back to the original.
func L(_ key: String, _ lang: Lang) -> String {
    lang == .ru ? (ru[key] ?? key) : key
}

/// Localized reads the language from the environment, for views that only
/// need a handful of strings.
struct Localized: View {
    let key: String
    @Environment(\.uiLanguage) private var lang
    var body: some View { Text(L(key, lang)) }
}
