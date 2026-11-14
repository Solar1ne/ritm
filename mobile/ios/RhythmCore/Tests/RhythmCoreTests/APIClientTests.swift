import Foundation
import Testing
@testable import RhythmCore

private final class MockURLProtocol: URLProtocol, @unchecked Sendable {
    static let lock = NSLock()
    nonisolated(unsafe) static var handler: ((URLRequest) throws -> (Int, String))?
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        do {
            Self.lock.lock(); let handler = Self.handler; Self.lock.unlock()
            let callback = try #require(handler)
            let (status, body) = try callback(request)
            let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(body.utf8))
            client?.urlProtocolDidFinishLoading(self)
        } catch { client?.urlProtocol(self, didFailWithError: error) }
    }
    override func stopLoading() {}
}

@Suite(.serialized) @MainActor struct APIClientTests {
    private let empty = #"{"revision":7,"sessionBinding":"bound","identity":"guest","today":"2026-10-22","timeZone":"Europe/Moscow","profile":null,"entries":null,"plan":null,"nextPlan":null,"acceptedNextPlan":null,"nextPlanToken":"","decision":null}"#
    private func client(cookie: Bool = false, _ handler: @escaping (URLRequest) throws -> (Int, String)) throws -> APIClient {
        MockURLProtocol.lock.lock(); MockURLProtocol.handler = handler; MockURLProtocol.lock.unlock()
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [MockURLProtocol.self]
        if cookie {
            let value = HTTPCookie(properties: [.domain: "example.test", .path: "/", .name: "rhythm_session", .value: "signed-cookie", .secure: "TRUE"])!
            config.httpCookieStorage?.setCookie(value)
        }
        return try APIClient(baseURL: URL(string: "https://example.test")!, session: URLSession(configuration: config))
    }
    @Test func testHTTPSOnly() {
        for address in ["http://example.test", "https://user:pass@example.test", "https://example.test/path", "https://example.test?token=x"] {
             #expect(throws: APIError.self) { try APIClient(baseURL: URL(string: address)!) }
        }
    }
    @Test func testNullableNewAccountAndBindingRevisionContract() async throws {
        let json = empty
        let api = try client { request in
            if request.httpMethod == "GET" {
                XCTAssertNil(request.value(forHTTPHeaderField: "X-Rhythm-Session"))
            } else {
                XCTAssertEqual(request.url?.path, "/api/entries")
                XCTAssertEqual(request.value(forHTTPHeaderField: "X-Rhythm-Session"), "bound")
                XCTAssertEqual(request.value(forHTTPHeaderField: "Origin"), "https://example.test")
                XCTAssertEqual(request.value(forHTTPHeaderField: "Content-Type"), "application/json")
                var data = request.httpBody
                if data == nil, let stream = request.httpBodyStream {
                    stream.open(); defer { stream.close() }
                    var bytes = [UInt8](repeating: 0, count: 4096)
                    let count = stream.read(&bytes, maxLength: bytes.count)
                    data = Data(bytes.prefix(max(0, count)))
                }
                let requestData = try #require(data)
                let object = try JSONSerialization.jsonObject(with: requestData)
                let body = try #require(object as? [String: Any])
                XCTAssertEqual(body["revision"] as? Int, 7)
                let entry = try #require(body["entry"] as? [String: Any])
                XCTAssertEqual(entry["id"] as? String, "stable-id")
                XCTAssertNil(entry["fatigue"])
            }
            return (200, json)
        }
        let state = try await api.state()
        XCTAssertNil(state.profile); XCTAssertNil(state.plan); XCTAssertNil(state.entries)
        _ = try await api.apply(.entry(revision: state.revision, Entry(id: "stable-id", date: state.today, status: "wellbeing", sleep: 7)))
    }
    @Test func testLoginDoesNotSendOldBinding() async throws {
        let json = empty
        let api = try client { request in
            if request.url?.path == "/api/auth/login" { XCTAssertNil(request.value(forHTTPHeaderField: "X-Rhythm-Session")) }
            return (200, json)
        }
        _ = try await api.state()
        _ = try await api.login(login: "someone", password: "not-a-stored-secret")
    }
    @Test func testSessionErrorAndConflictDecodedWithoutRetry() async throws {
        for (status, code) in [(401, "session"), (409, "revision_conflict"), (409, "session_changed")] {
            let api = try client { _ in (status, "{\"error\":{\"code\":\"\(code)\",\"message\":\"Rejected\"}}") }
            do { _ = try await api.state(); Issue.record("Expected rejection") }
            catch let error as APIError {
                XCTAssertEqual(error.status, status); XCTAssertEqual(error.code, code)
                XCTAssertEqual(error.invalidatesIdentity, code != "revision_conflict")
            }
        }
    }
    @Test func testInvalidJSONRejected() async throws {
        let api = try client { _ in (200, "not-json") }
        do { _ = try await api.state(); Issue.record("Expected decoding failure") } catch is DecodingError {} 
    }
    @Test func testAcceptSendsReviewedTokenAndRevision() async throws {
        let json = empty
        let api = try client { request in
            if request.httpMethod == "POST" {
                XCTAssertEqual(request.url?.path, "/api/plan/accept")
                var data = request.httpBody
                if data == nil, let stream = request.httpBodyStream {
                    stream.open(); defer { stream.close() }
                    var bytes = [UInt8](repeating: 0, count: 4096)
                    let count = stream.read(&bytes, maxLength: bytes.count)
                    data = Data(bytes.prefix(max(0, count)))
                }
                let payload = try #require(data)
                let object = try JSONSerialization.jsonObject(with: payload)
                let body = try #require(object as? [String: Any])
                XCTAssertEqual(body["revision"] as? Int, 7)
                XCTAssertEqual(body["weekStart"] as? String, "2026-10-26")
                XCTAssertEqual(body["nextPlanToken"] as? String, "reviewed-token")
            }
            return (200, json)
        }
        _ = try await api.state()
        _ = try await api.apply(.accept(revision: 7, weekStart: "2026-10-26", token: "reviewed-token"))
    }
    @Test func testTransportFailureCannotSaveOptimistically() async throws {
        let json = empty
        let api = try client { request in
            if request.httpMethod != "GET" { throw URLError(.notConnectedToInternet) }
            return (200, json)
        }
        let model = AppModel(api: api)
        await model.refresh()
        await model.save(.entry(revision: 7, Entry(date: "2026-10-22", status: "wellbeing", sleep: 8)))
        XCTAssertEqual(model.state?.revision, 7)
        XCTAssertNil(model.state?.entries)
        XCTAssertTrue(model.needsRefresh)
    }
    @Test func testCookieAndBindingTravelTogether() async throws {
        let json = empty
        let api = try client(cookie: true) { request in
            XCTAssertEqual(request.value(forHTTPHeaderField: "Cookie"), "rhythm_session=signed-cookie")
            if request.url?.path == "/api/export" {
                XCTAssertEqual(request.value(forHTTPHeaderField: "X-Rhythm-Session"), "bound")
            }
            return (200, json)
        }
        _ = try await api.state()
        _ = try await api.export()
    }
    @Test func testAccountDeletionClearsStateOnlyAfterConfirmation() async throws {
        let json = empty.replacingOccurrences(of: "guest", with: "account")
        let api = try client { request in
            if request.httpMethod == "DELETE" {
                XCTAssertEqual(request.url?.path, "/api/account")
                XCTAssertEqual(request.value(forHTTPHeaderField: "X-Rhythm-Session"), "bound")
                return (200, #"{"accountDeleted":true}"#)
            }
            return (200, json)
        }
        let model = AppModel(api: api)
        await model.refresh()
        let review = try #require(model.state)
        await model.deleteAccount(password: "test-password", reviewedState: review)
        XCTAssertNil(model.state)
        XCTAssertFalse(model.needsRefresh)
    }
    @Test func testFailedAccountDeletionRetainsState() async throws {
        let json = empty.replacingOccurrences(of: "guest", with: "account")
        let api = try client { request in
            request.httpMethod == "DELETE" ? (401, #"{"error":{"code":"credentials","message":"Wrong password"}}"#) : (200, json)
        }
        let model = AppModel(api: api)
        await model.refresh()
        let review = try #require(model.state)
        await model.deleteAccount(password: "wrong", reviewedState: review)
        XCTAssertEqual(model.state?.identity, "account")
        XCTAssertTrue(model.needsRefresh)
    }
    @Test func testAccountDeletionKeepsReviewedRevisionAfterRefresh() async throws {
        let reviewedJSON = empty.replacingOccurrences(of: "guest", with: "account")
        let reviewed = try JSONDecoder().decode(AppState.self, from: Data(reviewedJSON.utf8))
        let updatedJSON = reviewedJSON.replacingOccurrences(of: "\"revision\":7", with: "\"revision\":8")
        let api = try client { request in
            if request.httpMethod == "DELETE" {
                var data = request.httpBody
                if data == nil, let stream = request.httpBodyStream {
                    stream.open(); defer { stream.close() }
                    var result = Data(), buffer = [UInt8](repeating: 0, count: 1024)
                    while stream.hasBytesAvailable {
                        let count = stream.read(&buffer, maxLength: buffer.count)
                        guard count >= 0 else { throw URLError(.cannotDecodeRawData) }
                        if count == 0 { break }
                        result.append(contentsOf: buffer.prefix(count))
                    }
                    data = result
                }
                let body = try #require(data)
                let payload = try #require(JSONSerialization.jsonObject(with: body) as? [String: Any])
                XCTAssertEqual(payload["revision"] as? Int, 7)
                return (409, #"{"error":{"code":"revision_conflict","message":"Changed since review"}}"#)
            }
            return (200, updatedJSON)
        }
        let model = AppModel(api: api)
        await model.refresh()
        XCTAssertEqual(model.state?.revision, 8)
        await model.deleteAccount(password: "test-password", reviewedState: reviewed)
        XCTAssertEqual(model.state?.revision, 8)
        XCTAssertTrue(model.needsRefresh)
    }
    @Test func testAccountDeletionReviewCannotCrossAnIdentityChange() async throws {
        let reviewedJSON = empty.replacingOccurrences(of: "guest", with: "account")
        let reviewed = try JSONDecoder().decode(AppState.self, from: Data(reviewedJSON.utf8))
        let changedJSON = reviewedJSON.replacingOccurrences(of: "bound", with: "another-binding")
        let api = try client { request in
            XCTAssertEqual(request.httpMethod, "GET")
            return (200, changedJSON)
        }
        let model = AppModel(api: api)
        await model.refresh()
        await model.deleteAccount(password: "test-password", reviewedState: reviewed)
        XCTAssertEqual(model.state?.sessionBinding, "another-binding")
        XCTAssertNotNil(model.errorMessage)
        XCTAssertFalse(model.needsRefresh)
    }
    @Test func testFailedMutationRequiresRefreshAndDoesNotChangeState() async throws {
        let json = empty
        let api = try client { request in
            request.httpMethod == "GET" ? (200, json) : (409, #"{"error":{"code":"revision_conflict","message":"Changed"}}"#)
        }
        let model = AppModel(api: api)
        await model.refresh()
        await model.save(.deleteEntry(revision: 7, id: "record"))
        XCTAssertEqual(model.state?.revision, 7)
        XCTAssertTrue(model.needsRefresh)
        XCTAssertNotNil(model.errorMessage)
        await model.refresh()
        XCTAssertFalse(model.needsRefresh)
        XCTAssertNil(model.errorMessage)
    }
}

private func XCTAssertEqual<T: Equatable>(_ lhs: T, _ rhs: T, sourceLocation: SourceLocation = #_sourceLocation) { #expect(lhs == rhs, sourceLocation: sourceLocation) }
private func XCTAssertNil<T>(_ value: T?, sourceLocation: SourceLocation = #_sourceLocation) { #expect(value == nil, sourceLocation: sourceLocation) }
private func XCTAssertNotNil<T>(_ value: T?, sourceLocation: SourceLocation = #_sourceLocation) { #expect(value != nil, sourceLocation: sourceLocation) }
private func XCTAssertTrue(_ value: Bool, sourceLocation: SourceLocation = #_sourceLocation) { #expect(value, sourceLocation: sourceLocation) }
private func XCTAssertFalse(_ value: Bool, sourceLocation: SourceLocation = #_sourceLocation) { #expect(!value, sourceLocation: sourceLocation) }
