package utils

import (
    "fmt"
    "log"
    "sync"
    "time"
    "strings"
    "os"
    "encoding/gob"
)

type Job struct {
    Name            string          // name of the job
    ExpirationTime  int64           // Expiration time in unix miliseconds, will need this to calculate the duration of the timer expiry
    timer           *time.Timer     // timer created for this job
}

type JobScheduler struct {
    Jobs  map[string]*Job   // map of job name to job object
    mutex sync.Mutex        // mutex for read write in job scheduler

    fired     int64     // jobs that have run out since the start (RemovalStats)
    lastFired time.Time
}

func NewJobScheduler() *JobScheduler {
    return &JobScheduler{
        Jobs: make(map[string]*Job),
    }
}


// schedule a job after certain duration from now
func (js *JobScheduler) AddJob(name string, durationafter time.Duration, jobFunc func()) {

    // The lock is held while the timer is made and the job is put in the table, so that a timer that
    // fires at once finds its job there when it takes the lock to forget it.
    js.mutex.Lock()
    defer js.mutex.Unlock()
    job := &Job{
        Name:           name,
        ExpirationTime: int64(GetUnixMilli()+int64(durationafter/time.Millisecond)), // expiration time will be that duration from now
    }
    job.timer = time.AfterFunc(durationafter, func() {
        log.Println("running job on timer expiry: ", name)
        jobFunc()
        js.noteFired()
        js.forget(job)
    })
    js.Jobs[name] = job

    log.Println("job installed successfully for : ", name, " After (duration ns): ", durationafter)
}

// forget takes a job that has run out of the table, unless the name has a newer job by now (a visit
// that came at the very moment the old one fired): that one is not the old one's to delete.
func (js *JobScheduler) forget(job *Job) {
    js.mutex.Lock()
    defer js.mutex.Unlock()
    if js.Jobs[job.Name] == job {
        delete(js.Jobs, job.Name)
    }
}

// Pending reports whether a job of that name is waiting.
func (js *JobScheduler) Pending(name string) bool {
    js.mutex.Lock()
    defer js.mutex.Unlock()
    _, ok := js.Jobs[name]
    return ok
}

func (js *JobScheduler) RemoveJob(name string) {
    js.mutex.Lock()
    defer js.mutex.Unlock()

    if job, ok := js.Jobs[name]; ok {
        job.timer.Stop()
        delete(js.Jobs, name)
    }
}

func (js *JobScheduler) ResetJob(name string, durationafter time.Duration, jobFunc func()) {
    js.RemoveJob(name)  // remove old job
    js.AddJob(name, durationafter, jobFunc)
    log.Println("job successfully Reset for : ", name, " After (duration ns): ", durationafter)
}

// Load jobs from a file, and run the common jobfunction on all the jobs
func (js *JobScheduler) LoadJobsFromFile(file string, jobFunc func(jobname string)) error {
    f, err := os.Open(file)
    if err != nil {
        return err
    }
    defer f.Close()

    // read into a table of its own: a job already scheduled in this process is newer than the file's
    var loaded map[string]*Job
    if err = gob.NewDecoder(f).Decode(&loaded); err != nil {
        return err
    }

    js.mutex.Lock()
    defer js.mutex.Unlock()
    for name, job := range loaded {
        if _, scheduled := js.Jobs[name]; scheduled || job == nil {
            continue
        }
        job.Name = name
        // remaining duration milisecond; a job whose time came while the server was down runs at once
        remainingDuration := time.Duration(job.ExpirationTime-GetUnixMilli()) * time.Millisecond
        j := job
        j.timer = time.AfterFunc(remainingDuration, func() {
            if jobFunc != nil {
                jobFunc(j.Name) // run the job with the jobname
            }
            js.noteFired()
            js.forget(j)
        })
        js.Jobs[name] = j
        log.Println("job installed successfully for : ", name, "After (duration ns): ", remainingDuration)
    }

    return nil
}

// Save jobs to a file. It is written beside the file and moved over it, so that a stop in the middle
// of the write leaves the file that was there and not half of a new one.
func (js *JobScheduler) SaveJobsToFile(file string) error {
    js.mutex.Lock()
    defer js.mutex.Unlock()

    tmp := file + ".tmp"
    f, err := os.Create(tmp)
    if err != nil {
        return err
    }
    err = gob.NewEncoder(f).Encode(js.Jobs)
    if cerr := f.Close(); err == nil {
        err = cerr
    }
    if err != nil {
        os.Remove(tmp)
        return err
    }
    return os.Rename(tmp, file)
}

// JobSaveInterval is how often StartJobSaver saves what is waiting (a variable for the tests).
var JobSaveInterval = time.Minute

// StartJobSaver saves the waiting jobs every JobSaveInterval. They used to be saved only when the server
// was stopped properly, so a crash, an out of memory kill or a forced stop lost every removal that was
// waiting and the folders stayed. Now the most that is lost is the last minute. It returns what stops it.
func (js *JobScheduler) StartJobSaver(file string) (stop func()) {
    done := make(chan struct{})
    report := Jobs.Register("job-saver", "Waiting removals saved", "Writes the guest folders that wait to be removed to the job file, so that a crash does not lose them.", JobSaveInterval)
    go func() {
        ticker := time.NewTicker(JobSaveInterval)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                report.Run(func() (string, error) {
                    err := js.SaveJobsToFile(file)
                    if err != nil {
                        log.Println("Error: saving the waiting jobs: ", err)
                    }
                    return fmt.Sprintf("saved %d waiting removals", js.Stats().Pending), err
                })
            case <-done:
                return
            }
        }
    }()
    return func() { close(done) }
}

// gotty JObScheduler
var GottyJobs *JobScheduler

func InitGottyJobs() {
    GottyJobs = NewJobScheduler()
    err := GottyJobs.LoadJobsFromFile(GOTTY_PATH+"/"+JobFile, func(jobname string ) {
	log.Println("running job on timer expiry: ", jobname)
        // removal of pending guest directories
        if strings.HasPrefix(jobname, REMOVE_JOB_KEY) {
            parts := strings.SplitAfter(jobname, REMOVE_JOB_KEY)
            dirpath := parts[1]
            RemoveDir(dirpath)
        }
    })
    if err != nil {
        log.Println("Error: loading Jobs: ", err)
    }
    // Delete the job file after loading
    os.Remove(GOTTY_PATH+"/"+JobFile)
}
