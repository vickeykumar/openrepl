package utils

import (
	"meters"
	"sort"
	"sync"
	"time"
)

// The recurring jobs of the server (the ones that wake up by themselves: the saver of the waiting
// removals, the sweep of forgotten guest folders, the /tmp cleaner, the reaper, the settings poll, the
// gateway's guest expiry) report each run here, so that the dashboard can say what runs, how often, when it
// last ran, what it did and whether one has stopped. The one-off removal of a guest's folder, an hour after
// the last visit, is a JobScheduler job (jobscheduler.go) and is listed from there. Nothing is stored: a
// restart starts the counts again.

// the counters of package meters that the live map reads
const (
	MeterJobRuns     = "jobs.runs"     // a run of a recurring job
	MeterJobRemovals = "jobs.removals" // a guest's folder was removed by its job
)

// RecurringJob is one job that runs again and again.
type RecurringJob struct {
	id, name, what string
	every          time.Duration
	off            string // why it is not running, when it is not

	mu         sync.Mutex
	since      time.Time
	runs       int64
	failures   int64
	lastAt     time.Time
	lastTook   time.Duration
	lastResult string
	lastErr    string
}

// JobRegistry holds the recurring jobs, in the order they registered.
type JobRegistry struct {
	mu   sync.Mutex
	jobs []*RecurringJob
}

// Jobs is the registry of this process.
var Jobs = &JobRegistry{}

// Register adds a job that should run every `every`. A job that registers again under the same id is
// replaced (a test, or a server that starts its loops again).
func (r *JobRegistry) Register(id, name, what string, every time.Duration) *RecurringJob {
	return r.add(&RecurringJob{id: id, name: name, what: what, every: every, since: time.Now()})
}

// RegisterOff lists a job that does not run here, and says why ("OPENREPL_TMP_MAX_AGE is not set").
func (r *JobRegistry) RegisterOff(id, name, what, why string) *RecurringJob {
	return r.add(&RecurringJob{id: id, name: name, what: what, off: why, since: time.Now()})
}

func (r *JobRegistry) add(j *RecurringJob) *RecurringJob {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, old := range r.jobs {
		if old.id == j.id {
			r.jobs[i] = j
			return j
		}
	}
	r.jobs = append(r.jobs, j)
	return j
}

// Record notes one run that began at start: what it did, in a few words, and the error if it failed.
func (j *RecurringJob) Record(start time.Time, result string, err error) {
	meters.Add(MeterJobRuns, 1)
	j.mu.Lock()
	defer j.mu.Unlock()
	j.runs++
	j.lastAt = start
	j.lastTook = time.Since(start)
	j.lastResult = result
	if err != nil {
		j.failures++
		j.lastErr = err.Error()
	} else {
		j.lastErr = ""
	}
}

// Run runs f and records it.
func (j *RecurringJob) Run(f func() (string, error)) {
	start := time.Now()
	result, err := f()
	j.Record(start, result, err)
}

// the state of a job
const (
	JobOK      = "ok"      // it ran when it should
	JobWaiting = "waiting" // it has not had its first run yet
	JobLate    = "late"    // it has not run for much longer than its interval
	JobFailing = "failing" // its last run failed
	JobOff     = "off"     // it does not run here
)

// JobInfo is a job as the dashboard shows it. Times are Unix seconds, 0 for never.
type JobInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	What       string `json:"what"`
	EverySec   int64  `json:"everySec"`
	State      string `json:"state"`
	Off        string `json:"off,omitempty"`
	Runs       int64  `json:"runs"`
	Failures   int64  `json:"failures"`
	LastAt     int64  `json:"lastAt"`
	LastMS     int64  `json:"lastMs"`
	LastResult string `json:"lastResult,omitempty"`
	LastError  string `json:"lastError,omitempty"`
	NextAt     int64  `json:"nextAt"`
}

// a job is late when it has been quiet for twice its interval and a margin
func late(every, quiet time.Duration) bool {
	return quiet > 2*every+10*time.Second
}

func (j *RecurringJob) info(now time.Time) JobInfo {
	j.mu.Lock()
	defer j.mu.Unlock()
	in := JobInfo{ID: j.id, Name: j.name, What: j.what, EverySec: int64(j.every / time.Second), Off: j.off,
		Runs: j.runs, Failures: j.failures, LastMS: j.lastTook.Milliseconds(), LastResult: j.lastResult, LastError: j.lastErr}
	switch {
	case j.off != "":
		in.State = JobOff
	case j.lastErr != "":
		in.State = JobFailing
	case j.runs == 0:
		in.State = JobWaiting
		if late(j.every, now.Sub(j.since)) {
			in.State = JobLate
		}
	case late(j.every, now.Sub(j.lastAt)):
		in.State = JobLate
	default:
		in.State = JobOK
	}
	if !j.lastAt.IsZero() {
		in.LastAt = j.lastAt.Unix()
	}
	if j.off == "" {
		base := j.since
		if !j.lastAt.IsZero() {
			base = j.lastAt
		}
		in.NextAt = base.Add(j.every).Unix()
	}
	return in
}

// Snapshot lists the jobs, in the order they registered.
func (r *JobRegistry) Snapshot(now time.Time) []JobInfo {
	r.mu.Lock()
	jobs := append([]*RecurringJob(nil), r.jobs...)
	r.mu.Unlock()
	out := make([]JobInfo, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.info(now))
	}
	return out
}

// Worst is the state that matters most among the jobs that run here: failing, then late, else ok.
func Worst(jobs []JobInfo) string {
	worst := JobOK
	for _, j := range jobs {
		switch j.State {
		case JobFailing:
			return JobFailing
		case JobLate:
			worst = JobLate
		}
	}
	return worst
}

// ---- the removals of the guests' folders (JobScheduler) --------------------------------------------

// PendingJob is a removal that waits for its time.
type PendingJob struct {
	Name  string
	DueAt time.Time
}

// RemovalStats says what the JobScheduler has waiting and has done since it started.
type RemovalStats struct {
	Pending   int
	Next      time.Time // the soonest due, zero when nothing waits
	Fired     int64     // jobs that have run out
	LastFired time.Time
}

// Waiting lists the jobs that wait, the soonest first.
func (js *JobScheduler) Waiting() []PendingJob {
	js.mutex.Lock()
	out := make([]PendingJob, 0, len(js.Jobs))
	for _, j := range js.Jobs {
		out = append(out, PendingJob{Name: j.Name, DueAt: time.UnixMilli(j.ExpirationTime)})
	}
	js.mutex.Unlock()
	sort.Slice(out, func(a, b int) bool { return out[a].DueAt.Before(out[b].DueAt) })
	return out
}

// Stats counts the jobs that wait and that have run.
func (js *JobScheduler) Stats() RemovalStats {
	waiting := js.Waiting()
	js.mutex.Lock()
	defer js.mutex.Unlock()
	st := RemovalStats{Pending: len(waiting), Fired: js.fired, LastFired: js.lastFired}
	if len(waiting) > 0 {
		st.Next = waiting[0].DueAt
	}
	return st
}

// noteFired counts a job that has run out.
func (js *JobScheduler) noteFired() {
	meters.Add(MeterJobRemovals, 1)
	js.mutex.Lock()
	js.fired++
	js.lastFired = time.Now()
	js.mutex.Unlock()
}
