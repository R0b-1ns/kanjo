package claudecode

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// header holds the only fields read from every transcript line. Content is not
// among them: assistant text is never decoded.
type header struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	IsSidechain bool   `json:"isSidechain"`
	Message     *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *usage `json:"usage"`
	} `json:"message"`
}

type usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	CacheCreation       *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// userLine is decoded only for user messages, to scan their text for labels.
type userLine struct {
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type message struct {
	day, model string
	tokens     core.Tokens
	prompt     int64
}

// session accumulates what is read from one session's files.
type session struct {
	src     Source
	msgs    map[string]message // by message id: the last copy wins
	labels  map[string]int
	skipped int
}

func (s Source) readSession(id string, files []string) ([]core.UsageRow, core.SessionInfo, int) {
	if !core.ValidSession(id) {
		return nil, core.SessionInfo{}, len(files)
	}
	ss := &session{src: s, msgs: map[string]message{}, labels: map[string]int{}}
	for _, path := range files {
		ss.readFile(path)
	}
	task, taskSource := s.chooseTask(id, ss.labels)
	info := core.SessionInfo{Source: SourceName, Session: id, TaskSource: taskSource, LabelsSeen: len(ss.labels)}
	return s.rows(id, task, ss.msgs), info, ss.skipped
}

func (ss *session) readFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		ss.skipped++
		return
	}
	defer f.Close()
	oversized, err := ForEachLine(f, ss.src.MaxLineBytes, ss.line)
	ss.skipped += oversized
	if err != nil {
		ss.skipped++
	}
}

func (ss *session) line(raw []byte) {
	var h header
	if err := json.Unmarshal(raw, &h); err != nil {
		ss.skipped++
		return
	}
	switch h.Type {
	case "assistant":
		ss.assistant(h)
	case "user":
		if !h.IsSidechain {
			ss.user(raw)
		}
	}
}

func (ss *session) assistant(h header) {
	m := h.Message
	if m == nil || m.Usage == nil || m.ID == "" || m.Model == "<synthetic>" {
		return
	}
	msg, ok := ss.src.message(h.Timestamp, m.Model, *m.Usage)
	if !ok {
		ss.skipped++
		return
	}
	ss.msgs[m.ID] = msg
}

// message converts one usage record, reporting false when it cannot become a
// valid row (bad timestamp, model id or negative counts).
func (s Source) message(timestamp, model string, u usage) (message, bool) {
	ts, err := time.Parse(time.RFC3339, timestamp)
	if err != nil || !core.ValidModel(model) {
		return message{}, false
	}
	t := core.Tokens{Input: u.InputTokens, Output: u.OutputTokens, CacheRead: u.CacheReadTokens, CacheWrite5m: u.CacheCreationTokens}
	if cc := u.CacheCreation; cc != nil {
		t.CacheWrite5m, t.CacheWrite1h = cc.Ephemeral5m, cc.Ephemeral1h
	}
	day := ts.UTC().Format("2006-01-02")
	probe := core.UsageRow{Key: core.Key{Source: SourceName, Session: "s", Day: day, Task: core.NoTask, Model: model}, Tokens: t}
	if probe.Validate() != nil {
		return message{}, false
	}
	return message{day: day, model: model, tokens: t, prompt: t.Input + t.CacheRead + t.CacheWrite5m + t.CacheWrite1h}, true
}

func (ss *session) user(raw []byte) {
	if ss.src.TaskPattern == nil {
		return
	}
	var u userLine
	if err := json.Unmarshal(raw, &u); err != nil {
		ss.skipped++
		return
	}
	for _, text := range userTexts(u.Message.Content) {
		for _, label := range ss.src.TaskPattern.FindAllString(text, -1) {
			if core.ValidTask(label) && !slices.Contains(ss.src.TaskIgnore, label) {
				ss.labels[label]++
			}
		}
	}
}

// userTexts returns the human-written text of a user message: a plain string,
// or the "text" blocks of a block list. Tool results are not user text.
func userTexts(content json.RawMessage) []string {
	var plain string
	if json.Unmarshal(content, &plain) == nil {
		return []string{plain}
	}
	var blocks []textBlock
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "text" {
			out = append(out, b.Text)
		}
	}
	return out
}

func (s Source) chooseTask(sessionID string, labels map[string]int) (task, taskSource string) {
	var matches []string
	for prefix := range s.TaskMap {
		if strings.HasPrefix(sessionID, prefix) {
			matches = append(matches, prefix)
		}
	}
	if len(matches) > 0 {
		longest := slices.MaxFunc(matches, func(a, b string) int { return cmp.Compare(len(a), len(b)) })
		return s.TaskMap[longest], "map"
	}
	if label := mostFrequent(labels); label != "" {
		return label, "heuristic"
	}
	return core.NoTask, "none"
}

// mostFrequent returns the label with the highest count; ties go to the
// lexically smallest. It returns "" for an empty map.
func mostFrequent(labels map[string]int) string {
	if len(labels) == 0 {
		return ""
	}
	return slices.MinFunc(slices.Collect(maps.Keys(labels)), func(a, b string) int {
		return cmp.Or(cmp.Compare(labels[b], labels[a]), strings.Compare(a, b))
	})
}

func (s Source) rows(sessionID, task string, msgs map[string]message) []core.UsageRow {
	byKey := map[core.Key]*core.UsageRow{}
	for _, m := range msgs {
		long := s.LongContext != nil && s.LongContext(m.model, m.prompt)
		k := core.Key{Source: SourceName, Session: sessionID, Day: m.day, Task: task, Model: m.model, LongContext: long}
		if byKey[k] == nil {
			byKey[k] = &core.UsageRow{Key: k}
		}
		byKey[k].Messages++
		byKey[k].Tokens = byKey[k].Tokens.Add(m.tokens)
	}
	rows := make([]core.UsageRow, 0, len(byKey))
	for _, r := range byKey {
		rows = append(rows, *r)
	}
	// The task is the same on every row, so this orders by day, model, tier.
	slices.SortFunc(rows, func(a, b core.UsageRow) int {
		return strings.Compare(fmt.Sprint(a.Key), fmt.Sprint(b.Key))
	})
	return rows
}
