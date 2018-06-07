package containers

import (
	"log"
	"os/exec"
	"syscall"
)

var Containers = make(map[string]*container)

// Command Name to memory limit in MB (megabytes).
var Commands2memLimitMap = map[string]int64{
	"cling":         800, // threshold : 11
	"gointerpreter": 800, // 44
	"python2.7":     800,  // 3
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
	for command, memlimit := range Commands2memLimitMap {
		containerObj, err := NewContainer(command, memlimit*1024*1024) // memlimit in MB
		if err != nil {
			log.Println("ERROR: ", err)
			continue
		}
		Containers[command] = containerObj
	}
}

func DeleteContainers() {
	for command, containerObj := range Containers {
		containerObj.Delete()
		log.Println("Container Deleted for : ", command)
	}
}
