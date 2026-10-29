// Package reaper cleans up the zombies that gotty adopts when it runs as PID 1.
//
// In the Docker image gotty is the container's first process, so any process
// that loses its parent (the program a terminal started, what a shell left
// behind, the command nsenter forked) is handed to gotty. A process that ends
// stays a zombie until its parent waits for it, and gotty waits only for the
// children it started itself. Without this, every orphan that ends is listed
// as <defunct> for as long as the server runs.
//
// The reaper does not wait for "any child": that would take the exit status
// from a child whose own Wait is about to read it. It looks for zombies whose
// parent is this process and reaps one only after it has been seen as a zombie
// on two sweeps in a row, which a child that is waited for never is (its Wait
// returns within milliseconds), and it waits for that exact PID.
package reaper

import (
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Interval is how often the reaper looks.
const Interval = 3 * time.Second

// Start runs the reaper in the background when this process is PID 1. Anywhere
// else something else is the init and it does nothing.
func Start() {
	if os.Getpid() != 1 {
		return
	}
	log.Println("reaper: running as PID 1, orphans that end will be reaped")
	go run(Interval, os.Getpid(), zombiesOf, reap, log.Printf)
}

func run(every time.Duration, self int, zombies func(parent int) []int, reapOne func(pid int) bool, logf func(string, ...interface{})) {
	s := &sweeper{seen: map[int]bool{}}
	for range time.Tick(every) {
		for _, pid := range s.sweep(zombies(self), reapOne) {
			logf("reaper: reaped the orphan %d", pid)
		}
	}
}

// sweeper remembers the zombies of the last sweep.
type sweeper struct {
	seen map[int]bool
}

// sweep reaps the zombies that were also there at the last sweep, remembers the
// new ones, and returns the PIDs it reaped. A PID that is gone is forgotten.
func (s *sweeper) sweep(now []int, reapOne func(pid int) bool) []int {
	var reaped []int
	next := map[int]bool{}
	for _, pid := range now {
		if s.seen[pid] {
			if reapOne(pid) {
				reaped = append(reaped, pid)
			}
			continue
		}
		next[pid] = true
	}
	s.seen = next
	return reaped
}

// reap waits for one exact child without blocking; false if it was not there to be reaped.
func reap(pid int) bool {
	var status syscall.WaitStatus
	got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	return err == nil && got == pid
}

// zombiesOf lists the zombies whose parent is parent, from /proc.
func zombiesOf(parent int) []int {
	entries, err := ioutil.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := ioutil.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		if state, ppid, ok := parseStat(string(data)); ok && state == 'Z' && ppid == parent {
			out = append(out, pid)
		}
	}
	return out
}

// parseStat reads the state and the parent from a /proc/<pid>/stat line:
// "pid (name) S ppid ...". The name may hold spaces and brackets, so the line
// is read from its last ")".
func parseStat(line string) (state byte, ppid int, ok bool) {
	end := strings.LastIndex(line, ")")
	if end < 0 {
		return 0, 0, false
	}
	fields := strings.Fields(line[end+1:])
	if len(fields) < 2 || len(fields[0]) != 1 {
		return 0, 0, false
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, false
	}
	return fields[0][0], n, true
}
