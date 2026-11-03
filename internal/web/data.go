package web

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// Data is the /api/data answer. Lists are never null and a group without a
// price has "usd": null, never 0.
type Data struct {
	Scope             string             `json:"scope"`
	Notice            string             `json:"notice"`
	Prices            PriceSource        `json:"prices"`
	TotalUSD          float64            `json:"total_usd"`
	USDBySource       map[string]float64 `json:"usd_by_source"`
	UnpricedMessages  int64              `json:"unpriced_messages"`
	Models            []GroupData        `json:"models"`
	Tasks             []GroupData        `json:"tasks"`
	Days              []GroupData        `json:"days"`
	FilterTasks       []string           `json:"filter_tasks"`
	MultiTaskSessions []string           `json:"multi_task_sessions"`
	Projects          []GroupData        `json:"projects"`
	FailedProjects    []string           `json:"failed_projects"`
}

// PriceSource says where the prices come from and when they were taken.
type PriceSource struct {
	Source  string `json:"source"`
	Fetched string `json:"fetched"`
}

// GroupData is one line of a report.
type GroupData struct {
	Name             string   `json:"name"`
	Messages         int64    `json:"messages"`
	Input            int64    `json:"input"`
	Output           int64    `json:"output"`
	Reasoning        int64    `json:"reasoning"`
	CacheWrite5m     int64    `json:"cache_write_5m"`
	CacheWrite1h     int64    `json:"cache_write_1h"`
	CacheRead        int64    `json:"cache_read"`
	USD              *float64 `json:"usd"`
	UnpricedMessages int64    `json:"unpriced_messages"`
}

func groups(rep core.Report) []GroupData {
	out := make([]GroupData, 0, len(rep.Groups))
	for _, g := range rep.Groups {
		d := GroupData{
			Name: g.Name, Messages: g.Messages, Input: g.Input, Output: g.Output, Reasoning: g.Reasoning,
			CacheWrite5m: g.CacheWrite5m, CacheWrite1h: g.CacheWrite1h, CacheRead: g.CacheRead,
			UnpricedMessages: g.UnpricedMessages,
		}
		if g.Priced {
			usd := g.USD
			d.USD = &usd
		}
		out = append(out, d)
	}
	return out
}

func names(rep core.Report) []string {
	out := make([]string, 0, len(rep.Groups))
	for _, g := range rep.Groups {
		out = append(out, g.Name)
	}
	return out
}

// request is a validated /api/data query.
type request struct {
	scope string
	query core.Query
}

// parseRequest accepts scope (project or all), task and since, each at most
// once; anything else is refused.
func parseRequest(rawQuery string) (request, bool) {
	vals, err := url.ParseQuery(rawQuery)
	if err != nil {
		return request{}, false
	}
	req := request{scope: "project", query: core.Query{By: "task"}} // By: any valid grouping, only the filters are checked
	for key, v := range vals {
		if len(v) != 1 || !req.set(key, v[0]) {
			return request{}, false
		}
	}
	return req, (req.scope == "project" || req.scope == "all") && req.query.Validate() == nil
}

// set stores one query parameter; false means the parameter is unknown.
func (r *request) set(key, value string) bool {
	switch key {
	case "scope":
		r.scope = value
	case "task":
		r.query.Task = value
	case "since":
		r.query.Since = value
	default:
		return false
	}
	return true
}

func (s *server) data(w http.ResponseWriter, r *http.Request) {
	req, ok := parseRequest(r.URL.RawQuery)
	if !ok {
		jsonError(w, r, http.StatusBadRequest, "bad request")
		return
	}
	loaded, err := s.load(r.Context(), req.scope)
	if err != nil { // the cause may hold a path: it is never sent
		jsonError(w, r, http.StatusInternalServerError, "cannot read data")
		return
	}
	body, err := json.Marshal(s.build(req, loaded))
	if err != nil {
		jsonError(w, r, http.StatusInternalServerError, "cannot read data")
		return
	}
	reply(w, r, jsonType, append(body, '\n'))
}

const jsonType = "application/json; charset=utf-8"

func jsonError(w http.ResponseWriter, r *http.Request, code int, msg string) {
	body, _ := json.Marshal(map[string]string{"error": msg})
	w.Header().Set("Content-Type", jsonType)
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		w.Write(append(body, '\n'))
	}
}

// build reports on the loaded projects with the same aggregation as
// "kanjo usage", so the page and the CLI cannot disagree.
func (s *server) build(req request, loaded Loaded) Data {
	rows, sessions := core.Flatten(loaded.Projects)
	dash := core.NewDashboard(rows, sessions, s.book, req.query)
	bySource := dash.Models.USDBySource
	if bySource == nil {
		bySource = map[string]float64{}
	}
	d := Data{
		Scope: req.scope, Notice: core.EstimateNotice,
		Prices:   PriceSource{Source: s.book.Source(), Fetched: s.book.Fetched()},
		TotalUSD: dash.Models.TotalUSD, USDBySource: bySource, UnpricedMessages: dash.Models.UnpricedMessages,
		Models: groups(dash.Models), Tasks: groups(dash.Tasks), Days: groups(dash.Days),
		FilterTasks:       names(core.Aggregate(rows, sessions, s.book, core.Query{By: "task"})),
		MultiTaskSessions: append([]string{}, dash.Tasks.MultiLabelSessions...),
		Projects:          []GroupData{},
		FailedProjects:    append([]string{}, loaded.Failed...),
	}
	if req.scope == "all" {
		q := req.query
		q.By = "project"
		d.Projects = groups(core.AggregateProjects(loaded.Projects, s.book, q))
	}
	return d
}
