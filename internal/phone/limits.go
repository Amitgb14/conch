package phone

import (
	"cmp"
	"slices"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// Limits is GET /api/limits: how much of each agent account's plan is
// used, as the agents last reported it (agent.limits), on one machine.
// Limits is never null; an agent that has not reported is not in it.
type Limits struct {
	Machine string       `json:"machine"`
	Limits  []PlanLimits `json:"limits"`
}

// PlanLimits is one agent account's windows, under the names the agent
// gave them (five_hour, seven_day, seven_day_fable, spend_limit, primary…).
type PlanLimits struct {
	Agent   string        `json:"agent"`
	Account string        `json:"account,omitempty"`
	Windows []LimitWindow `json:"windows"`
	At      time.Time     `json:"at"`
}

// LimitWindow is one window: used_pct is 0-100, above 100 once exceeded.
type LimitWindow struct {
	Key      string     `json:"key"`
	UsedPct  float64    `json:"used_pct"`
	ResetsAt *time.Time `json:"resets_at,omitempty"`
}

// limits answers for this computer only for now, as /api/resources does.
func (g *Gateway) limits(rq *request) (any, *APIError) {
	id := rq.r.URL.Query().Get("machine")
	local := id == "" || id == LocalMachine
	c, machine, aerr := g.machineFor(rq.dev, id)
	switch {
	case aerr != nil && (local || aerr.Code != CodeServerUnavailable):
		return nil, aerr
	case !local:
		return nil, apiErr(CodeServerUnavailable, "plan limits are only available for this computer for now")
	}
	var res proto.AgentLimitsResult
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodAgentLimits, nil, &res); aerr != nil {
		return nil, aerr
	}
	return buildLimits(machine, res.Limits), nil
}

// buildLimits is agent.limits for the phone: the named windows when the
// agent gave them, else the three conch knows, sorted by agent and account.
func buildLimits(machine string, in []proto.PlanLimits) Limits {
	out := Limits{Machine: machine, Limits: []PlanLimits{}}
	for _, l := range in {
		p := PlanLimits{Agent: l.Agent, Account: l.Account, Windows: []LimitWindow{}, At: l.At.UTC()}
		if len(l.Windows) > 0 {
			for _, w := range l.Windows {
				p.Windows = append(p.Windows, limitWindow(w.Key, w.UsedPct, w.ResetsAt))
			}
		} else {
			for _, w := range []struct {
				key string
				w   *proto.LimitWindow
			}{{"five_hour", l.FiveHour}, {"seven_day", l.Week}, {"spend_limit", l.Spend}} {
				if w.w != nil {
					p.Windows = append(p.Windows, limitWindow(w.key, w.w.UsedPct, w.w.ResetsAt))
				}
			}
		}
		if len(p.Windows) > 0 {
			out.Limits = append(out.Limits, p)
		}
	}
	slices.SortFunc(out.Limits, func(a, b PlanLimits) int {
		return cmp.Or(cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.Account, b.Account))
	})
	return out
}

func limitWindow(key string, used float64, resets time.Time) LimitWindow {
	w := LimitWindow{Key: key, UsedPct: used}
	if !resets.IsZero() {
		r := resets.UTC()
		w.ResetsAt = &r
	}
	return w
}
