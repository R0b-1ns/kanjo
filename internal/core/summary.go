package core

import "time"

// Summary is the totals of the run journal.
type Summary struct {
	Runs, Tasks, Unlabelled, Launches, Retries int
	Duration                                   time.Duration
	Script                                     map[RunVerdict]int
	Lead                                       map[LeadVerdict]int
	NoLead                                     int
}

// Summarize totals the runs of one task (all when task is empty) that started
// at or after since (all when zero). Tasks are the distinct labels other than
// NoTask; runs without a label are counted apart.
func Summarize(recs []RunRecord, task string, since time.Time) Summary {
	s := Summary{Script: map[RunVerdict]int{}, Lead: map[LeadVerdict]int{}}
	tasks := map[string]struct{}{}
	for _, r := range recs {
		if (task != "" && r.Task != task) || r.StartedAt.Before(since) {
			continue
		}
		s.Runs++
		s.Launches += r.Launches
		s.Retries += r.Retries
		s.Duration += r.Duration
		s.Script[r.Verdict]++
		if r.Task == NoTask {
			s.Unlabelled++
		} else {
			tasks[r.Task] = struct{}{}
		}
		if r.Lead == nil {
			s.NoLead++
		} else {
			s.Lead[r.Lead.Verdict]++
		}
	}
	s.Tasks = len(tasks)
	return s
}
