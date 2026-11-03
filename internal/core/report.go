package core

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Query selects and groups usage rows.
type Query struct {
	By    string // task, model, day or session
	Task  string // "" = all tasks; a label also selects its sub-tasks (label/...)
	Since string // YYYY-MM-DD inclusive, "" = all days
}

// Validate checks the query fields.
func (q Query) Validate() error {
	switch q.By {
	case "task", "model", "day", "session":
	default:
		return fmt.Errorf("invalid --by %q: want task, model, day or session", q.By)
	}
	return q.validateFilters()
}

// ValidateAll checks a query over several projects: session ids can collide
// across projects, so --by session is not offered, and project is.
func (q Query) ValidateAll() error {
	switch q.By {
	case "project", "task", "model", "day":
	default:
		return fmt.Errorf("invalid --by %q: want project, task, model or day with --all", q.By)
	}
	return q.validateFilters()
}

func (q Query) validateFilters() error {
	if q.Task != "" && !ValidTask(q.Task) {
		return fmt.Errorf("invalid --task %q", q.Task)
	}
	if q.Since != "" && !ValidDay(q.Since) {
		return fmt.Errorf("invalid --since %q: want YYYY-MM-DD", q.Since)
	}
	return nil
}

// Group is one line of a report.
type Group struct {
	Name     string
	Messages int64
	Tokens
	USD              float64
	Priced           bool // false when no row of the group has a price
	UnpricedMessages int64
}

// Report is the result of Aggregate.
type Report struct {
	Groups             []Group
	TotalUSD           float64
	USDBySource        map[string]float64 // priced cost per row source (claude, junior, ...)
	UnpricedMessages   int64
	MultiLabelSessions []string // sessions that mentioned several task labels
}

// taskRoot cuts a task label at its first "/".
func taskRoot(task string) string {
	root, _, _ := strings.Cut(task, "/")
	return root
}

func (q Query) groupName(r UsageRow) string {
	switch q.By {
	case "model":
		return r.Model
	case "day":
		return r.Day
	case "session":
		return r.Session
	}
	return taskRoot(r.Task)
}

func (q Query) match(r UsageRow) bool {
	onTask := q.Task == "" || r.Task == q.Task || strings.HasPrefix(r.Task, q.Task+"/")
	return onTask && (q.Since == "" || r.Day >= q.Since)
}

// Aggregate filters rows by q, groups them by q.By, and prices them. Rows whose
// model has no price are counted as unpriced and add nothing to the totals.
func Aggregate(rows []UsageRow, sessions []SessionInfo, pb PriceBook, q Query) Report {
	multi := map[sessionRef]bool{}
	for _, s := range sessions {
		if s.LabelsSeen > 1 {
			multi[sessionRef{s.Source, s.Session}] = true
		}
	}
	groups := map[string]*Group{}
	flagged := map[string]struct{}{}
	var rep Report
	for _, r := range rows {
		if !q.match(r) {
			continue
		}
		name := q.groupName(r)
		if groups[name] == nil {
			groups[name] = &Group{Name: name}
		}
		groups[name].add(r, pb, &rep)
		if multi[sessionRef{r.Source, r.Session}] {
			flagged[r.Session] = struct{}{}
		}
	}
	for _, g := range groups {
		rep.Groups = append(rep.Groups, *g)
	}
	slices.SortFunc(rep.Groups, func(a, b Group) int { return strings.Compare(a.Name, b.Name) })
	rep.MultiLabelSessions = slices.Sorted(maps.Keys(flagged))
	return rep
}

// add folds one row into the group and the report totals.
func (g *Group) add(r UsageRow, pb PriceBook, rep *Report) {
	g.Messages += r.Messages
	g.Tokens = g.Tokens.Add(r.Tokens)
	usd, priced := Cost(r, pb)
	if !priced {
		g.UnpricedMessages += r.Messages
		rep.UnpricedMessages += r.Messages
		return
	}
	g.USD += usd
	g.Priced = true
	rep.TotalUSD += usd
	if rep.USDBySource == nil {
		rep.USDBySource = map[string]float64{}
	}
	rep.USDBySource[r.Source] += usd
}

type sessionRef struct{ source, session string }
