// +build !linux

package containers

import (
	"log"
	"syscall"
)

type container struct {
	// Name of the container as per command Name.
	Name string
}

func (c *container) AddContainerAttributes(containerAttribs *syscall.SysProcAttr) {
	log.Println("INFO: call to AddContainerAttributes")
	return
}

func (c *container) AddProcess(pid int) {
	log.Println("INFO: Waiting for command to finish...", pid)
}

func (c *container) IsProcess(pid int) bool {
	return false
}

func (c *container) Delete() {
	log.Println("container deleted for : ", c.Name)
}

func (c *container) AddProcesstoNewSubCgroup(pid int) {
	log.Println("AddProcesstoNewSubCgroup called for : ", c.Name, pid)
}

func (c *container) DeleteProcessFromSubCgroup(pid int) {
	log.Println("DeleteProcessFromSubCgroup called for : ", c.Name, pid)
}

func NewContainer(name string, memlimit int64) (*container, error) {
	var containerObj container
	containerObj.Name = name
	log.Println("INFO: New Container created for : ", name)
	return &containerObj, nil
}

func EnableNetworking(pid int) {
    log.Println("Network enabled for pid: ", pid)
}

func GetCommandArgs(command string, argv []string, ppid int) (commandArgs []string) {
	var commandlist []string
	commandlist = append(commandlist, command)
	commandlist = append(commandlist, argv...)
	commandArgs = append(commandArgs, commandlist...)
	log.Println("commands Args: ", commandArgs)
	return commandArgs
}
