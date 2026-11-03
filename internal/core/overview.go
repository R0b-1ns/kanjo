package core

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
