package containers

import (
	"net/url"
	"syscall"
	"testing"

	"utils"
)

func TestAnAdminsREPLGetsNoNamespacesAndAGuestsDoes(t *testing.T) {
	Containers["adminprobe"] = &container{
		Name: "adminprobe",
		SysProcAttr: &syscall.SysProcAttr{
			Cloneflags:  syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET | syscall.CLONE_NEWUSER,
			UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: 1}},
		},
	}
	defer delete(Containers, "adminprobe")

	admin := &syscall.SysProcAttr{}
	AddContainerAttributes("adminprobe", admin, url.Values{utils.USER_PRIVILEGE_KEY: {utils.ADMIN}})
	if admin.Cloneflags != 0 || len(admin.UidMappings) != 0 {
		t.Errorf("an admin's REPL got namespaces: %x %v", admin.Cloneflags, admin.UidMappings)
	}

	guest := &syscall.SysProcAttr{}
	AddContainerAttributes("adminprobe", guest, url.Values{utils.USER_PRIVILEGE_KEY: {utils.GUEST}})
	if guest.Cloneflags&syscall.CLONE_NEWPID == 0 || guest.Cloneflags&syscall.CLONE_NEWNET == 0 || len(guest.UidMappings) == 0 {
		t.Errorf("a guest's REPL got no namespaces: %x %v", guest.Cloneflags, guest.UidMappings)
	}
}
