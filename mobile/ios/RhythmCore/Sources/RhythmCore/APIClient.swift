import Foundation

@MainActor public protocol RhythmAPI {
    func state() async throws -> AppState
    func login(login: String, password: String) async throws -> AppState
    func apply(_ command: Command) async throws -> AppState
    func logout() async throws
    func deleteAccount(password: String, revision: Int) async throws
    func export() async throws -> Data
    func resetBinding()
}

/// Single UI owner serializes operations. Cookies authenticate; binding only pins identity.
@MainActor public final class APIClient: RhythmAPI {
    public let baseURL: URL
    private let session: URLSession
    private var binding: String?
    private var generation = 0
    public init(baseURL: URL, session: URLSession? = nil) throws {
        guard baseURL.scheme == "https", baseURL.host != nil, baseURL.user == nil,
              baseURL.password == nil, baseURL.query == nil, baseURL.fragment == nil,
              baseURL.path.isEmpty || baseURL.path == "/" else {
            throw APIError(status: 0, code: "configuration", message: "Адрес сервера должен быть HTTPS origin.")
        }
        self.baseURL = baseURL
        if let session { self.session = session }
        else {
            let config = URLSessionConfiguration.default
            config.urlCache = nil
            config.httpCookieStorage = .shared
            self.session = URLSession(configuration: config, delegate: RejectRedirects(), delegateQueue: nil)
        }
    }
    public func resetBinding() { binding = nil; generation += 1 }
    public func state() async throws -> AppState { try await readState("/api/state") }
    public func login(login: String, password: String) async throws -> AppState {
        resetBinding()
        return try await readState("/api/auth/login", body: ["login": login, "password": password])
    }
    public func apply(_ command: Command) async throws -> AppState {
        switch command {
        case let .entry(revision, entry):
            let encoded = try JSONEncoder().encode(entry)
            return try await readState("/api/entries", body: ["revision": revision, "entry": JSONSerialization.jsonObject(with: encoded)])
        case let .deleteEntry(revision, id):
            return try await readState("/api/entries", body: ["revision": revision, "id": id], method: "DELETE")
        case let .accept(revision, weekStart, token):
            return try await readState("/api/plan/accept", body: ["revision": revision, "weekStart": weekStart, "nextPlanToken": token])
        }
    }
    public func logout() async throws {
        _ = try await request("/api/auth/logout", body: [:])
        resetBinding()
    }
    public func deleteAccount(password: String, revision: Int) async throws {
        let data = try await request("/api/account", body: ["password": password, "revision": revision, "confirmDelete": true], method: "DELETE")
        struct Confirmation: Decodable { let accountDeleted: Bool }
        guard try JSONDecoder().decode(Confirmation.self, from: data).accountDeleted else {
            throw APIError(status: 0, code: "response", message: "Удаление аккаунта не подтверждено.")
        }
        for cookie in session.configuration.httpCookieStorage?.cookies(for: baseURL) ?? [] {
            session.configuration.httpCookieStorage?.deleteCookie(cookie)
        }
        resetBinding()
    }
    public func export() async throws -> Data { try await request("/api/export") }
    private func readState(_ path: String, body: [String: Any]? = nil, method: String = "POST") async throws -> AppState {
        let data = try await request(path, body: body, method: method)
        let state = try JSONDecoder().decode(AppState.self, from: data)
        guard !state.sessionBinding.isEmpty else { throw APIError(status: 0, code: "response", message: "Некорректная сессия сервера.") }
        binding = state.sessionBinding
        return state
    }
    private func request(_ path: String, body: [String: Any]? = nil, method: String = "POST") async throws -> Data {
        let version = generation
        let targetURL = baseURL.appendingPathComponent(String(path.dropFirst()))
        var request = URLRequest(url: targetURL)
        request.httpMethod = body == nil ? "GET" : method
        request.timeoutInterval = 20
        request.cachePolicy = .reloadIgnoringLocalCacheData
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        // Bind only this origin's cookie jar to the request. URLSession processes Set-Cookie.
        if let cookies = session.configuration.httpCookieStorage?.cookies(for: targetURL), !cookies.isEmpty {
            for (header, value) in HTTPCookie.requestHeaderFields(with: cookies) {
                request.setValue(value, forHTTPHeaderField: header)
            }
        }
        if let binding { request.setValue(binding, forHTTPHeaderField: "X-Rhythm-Session") }
        if let body {
            request.setValue(baseURL.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/")), forHTTPHeaderField: "Origin")
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        let (data, response) = try await session.data(for: request)
        guard version == generation else { throw APIError(status: 409, code: "session_changed", message: "Сессия изменилась. Войдите снова.") }
        guard let response = response as? HTTPURLResponse else { throw URLError(.badServerResponse) }
        guard (200..<300).contains(response.statusCode) else {
            struct Envelope: Decodable { struct Detail: Decodable { let code: String; let message: String }; let error: Detail }
            let details = try? JSONDecoder().decode(Envelope.self, from: data)
            let error = APIError(status: response.statusCode, code: details?.error.code ?? "http", message: details?.error.message ?? "Сервер не подтвердил операцию.")
            if error.invalidatesIdentity { resetBinding() }
            throw error
        }
        guard response.mimeType == "application/json" else { throw URLError(.cannotParseResponse) }
        return data
    }
}

/// Never forward account headers or request bodies to a redirected destination.
private final class RejectRedirects: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}
