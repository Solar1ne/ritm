import Foundation
import Observation

@MainActor @Observable public final class AppModel {
    public private(set) var state: AppState?
    public private(set) var busy = false
    public private(set) var needsRefresh = false
    public var errorMessage: String?
    private let api: any RhythmAPI
    public init(api: any RhythmAPI) { self.api = api }
    public func refresh(resetIdentity: Bool = false) async {
        guard !busy else { return }
        if resetIdentity { api.resetBinding(); state = nil }
        await run { try await self.api.state() }
    }
    public func login(_ login: String, password: String) async {
        guard !busy else { return }
        state = nil
        await run { try await self.api.login(login: login, password: password) }
    }
    public func save(_ command: Command) async {
        guard !busy, !needsRefresh, state != nil else { return }
        await run { try await self.api.apply(command) }
    }
    public func logout() async {
        guard !busy else { return }
        busy = true
        defer { busy = false }
        do { try await api.logout(); state = nil; needsRefresh = false }
        catch { handle(error) }
    }
    public func deleteAccount(password: String, reviewedState: AppState) async {
        guard !busy, !needsRefresh, state?.identity == "account" else { return }
        guard reviewedState.identity == "account", state?.sessionBinding == reviewedState.sessionBinding else {
            errorMessage = "Сессия изменилась. Откройте подтверждение удаления заново."
            return
        }
        busy = true
        defer { busy = false }
        do { try await api.deleteAccount(password: password, revision: reviewedState.revision); state = nil; needsRefresh = false; errorMessage = nil }
        catch { handle(error) }
    }
    public func export() async -> Data? {
        guard !busy, !needsRefresh, state != nil else { return nil }
        busy = true
        defer { busy = false }
        do { return try await api.export() } catch { handle(error); return nil }
    }
    private func run(_ operation: () async throws -> AppState) async {
        busy = true; errorMessage = nil
        defer { busy = false }
        do { state = try await operation(); needsRefresh = false }
        catch { handle(error) }
    }
    private func handle(_ error: Error) {
        if let error = error as? APIError, error.invalidatesIdentity { state = nil }
        needsRefresh = true
        errorMessage = "Сервер не подтвердил действие. Обновите данные и проверьте результат перед повтором.\n" + error.localizedDescription
    }
}
