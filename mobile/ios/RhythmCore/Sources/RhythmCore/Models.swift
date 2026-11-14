import Foundation

public struct Profile: Codable, Sendable {
    public let name: String
    public let goal: String
    public let experience: String
    public let days: [Int]?
    public let minutes: Int
}
public struct Session: Codable, Identifiable, Sendable {
    public let id: String
    public let date: String
    public let title: String
    public let minutes: Int
    public let status: String
    public let detailsKnown: Bool
}
public struct Plan: Codable, Sendable {
    public let weekStart: String
    public let sessions: [Session]?
    public let totalMinutes: Int
}
public struct Decision: Codable, Sendable {
    public let title: String
    public let reasons: [String]?
}
public struct Entry: Codable, Identifiable, Sendable {
    public let id: String
    public let date: String
    public let sessionId: String?
    public let status: String
    public let sleep: Double?
    public let fatigue: Int?
    public init(id: String = UUID().uuidString, date: String, sessionId: String? = nil, status: String, sleep: Double? = nil, fatigue: Int? = nil) {
        self.id = id; self.date = date; self.sessionId = sessionId
        self.status = status; self.sleep = sleep; self.fatigue = fatigue
    }
}
public struct AppState: Decodable, Sendable {
    public let revision: Int
    public let sessionBinding: String
    public let identity: String
    public let accountsEnabled: Bool?
    public let today: String
    public let timeZone: String
    public let profile: Profile?
    public let entries: [Entry]?
    public let plan: Plan?
    public let nextPlan: Plan?
    public let acceptedNextPlan: Plan?
    public let nextPlanToken: String
    public let decision: Decision?
}
public enum Command: Sendable {
    case entry(revision: Int, Entry)
    case deleteEntry(revision: Int, id: String)
    case accept(revision: Int, weekStart: String, token: String)
}
public struct APIError: Error, LocalizedError, Sendable {
    public let status: Int
    public let code: String
    public let message: String
    public var errorDescription: String? { message }
    public var invalidatesIdentity: Bool { code == "session_changed" || (status == 401 && code == "session") }
}
