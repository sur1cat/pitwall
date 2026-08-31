import Foundation
import UserNotifications

/// Notifier posts a macOS notification when an agent starts waiting or
/// finishes. It asks for permission once, and quietly falls back to an
/// AppleScript notification if the system refuses — a menu bar tool should
/// never be the reason something crashes or nags.
@MainActor
final class Notifier {
    static let shared = Notifier()

    private var authorized = false
    private var asked = false

    func enable() {
        guard !asked else { return }
        asked = true
        UNUserNotificationCenter.current()
            .requestAuthorization(options: [.alert, .sound]) { granted, _ in
                Task { @MainActor in self.authorized = granted }
            }
    }

    func post(title: String, body: String) {
        guard authorized else { return fallback(title: title, body: body) }
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        let request = UNNotificationRequest(identifier: UUID().uuidString,
                                            content: content, trigger: nil)
        UNUserNotificationCenter.current().add(request) { error in
            if error != nil {
                Task { @MainActor in self.fallback(title: title, body: body) }
            }
        }
    }

    private func fallback(title: String, body: String) {
        let esc = { (s: String) in s.replacingOccurrences(of: "\"", with: "'") }
        let script = "display notification \"\(esc(body))\" with title \"\(esc(title))\""
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        p.arguments = ["-e", script]
        try? p.run()
    }
}
