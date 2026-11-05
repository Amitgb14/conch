import Foundation

// What the agents on a machine used over the last day, week and month,
// by model (usage.history.v1): the server reads the conversations they
// saved, so it is everything, not only what conch watched. The status
// bar's usage chip opens it, beside the plan windows.

public struct UsageTokens: Decodable, Equatable, Sendable {
    public var input: Int
    public var cacheWrite: Int
    public var cacheRead: Int
    public var output: Int
    public var messages: Int

    enum CodingKeys: String, CodingKey {
        case input, output, messages
        case cacheWrite = "cache_write"
        case cacheRead = "cache_read"
    }

    public init(input: Int = 0, cacheWrite: Int = 0, cacheRead: Int = 0, output: Int = 0, messages: Int = 0) {
        self.input = input
        self.cacheWrite = cacheWrite
        self.cacheRead = cacheRead
        self.output = output
        self.messages = messages
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        input = try c.decodeIfPresent(Int.self, forKey: .input) ?? 0
        cacheWrite = try c.decodeIfPresent(Int.self, forKey: .cacheWrite) ?? 0
        cacheRead = try c.decodeIfPresent(Int.self, forKey: .cacheRead) ?? 0
        output = try c.decodeIfPresent(Int.self, forKey: .output) ?? 0
        messages = try c.decodeIfPresent(Int.self, forKey: .messages) ?? 0
    }

    /// Every token counted.
    public var total: Int { input + cacheWrite + cacheRead + output }
}

public struct ModelUsage: Decodable, Equatable, Sendable {
    public var model: String
    public var tokens: UsageTokens

    enum CodingKeys: String, CodingKey { case model }

    public init(model: String, tokens: UsageTokens) {
        self.model = model
        self.tokens = tokens
    }

    public init(from d: Decoder) throws {
        model = try d.container(keyedBy: CodingKeys.self).decode(String.self, forKey: .model)
        tokens = try UsageTokens(from: d)
    }
}

public struct UsagePeriod: Decodable, Equatable, Sendable {
    public var hours: Int
    public var tokens: UsageTokens
    public var models: [ModelUsage]

    enum CodingKeys: String, CodingKey { case hours, models }

    public init(hours: Int, tokens: UsageTokens, models: [ModelUsage] = []) {
        self.hours = hours
        self.tokens = tokens
        self.models = models
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        hours = try c.decode(Int.self, forKey: .hours)
        models = try c.decodeIfPresent([ModelUsage].self, forKey: .models) ?? []
        tokens = try UsageTokens(from: d)
    }

    /// "24 hours", "7 days", "30 days".
    public var label: String { usagePeriodLabel(hours) }
}

public struct AgentUsage: Decodable, Equatable, Sendable {
    public var agent: String
    public var periods: [UsagePeriod]

    /// The period of this many hours, if there is one.
    public func period(_ hours: Int) -> UsagePeriod? { periods.first { $0.hours == hours } }
}

public struct UsageHistory: Decodable, Equatable, Sendable {
    public var at: Date?
    public var agents: [AgentUsage]

    enum CodingKeys: String, CodingKey { case at, agents }

    public init(at: Date? = nil, agents: [AgentUsage] = []) {
        self.at = at
        self.agents = agents
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        at = try? c.decodeIfPresent(Date.self, forKey: .at)
        agents = try c.decodeIfPresent([AgentUsage].self, forKey: .agents) ?? []
    }
}

/// The periods the server summarises, in hours, for the picker.
public let usagePeriods = [24, 7 * 24, 30 * 24]

/// A period's name as the picker says it.
public func usagePeriodLabel(_ hours: Int) -> String {
    if hours % 24 == 0 && hours >= 48 { return "\(hours / 24) days" }
    return hours == 1 ? "1 hour" : "\(hours) hours"
}

/// A line of counts: "in 1.2M · out 340k · cache 4.1M read, 210k written".
public func usageLine(_ t: UsageTokens) -> String {
    var parts = ["in " + humanCount(t.input), "out " + humanCount(t.output)]
    if t.cacheRead > 0 || t.cacheWrite > 0 {
        parts.append("cache \(humanCount(t.cacheRead)) read, \(humanCount(t.cacheWrite)) written")
    }
    return parts.joined(separator: " · ")
}

/// A model's share of a period's tokens, 0 to 1.
public func usageShare(_ part: UsageTokens, of whole: UsageTokens) -> Double {
    whole.total > 0 ? Double(part.total) / Double(whole.total) : 0
}
