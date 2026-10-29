package containers

import (
	"log"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"utils"
)

var Containers = make(map[string]*container)

// HasCAPSysAdmin is detected once during startup. It controls whether forked
// terminals can request the parent process's namespaces through nsenter.
var HasCAPSysAdmin bool

const MAX_MEMORY_LIMIT = 2564 // Max memory limits in MBs
const MB = 1024 * 1024

// Command Name to memory limit in MB (megabytes).
var Commands2memLimitMap = map[string]int64{
	"cling":     192, // Cling 1.2 on arm64 starts near 123 MB (it was 22 for the old minimal x86 build; threshold : 11)
	"gomacro":   45,  // the weight of the Go REPL before gomacro: measure it with the image
	"yaegi":     48,  // starts near 16 MB
	"python":    32,  // Python 3: it starts at 6 MB, the rest is for the user's code
	"python2.7": 16,  // starts near 5 MB
	"bash":      0,   // set in init: the largest limit of the others, since a shell starts any of them
	"ipython":   96,
	"ipython3":  96,  // starts near 40 MB
	"irb":       64,  // starts near 17 MB
	"perli":     32,  // starts near 8 MB
	"node":      64,  // starts near 29 MB
	"jq-repl":   8,   // 2 was killed at start; the sandbox helper that becomes it peaks near 8 MB, so 8 is the least
	"tclsh":     8,   // starts near 3 MB
	"java":      192, // jvm takes lot of memory: jshell, tuned (utils/interactive.go), peaks near 150 MB; Run uses javac and java
	"evcxr":     50,  // rust REPL
	"sqlite3":   10,
	"ts-node":   192, // starts near 104 MB: it loads the TypeScript compiler
	"rappel":    8,   // as jq-repl
}

// A bash console can start any other REPL, and what it starts runs in its cgroup,
// so its limit is the largest of the others. It follows the table: change a
// limit above and bash changes with it.
func init() {
	var largest int64
	for name, mb := range Commands2memLimitMap {
		if name != "bash" && mb > largest {
			largest = mb
		}
	}
	Commands2memLimitMap["bash"] = largest
}

var memLimitMutex sync.Mutex

// To get wieght of a command in MB.
func GetCommandWieght(command string) int64 {
	memLimitMutex.Lock()
	defer memLimitMutex.Unlock()
	w, ok := Commands2memLimitMap[command]
	if ok {
		return w
	}
	return 0
}

func AddContainerAttributes(name string, containerAttribs *syscall.SysProcAttr, params url.Values) {
	if containerAttribs == nil {
		containerAttribs = &syscall.SysProcAttr{}
	}
	containerobj, ok := Containers[name]
	if !ok {
		log.Println("ERROR: couldn't find container for : " + name)
		return
	}
	containerobj.AddContainerAttributes(containerAttribs, params)
	log.Println("INFO: Added container Attributes: ", *containerAttribs)
}

func AddProcess(name string, cmd *exec.Cmd) {
	if cmd.Process == nil {
		log.Println("ERROR: nil Process caught for : " + name)
		return
	}
	containerobj, ok := Containers[name]
	if !ok {
		log.Println("ERROR: couldn't find container for : " + name)
		return
	}
	pid := cmd.Process.Pid
	containerobj.AddProcess(pid)
}

// Status says how many of the sandboxes that InitContainers tried to make exist.
// A REPL whose sandbox is missing runs without namespaces and without a memory
// limit (LLD 03).
func Status() (ready, total int) { return len(Containers), len(Commands2memLimitMap) }

func InitContainers() {
	HasCAPSysAdmin = detectCAPSysAdmin()
	log.Println("INFO: CAP_SYS_ADMIN available:", HasCAPSysAdmin)
	// the filesystem sandbox does not depend on the cgroups below
	InitLandlock()
	if err := initCgroupV2(); err != nil {
		log.Println("ERROR: unable to initialize cgroup v2 sandboxing:", err)
		return
	}

	for command, _ := range Commands2memLimitMap {
		containerObj, err := NewContainer(command, MAX_MEMORY_LIMIT*MB) // memlimit in MB
		if err != nil {
			log.Println("ERROR: ", err)
			continue
		}
		Containers[command] = containerObj
		os.MkdirAll(utils.HOME_DIR, 0777)
		os.Chmod(utils.HOME_DIR, 0777)
		os.MkdirAll(utils.HOME_DIR+command, 0777)
		os.Chmod(utils.HOME_DIR+command, 0777)
	}
}

func DeleteContainers() {
	for command, containerObj := range Containers {
		containerObj.Delete()
		os.RemoveAll(utils.HOME_DIR + command)
		log.Println("Container Deleted for : ", command)
	}
}

func AddProcesstoNewSubCgroup(name string, pid int, iscompiled bool) {
	TrackProcess(pid)
	containerobj, ok := Containers[name]
	if !ok {
		log.Println("AddProcesstoNewSubCgroup: ERROR: couldn't find container for : " + name)
		return
	}
	containerobj.AddProcesstoNewSubCgroup(pid, iscompiled)
}

func DeleteProcessFromSubCgroup(name string, pid int) {
	UntrackProcess(pid)
	containerobj, ok := Containers[name]
	if !ok {
		log.Println("DeleteProcessFromSubCgroup: ERROR: couldn't find container for : " + name)
		return
	}
	containerobj.DeleteProcessFromSubCgroup(pid)
}

// The REPLs this server started and that have not ended, by pid. A forked tab
// may only join one of these. It does not depend on cgroups: if delegated
// cgroup access is unavailable, the cgroup bookkeeping below is empty, and a
// fork was refused as "invalid parent id" even though the parent terminal was
// running.
var (
	liveMu sync.Mutex
	live   = map[int]struct{}{}
)

// TrackProcess records a REPL that has started.
func TrackProcess(pid int) {
	liveMu.Lock()
	live[pid] = struct{}{}
	liveMu.Unlock()
}

// UntrackProcess forgets a REPL that has ended.
func UntrackProcess(pid int) {
	liveMu.Lock()
	delete(live, pid)
	liveMu.Unlock()
}

func isTracked(pid int) bool {
	liveMu.Lock()
	defer liveMu.Unlock()
	_, ok := live[pid]
	return ok
}

func IsProcess(pid int) bool {
	if isTracked(pid) {
		return true
	}
	for _, containerObj := range Containers {
		if containerObj.IsProcess(pid) {
			return true
		}
	}
	// In an unprivileged container, cgroup setup can be unavailable even
	// though the parent terminal is still alive. The nsenter fallback does not
	// request namespaces in that mode, so accepting a live local process is
	// safe and lets secondary terminal tabs share its workspace.
	if !HasCAPSysAdmin && processExists(pid) {
		return true
	}
	return false
}

/*func TestCommand(command string, argv []string) {
		var b bytes.Buffer
		cmd := exec.Command(command, argv...)
	    cmd.Stdout = &b
	    cmd.Stderr = &b
	    err := cmd.Run()
	    if err != nil {
	       log.Println("ERROR: ", err.Error())
	    }
	    log.Println("output:", string(b.Bytes()))
}*/
