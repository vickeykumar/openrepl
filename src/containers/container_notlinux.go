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
