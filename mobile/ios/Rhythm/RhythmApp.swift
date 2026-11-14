import SwiftUI
import RhythmCore

@main @MainActor struct RhythmApp: App {
    private let configuration: Result<APIClient, Error>
    init() {
        configuration = Result {
            guard let raw = Bundle.main.object(forInfoDictionaryKey: "RhythmBaseURL") as? String,
                  let url = URL(string: raw) else { throw URLError(.badURL) }
            return try APIClient(baseURL: url)
        }
    }
    var body: some Scene {
        WindowGroup {
            switch configuration {
            case .success(let api): RootView(api: api)
            case .failure: ContentUnavailableView("Ошибка конфигурации", systemImage: "exclamationmark.triangle", description: Text("Укажите корректный HTTPS сервер в RhythmBaseURL."))
            }
        }
    }
}
