package containers

import (
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

const HOME_DIR = "/tmp/home/"

var Containers = make(map[string]*container)

const MAX_MEMORY_LIMIT = 900 // Max memory limits in MBs
const MB = 1024 * 1024

// Command Name to memory limit in MB (megabytes).
var Commands2memLimitMap = map[string]int64{
	"cling":         11, // threshold : 11
	"gointerpreter": 45, // 44 with pp
	"python2.7":     2,  // 3
	"bash":          2,  // 2
	"ipython":       8,
	"ipython3":	     8,
	"irb":           4,
	"perli":         2,
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

func AddContainerAttributes(name string, containerAttribs *syscall.SysProcAttr) {
	if containerAttribs == nil {
		containerAttribs = &syscall.SysProcAttr{}
	}
	containerobj, ok := Containers[name]
	if !ok {
		log.Println("ERROR: couldn't find container for : " + name)
		return
	}
	containerobj.AddContainerAttributes(containerAttribs)
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
		os.MkdirAll(HOME_DIR, 0777)
		os.Chmod(HOME_DIR, 0777)
		os.MkdirAll(HOME_DIR + command, 0777)
		os.Chmod(HOME_DIR + command, 0777)
	}
}

func DeleteContainers() {
	for command, containerObj := range Containers {
		containerObj.Delete()
		os.RemoveAll(HOME_DIR + command)
		log.Println("Container Deleted for : ", command)
	}
}

func AddProcesstoNewSubCgroup(name string, pid int) {
	containerobj, ok := Containers[name]
	if !ok {
		log.Println("AddProcesstoNewSubCgroup: ERROR: couldn't find container for : " + name)
		return
	}
	containerobj.AddProcesstoNewSubCgroup(pid)
}

func DeleteProcessFromSubCgroup(name string, pid int) {
	containerobj, ok := Containers[name]
	if !ok {
		log.Println("DeleteProcessFromSubCgroup: ERROR: couldn't find container for : " + name)
		return
	}
	containerobj.DeleteProcessFromSubCgroup(pid)
}
