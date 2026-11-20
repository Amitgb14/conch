import ConchKit
import Foundation

// Settings → Agents' Agent accounts: what `conch account` does, on each
// machine's own server (agent.accounts.v1).
extension Store {
    /// Whether machine's server keeps agent accounts.
    func keepsAccounts(on machine: String) -> Bool {
        conns[machine]?.hello?.capabilities?.contains(accountsCapability) == true
    }

    /// Asks machine for its accounts again.
    func loadAccounts(on machine: String) {
        guard let c = conns[machine], keepsAccounts(on: machine) else { return }
        Task {
            guard let l = try? await c.call("account.list", as: AgentAccountList.self) else { return }
            agentAccounts[machine] = l
        }
    }

    /// Runs one account call on machine, says how it went, and reads the
    /// accounts again either way so the page shows what is so.
    private func accountCall(on machine: String, done: String, _ call: @escaping (Connection) async throws -> Void) {
        guard let c = conns[machine] else {
            say("\(label(machine)) is not connected", .err)
            return
        }
        Task {
            do {
                try await call(c)
                say(done)
            } catch {
                say("\(error.localizedDescription)", .err)
            }
            loadAccounts(on: machine)
        }
    }

    /// Adds agent's account called name, in dir or a folder of conch's,
    /// and offers to sign in to it.
    func addAccount(agent: String, name: String, dir: String?, on machine: String, signIn: Bool) {
        accountCall(on: machine, done: "Added \(agent)'s \(name) account") { c in
            let d = dir?.trimmingCharacters(in: .whitespaces)
            try await c.call("account.add", AccountAddParams(agent: agent, name: name, dir: d?.isEmpty == false ? d : nil))
            if signIn { await MainActor.run { self.signIn(agent: agent, account: name, on: machine) } }
        }
    }

    /// Forgets an account; its folder, sign-in and conversations stay.
    func removeAccount(agent: String, name: String, on machine: String) {
        accountCall(on: machine, done: "Removed \(agent)'s \(name) account; its folder is left where it was") { c in
            try await c.call("account.remove", AccountRefParams(agent: agent, name: name))
        }
    }

    /// Pins path to the account called name (any agent's of that name).
    func pinAccount(path: String, name: String, on machine: String) {
        accountCall(on: machine, done: "Agents started in \((path as NSString).lastPathComponent) use \(name)") { c in
            try await c.call("account.pin", AccountPinParams(path: path, account: name))
        }
    }

    /// Takes path's pin off.
    func unpinAccount(path: String, on machine: String) {
        accountCall(on: machine, done: "Unpinned \((path as NSString).lastPathComponent)") { c in
            try await c.call("account.pin", AccountPinParams(path: path))
        }
    }

    /// Sets the failover line, 0 for off: every agent's, or with agent that
    /// agent's own; shared puts the agent back on every agent's line.
    func setAccountFailover(_ at: Int, agent: String? = nil, shared: Bool = false, on machine: String) {
        let who = agent.map { "\(agentTitle($0))'s new agents" } ?? "New agents"
        let done = shared ? "\(agentTitle(agent ?? "")) follows every agent's failover line"
            : at > 0 ? "\(who) skip an account past \(at)%" : agent == nil ? "Failover is off" : "Failover is off for \(agentTitle(agent!))"
        accountCall(on: machine, done: done) { c in
            try await c.call("account.failover", AccountFailoverParams(at: at, agent: agent, shared: shared ? true : nil))
        }
    }

    /// Whether machine's server keeps a failover line per agent.
    func failoverPerAgent(on machine: String) -> Bool {
        conns[machine]?.hello?.capabilities?.contains(accountFailoverAgentCapability) == true
    }

    /// An agent's name as people know it.
    func agentTitle(_ agent: String) -> String {
        ["claude": "Claude Code", "codex": "Codex"][agent] ?? agent
    }

    /// Asks machine what each account used, with its hours and days; a
    /// server without it leaves the panel to the plan alone.
    func loadAccountUsage(on machine: String) {
        guard let c = conns[machine], c.hello?.capabilities?.contains(usageAccountsCapability) == true else { return }
        Task {
            struct P: Encodable { var by_account = true }
            guard let h = try? await c.call("usage.history", P(), as: UsageHistory.self) else { return }
            accountUsage[machine] = h
        }
    }

    /// Everything the status bar's usage shows, for this computer: its
    /// accounts with their plans, usage and listing.
    var usageAccountsHere: [UsageAccount] {
        usageAccounts(limits: limits, history: accountUsage["local"], list: agentAccounts["local"])
    }

    /// The agent in view and the account it runs on, when the focused pane
    /// is this computer's and runs one.
    var agentInView: (agent: String, account: String?)? {
        guard let k = layout.focused, unkey(k).machine == "local", let p = pane(k), let a = p.agent?.name, !a.isEmpty else { return nil }
        return (a, p.account)
    }

    /// The usage panel's data asked for again, as it opens.
    func refreshAccountUsage() {
        refreshLimits()
        loadAccounts(on: "local")
        loadAccountUsage(on: "local")
    }

    /// Starts agent on account in machine's home, to sign in once with
    /// its own /login, and shows it.
    func signIn(agent: String, account: String, on machine: String) {
        guard let c = conns[machine] else { return }
        Task {
            struct P: Encodable { var agent: String; var account: String; var no_project: Bool; var cols: Int?; var rows: Int? }
            let size = newPaneSize()
            do {
                let info = try await c.call("pane.create", P(agent: agent, account: account, no_project: true, cols: size?.cols, rows: size?.rows), as: PaneInfo.self)
                open(ConchKit.key(machine, info.id))
                // Sign In is pressed in Settings, in front of the window the
                // pane opens in: bring that window forward, or the agent
                // waits for its /login behind Settings unseen.
                AppWindows.shared.bringForward(self)
                focusTerminal()
                say("Started \(agentTitle(agent)) on \(account): sign in there with /login")
            } catch {
                say("Could not start \(agent) on \(account): \(error.localizedDescription)", .err)
            }
        }
    }
}
