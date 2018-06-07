// +build linux

package containers

import (
	"syscall"
	"log"
	"os"
	"github.com/containerd/cgroups"
	"github.com/pkg/errors"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

var CPUshares = uint64(1024)

type container struct {
	// Name of the container as per command Name.
	Name			string
	// Control holds container specific control/limits.
	Control 		cgroups.Cgroup

	// SysProcAttr holds optional, operating system-specific attributes to containerize the Process.
	SysProcAttr 	*syscall.SysProcAttr
}

func (c *container) AddContainerAttributes(containerAttribs *syscall.SysProcAttr) {
	containerAttribs.Cloneflags = c.SysProcAttr.Cloneflags
	containerAttribs.UidMappings = c.SysProcAttr.UidMappings
	containerAttribs.GidMappings = c.SysProcAttr.GidMappings
}

func (c *container) AddProcess(pid int) {
	if err := c.Control.Add(cgroups.Process{Pid:pid}); err != nil {
        log.Printf("ERROR: Error while adding process : %d to cgroups: %s\n",pid,err.Error())
    }
    log.Println("INFO: Waiting for command to finish...",pid)
}

func (c *container) Delete() {
	c.Control.Delete()
	log.Println("controller deleted for : ",c.Name)
}

func NewContainer(name string, memlimit int64) (*container, error) {
	var containerObj container
	var err error
	containerObj.Name = name
	containerObj.Control, err = cgroups.New(cgroups.V1, cgroups.StaticPath("/"+name+"_container"), &specs.LinuxResources{
        /*CPU: &specs.LinuxCPU{
                Shares: &CPUshares,
                Cpus:   "0",
                Mems:   "0",    
        },*/
        Memory: &specs.LinuxMemory{
                Limit: &memlimit,
        },
    })
    if err != nil {
        	return &containerObj, errors.New("Unable to create Container: " + err.Error())
    }

    containerObj.SysProcAttr = &syscall.SysProcAttr{
                Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET| syscall.CLONE_NEWUSER,
                Unshareflags: syscall.CLONE_NEWNS | syscall.CLONE_NEWNET,
                UidMappings: []syscall.SysProcIDMap{
                        {
                                ContainerID: 0,
                                HostID:      os.Getuid(),
                                Size:        1,
                        },      
                },              
                GidMappings: []syscall.SysProcIDMap{
                        {       
                                ContainerID: 0,
                                HostID:      os.Getgid(),
                                Size:        1,
                        },
                },
    }
    log.Println("INFO: New Container created for : ",name)
    return &containerObj, nil
}


