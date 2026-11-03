package core

import (
	"maps"
	"slices"
)

// EstimateNotice must accompany every displayed cost (spec section 7).
const EstimateNotice = "Claude cost is an estimate at API list prices, not a subscription bill."

// Dashboard is what the web page shows: the same rows grouped three ways.
// Each Report carries the same totals.
type Dashboard struct{ Models, Tasks, Days Report }

// NewDashboard aggregates rows by model, task and day with q's filters; q.By
// is ignored.
func NewDashboard(rows []UsageRow, sessions []SessionInfo, pb PriceBook, q Query) Dashboard {
	by := func(name string) Report {
		q.By = name
		return Aggregate(rows, sessions, pb, q)
	}
	return Dashboard{Models: by("model"), Tasks: by("task"), Days: by("day")}
}

// ProjectUsage is the saved usage of one project.
type ProjectUsage struct {
	Name     string
	Rows     []UsageRow
	Sessions []SessionInfo
}

// Flatten concatenates the rows and sessions of several projects.
func Flatten(ps []ProjectUsage) ([]UsageRow, []SessionInfo) {
	var rows []UsageRow
	var sessions []SessionInfo
	for _, p := range ps {
		rows = append(rows, p.Rows...)
		sessions = append(sessions, p.Sessions...)
	}
	return rows, sessions
}

// AggregateProjects reports several projects together. With q.By == "project"
// each project is one group; otherwise all rows are grouped as Aggregate does.
// shortcut: multi-task session ids are not qualified by project; qualify them
// if two projects ever reuse a session id.
func AggregateProjects(ps []ProjectUsage, pb PriceBook, q Query) Report {
	if q.By != "project" {
		rows, sessions := Flatten(ps)
		return Aggregate(rows, sessions, pb, q)
	}
	q.By = "task"
	var out Report
	flagged := map[string]struct{}{}
	for _, p := range ps {
		rep := Aggregate(p.Rows, p.Sessions, pb, q)
		if len(rep.Groups) == 0 {
			continue
		}
		g := Group{Name: p.Name, UnpricedMessages: rep.UnpricedMessages, Priced: len(rep.USDBySource) > 0, USD: rep.TotalUSD}
		for _, tg := range rep.Groups {
			g.Messages += tg.Messages
			g.Tokens = g.Tokens.Add(tg.Tokens)
		}
		out.Groups = append(out.Groups, g)
		out.TotalUSD += rep.TotalUSD
		out.UnpricedMessages += rep.UnpricedMessages
		out.Attribution = out.Attribution.Add(rep.Attribution)
		for src, usd := range rep.USDBySource {
			if out.USDBySource == nil {
				out.USDBySource = map[string]float64{}
			}
			out.USDBySource[src] += usd
		}
		for _, s := range rep.MultiLabelSessions {
			flagged[s] = struct{}{}
		}
	}
	out.MultiLabelSessions = slices.Sorted(maps.Keys(flagged))
	return out
}
