package utils

import (
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
}

func NewJobScheduler() *JobScheduler {
    return &JobScheduler{
        Jobs: make(map[string]*Job),
    }
}


// schedule a job after certain duration from now
func (js *JobScheduler) AddJob(name string, durationafter time.Duration, jobFunc func()) {

    timer := time.AfterFunc(durationafter, func() {
        log.Println("running job on timer expiry: ", name)
        jobFunc()
        js.mutex.Lock()
        defer js.mutex.Unlock()
        delete(js.Jobs, name)
    })

    js.mutex.Lock()
    defer js.mutex.Unlock()
    js.Jobs[name] = &Job{
        Name:           name,
        ExpirationTime: int64(GetUnixMilli()+int64(durationafter/time.Millisecond)), // expiration time will be that duration from now
        timer:          timer,
    }

    log.Println("job installed successfully for : ", name, " After (duration ns): ", durationafter)
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

    js.mutex.Lock()
    defer js.mutex.Unlock()

    decoder := gob.NewDecoder(f)
    err = decoder.Decode(&js.Jobs)
    if err != nil {
        return err
    }

    // Reset the timers for the loaded jobs
    for _, job := range js.Jobs {
        // remaining duration milisecond
        remainingDuration := time.Duration(job.ExpirationTime - GetUnixMilli())*time.Millisecond
        jobname := job.Name
        job.timer = time.AfterFunc(remainingDuration, func() {
            if jobFunc!=nil {
                jobFunc(jobname)   // run the job with the jobname
            }
            js.mutex.Lock()
            defer js.mutex.Unlock()
            delete(js.Jobs, jobname)
        })
        log.Println("job installed successfully for : ", job.Name, "After (duration ns): ", remainingDuration)
    }

    return nil
}

// Save jobs to a file
func (js *JobScheduler) SaveJobsToFile(file string) error {
    js.mutex.Lock()
    defer js.mutex.Unlock()

    f, err := os.Create(file)
    if err != nil {
        return err
    }
    defer f.Close()

    encoder := gob.NewEncoder(f)
    err = encoder.Encode(js.Jobs)
    if err != nil {
        return err
    }

    return nil
}

// gotty JObScheduler
var GottyJobs *JobScheduler

func InitGottyJobs() {
    GottyJobs = NewJobScheduler()
    err := GottyJobs.LoadJobsFromFile(GOTTY_PATH+"/"+JobFile, func(jobname string ) {
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
