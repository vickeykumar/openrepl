package server

import (
	"strings"
	"testing"
	"time"
	"utils"
)

// The dashboard's Jobs view: the recurring jobs with their state, and the removals that wait.

func TestTheJobsViewListsTheRecurringJobsAndTheirState(t *testing.T) {
	_, h := adminTestServer(t)
	saved := utils.Jobs
	utils.Jobs = &utils.JobRegistry{}
	t.Cleanup(func() { utils.Jobs = saved })

	fine := utils.Jobs.Register("fine", "Fine", "does a thing", time.Minute)
	fine.Record(time.Now(), "2 things done", nil)
	broken := utils.Jobs.Register("broken", "Broken", "fails", time.Minute)
	broken.Record(time.Now(), "", errTest("the disk is full"))
	utils.Jobs.Register("new", "New", "has not run yet", time.Minute)
	utils.Jobs.RegisterOff("off", "Off", "is off", "not set")

	var r jobsReply
	decode(t, get(h, "/admin/jobs"), &r)
	state := map[string]utils.JobInfo{}
	for _, j := range r.Jobs {
		state[j.ID] = j
	}
	if len(r.Jobs) != 4 || r.Jobs[0].ID != "fine" {
		t.Fatalf("jobs: %+v", r.Jobs)
	}
	if j := state["fine"]; j.State != utils.JobOK || j.Runs != 1 || j.LastResult != "2 things done" || j.EverySec != 60 || j.NextAt <= j.LastAt {
		t.Errorf("fine: %+v", j)
	}
	if j := state["broken"]; j.State != utils.JobFailing || j.Failures != 1 || j.LastError != "the disk is full" {
		t.Errorf("broken: %+v", j)
	}
	if j := state["new"]; j.State != utils.JobWaiting || j.Runs != 0 {
		t.Errorf("new: %+v", j)
	}
	if j := state["off"]; j.State != utils.JobOff || j.Off != "not set" || j.NextAt != 0 {
		t.Errorf("off: %+v", j)
	}
	if utils.Worst(r.Jobs) != utils.JobFailing {
		t.Error("a failing job is the worst state")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestAJobThatHasBeenQuietForLongerThanItsIntervalIsLate(t *testing.T) {
	reg := &utils.JobRegistry{}
	j := reg.Register("slow", "Slow", "", time.Minute)
	j.Record(time.Now().Add(-5*time.Minute), "ok", nil)
	if s := reg.Snapshot(time.Now()); s[0].State != utils.JobLate {
		t.Fatalf("5 minutes of quiet for a job every minute: %+v", s[0])
	}
	j.Record(time.Now(), "ok", nil)
	if s := reg.Snapshot(time.Now()); s[0].State != utils.JobOK || utils.Worst(s) != utils.JobOK {
		t.Fatalf("after a run: %+v", s[0])
	}
	reg.Register("slow", "Slow", "", time.Minute) // the same id replaces it
	if s := reg.Snapshot(time.Now()); len(s) != 1 || s[0].Runs != 0 {
		t.Fatalf("a second registration: %+v", s)
	}
}

func TestTheJobsViewListsTheRemovalsThatWaitWithoutTheirPaths(t *testing.T) {
	_, h := adminTestServer(t)
	saved := utils.GottyJobs
	utils.GottyJobs = utils.NewJobScheduler()
	t.Cleanup(func() { utils.GottyJobs = saved })

	for i, name := range []string{"guest-300", "guest-100", "guest-200"} {
		utils.GottyJobs.AddJob(utils.REMOVE_JOB_KEY+"/tmp/home/"+name, time.Duration(i+1)*time.Hour, func() {})
	}
	utils.GottyJobs.AddJob("SOMETHING-else", 30*time.Minute, func() {})
	defer func() {
		for _, p := range utils.GottyJobs.Waiting() {
			utils.GottyJobs.RemoveJob(p.Name)
		}
	}()

	var r jobsReply
	body := get(h, "/admin/jobs")
	decode(t, body, &r)
	if strings.Contains(body.Body.String(), "/tmp/home") {
		t.Errorf("a path is in the reply: %s", body.Body.String())
	}
	if r.Removals.Pending != 4 || r.Removals.NextAt == 0 || r.Removals.Fired != 0 {
		t.Fatalf("removals: %+v", r.Removals)
	}
	// the folders, the soonest first; the job that is not a removal is counted but not listed
	if len(r.Removals.Items) != 3 || r.Removals.Items[0].Folder != "guest-300" || r.Removals.Items[2].Folder != "guest-200" {
		t.Errorf("items: %+v", r.Removals.Items)
	}
}

func TestTheRemovalsListIsShortAndTheCountIsNot(t *testing.T) {
	saved := utils.GottyJobs
	utils.GottyJobs = utils.NewJobScheduler()
	t.Cleanup(func() { utils.GottyJobs = saved })
	for i := 0; i < maxRemovalsListed+15; i++ {
		utils.GottyJobs.AddJob(utils.REMOVE_JOB_KEY+"/tmp/home/guest-"+strings.Repeat("x", i+1), time.Hour, func() {})
	}
	defer func() {
		for _, p := range utils.GottyJobs.Waiting() {
			utils.GottyJobs.RemoveJob(p.Name)
		}
	}()
	r := currentJobs(time.Now())
	if r.Removals.Pending != maxRemovalsListed+15 || len(r.Removals.Items) != maxRemovalsListed {
		t.Fatalf("%d pending, %d listed", r.Removals.Pending, len(r.Removals.Items))
	}
}

func TestACountOfRemovalsThatRanOut(t *testing.T) {
	js := utils.NewJobScheduler()
	js.AddJob("REMOVE-a", 10*time.Millisecond, func() {})
	for i := 0; i < 100 && js.Stats().Fired == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	st := js.Stats()
	if st.Fired != 1 || st.Pending != 0 || st.LastFired.IsZero() {
		t.Fatalf("stats: %+v", st)
	}
}
