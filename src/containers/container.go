package containers

import (
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"utils"
	"net/url"
)


var Containers = make(map[string]*container)

const MAX_MEMORY_LIMIT = 2564 // Max memory limits in MBs
const MB = 1024 * 1024

// Command Name to memory limit in MB (megabytes).
var Commands2memLimitMap = map[string]int64{
	"cling":         22, // threshold : 11
	"gointerpreter": 45, // 44 with pp
	"yaegi":	     10,
	"python":        2,
	"python2.7":     2,  // 3
	"bash":          10,  // 2, for simultaneous bash consoles
	"ipython":       10,
	"ipython3":	     20,
	"irb":           10,
	"perli":         3,
	"node":          10,
	"jq-repl":       2,
	"tclsh":       	 2,
	"java":			 128, // jvm takes lot of memory
	"evcxr":		 50,  // rust REPL
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

func InitContainers() {
	for command, _ := range Commands2memLimitMap {
		containerObj, err := NewContainer(command, MAX_MEMORY_LIMIT*MB) // memlimit in MB
		if err != nil {
			log.Println("ERROR: ", err)
			continue
		}
		Containers[command] = containerObj
		os.MkdirAll(utils.HOME_DIR, 0777)
		os.Chmod(utils.HOME_DIR, 0777)
		os.MkdirAll(utils.HOME_DIR + command, 0777)
		os.Chmod(utils.HOME_DIR + command, 0777)
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
	containerobj, ok := Containers[name]
	if !ok {
		log.Println("AddProcesstoNewSubCgroup: ERROR: couldn't find container for : " + name)
		return
	}
	containerobj.AddProcesstoNewSubCgroup(pid, iscompiled)
}

func DeleteProcessFromSubCgroup(name string, pid int) {
	containerobj, ok := Containers[name]
	if !ok {
		log.Println("DeleteProcessFromSubCgroup: ERROR: couldn't find container for : " + name)
		return
	}
	containerobj.DeleteProcessFromSubCgroup(pid)
}

func IsProcess(pid int) bool {
	for _, containerObj := range Containers {
		if containerObj.IsProcess(pid) {
			return true
		}
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
