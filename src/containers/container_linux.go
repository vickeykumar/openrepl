//go:build linux
// +build linux

package containers

import (
	"bufio"
	"bytes"
	"log"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"utils"

	cgroup2 "github.com/containerd/cgroups/v3/cgroup2"
	"github.com/pkg/errors"
)

// flags for nsenter
const (
	CLONE_NEWUTS  = "-u"
	CLONE_NEWPID  = "-p"
	CLONE_NEWNET  = "-n"
	CLONE_NEWUSER = "-U"
)

var ns_flags = []string{
	CLONE_NEWUTS,
	CLONE_NEWPID,
	CLONE_NEWNET,
	CLONE_NEWUSER,
}

const capSysAdmin = 21

// detectCAPSysAdmin reports whether this process has CAP_SYS_ADMIN in its
// effective capability set. Docker's privileged setting is not visible inside
// the container, but this is the capability nsenter needs for these namespace
// operations.
func detectCAPSysAdmin() bool {
	status, err := os.Open("/proc/self/status")
	if err != nil {
		return false
	}
	defer status.Close()

	scanner := bufio.NewScanner(status)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || fields[0] != "CapEff:" {
			continue
		}
		mask, err := strconv.ParseUint(fields[1], 16, 64)
		return err == nil && mask&(uint64(1)<<capSysAdmin) != 0
	}
	return false
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.Stat("/proc/" + strconv.Itoa(pid))
	return err == nil
}

// syscall attributes where an admin get privilege over other
// admin gets network access
var admin_privileges = []uintptr{
	syscall.CLONE_NEWNET,
}

const BASH_PATH = "/bin/bash"

var CPUshares = uint64(1024)

type container struct {
	// Name of the container as per command Name.
	Name string
	// Control is this command's cgroup v2 parent. REPL processes live in children.
	Control *cgroup2.Manager

	// SysProcAttr holds optional, operating system-specific attributes to containerize the Process.
	SysProcAttr *syscall.SysProcAttr

	SubCgroups map[int]*cgroup2.Manager // map of pid to cgroup v2 leaf control
	mu         sync.Mutex
}

func (c *container) AddContainerAttributes(containerAttribs *syscall.SysProcAttr, params url.Values) {
	containerAttribs.Cloneflags = c.SysProcAttr.Cloneflags
	containerAttribs.UidMappings = c.SysProcAttr.UidMappings
	containerAttribs.GidMappings = c.SysProcAttr.GidMappings
	if utils.IsUserAdmin(params) {
		// give special rights to admin
		for _, flags := range admin_privileges {
			containerAttribs.Cloneflags &^= flags
		}
	}
}

func (c *container) AddProcess(pid int) {
	// The process remains in the server's manager leaf until
	// AddProcesstoNewSubCgroup creates its limited leaf and moves it there.
	log.Println("INFO: Waiting for command to finish...", pid)
}

func (c *container) IsProcess(pid int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.SubCgroups[pid]
	return ok
}

func (c *container) Delete() {
	if err := c.Control.Delete(); err != nil {
		log.Printf("ERROR: unable to remove cgroup %s: %v", c.Name, err)
	}
	log.Println("controller deleted for : ", c.Name)
	// TBD : delete sub cgroup MAP
}

func (c *container) AddProcesstoNewSubCgroup(pid int, iscompiled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pidstr := strconv.Itoa(pid)
	memlimit := Commands2memLimitMap[c.Name] * MB // mem limit in MBs
	if iscompiled {
		memlimit = int64(math.Floor(3.0 * float64(memlimit)))
		log.Println("effective memlimit: ", memlimit)
	}
	control, err := c.Control.NewChild(pidstr, &cgroup2.Resources{
		Memory: &cgroup2.Memory{Max: &memlimit},
	})
	if err != nil {
		log.Printf("ERROR: unable to create limited cgroup for %s pid %s: %v", c.Name, pidstr, err)
		return
	}
	if err := control.AddProc(uint64(pid)); err != nil {
		log.Printf("ERROR: unable to move process to cgroup for %s pid %s: %v", c.Name, pidstr, err)
		if deleteErr := control.Delete(); deleteErr != nil {
			log.Printf("ERROR: unable to remove failed cgroup for %s pid %s: %v", c.Name, pidstr, deleteErr)
		}
		return
	}
	c.SubCgroups[pid] = control
}

func (c *container) DeleteProcessFromSubCgroup(pid int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cgroup, ok := c.SubCgroups[pid]
	if ok {
		if err := cgroup.Delete(); err != nil {
			log.Println("ERROR: Unable to delete cgroup for : " + c.Name + " pid: " + strconv.Itoa(pid) + " ERROR: " + err.Error())
		}
		delete(c.SubCgroups, pid)
		log.Println("container deleted for "+c.Name+" pid: ", pid)
	}
}

func NewContainer(name string, memlimit int64) (*container, error) {
	var containerObj container
	containerObj.Name = name
	groupName := name + "_container"
	stalePath := filepath.Join(cgroupV2Mountpoint, "openrepl", groupName)
	if _, err := os.Stat(stalePath); err == nil {
		stale, loadErr := cgroup2.Load(filepath.Join("/openrepl", groupName), cgroup2.WithMountpoint(cgroupV2Mountpoint))
		if loadErr != nil {
			return &containerObj, errors.Wrap(loadErr, "unable to load stale cgroup v2 command group")
		}
		if err := stale.Delete(); err != nil {
			return &containerObj, errors.Wrap(err, "unable to remove stale cgroup v2 command group")
		}
	} else if !os.IsNotExist(err) {
		return &containerObj, errors.Wrap(err, "unable to inspect stale cgroup v2 command group")
	}
	control, err := cgroupV2Root.NewChild(groupName, &cgroup2.Resources{Memory: &cgroup2.Memory{}})
	if err != nil {
		return &containerObj, errors.Wrap(err, "unable to create cgroup v2 command group")
	}
	containerObj.Control = control

	containerObj.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET | syscall.CLONE_NEWUSER,
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
	containerObj.SubCgroups = make(map[int]*cgroup2.Manager)
	log.Println("INFO: New Container created for : ", name, memlimit)
	return &containerObj, nil
}

const cgroupV2Mount = "/sys/fs/cgroup"

var (
	cgroupV2Root       *cgroup2.Manager
	cgroupV2Mountpoint string
)

// initCgroupV2 prepares a delegated, process-free parent for REPL cgroups.
// cgroup v2 requires processes to live in leaf cgroups when controllers are
// enabled for child cgroups, so the server moves itself into a manager leaf.
func initCgroupV2() error {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return err
	}
	var rel string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "0::") {
			rel = strings.TrimPrefix(line, "0::")
			break
		}
	}
	if rel == "" {
		return errors.New("unified cgroup v2 hierarchy not found for process")
	}
	rel = strings.TrimPrefix(filepath.Clean("/"+rel), "/")
	cgroupV2Mountpoint = filepath.Join(cgroupV2Mount, rel)
	if _, err := os.Stat(filepath.Join(cgroupV2Mountpoint, "cgroup.controllers")); err != nil {
		return errors.Wrap(err, "cgroup v2 is not mounted at /sys/fs/cgroup")
	}
	cgroupV2Root, err = cgroup2.NewManager(cgroupV2Mountpoint, "/openrepl", &cgroup2.Resources{})
	if err != nil {
		return errors.Wrap(err, "create cgroup v2 root")
	}
	manager, err := cgroupV2Root.NewChild("manager", nil)
	if err != nil {
		return errors.Wrap(err, "create cgroup v2 manager leaf")
	}
	if err := manager.AddProc(uint64(os.Getpid())); err != nil {
		return errors.Wrap(err, "move server into cgroup v2 manager leaf")
	}
	return nil
}

func EnableNetworking(pid int) {
	//log.Println("euid: ",syscall.Getuid(), syscall.Geteuid())
	var b bytes.Buffer
	cmd := exec.Command("/usr/bin/nsenter", "-n", "-t"+strconv.Itoa(pid), "ifconfig", "lo", "up")
	cmd.Stdout = &b
	cmd.Stderr = &b
	err := cmd.Start()
	if err != nil {
		log.Println("ERROR: error enabling network for pid: ", pid, "error: ", err.Error(), string(b.Bytes()))
		return
	}

	err = cmd.Wait()
	if err != nil {
		log.Println("Error: waiting for network enable for pid: ", pid, err.Error(), string(b.Bytes()))
	}
}

func GetCommandArgs(command string, argv []string, ppid int, params map[string][]string) (commandArgs []string) {
	var commandlist []string
	var commandpath string
	var leadArgs []string // arguments that belong to the program itself, before the request's
	var err error
	if utils.Iscompiled(params) {
		commandpath = BASH_PATH
	} else {
		commandpath, err = exec.LookPath(command) // lookup path for absolutepath
		if err != nil {
			commandpath = command
		}
		// A language whose REPL is another program (java: jshell, with small
		// memory limits): run that one. If it is not installed the command
		// itself is run, as before.
		if alt, ok := utils.InteractiveCommand(command); ok {
			if p, lerr := exec.LookPath(alt[0]); lerr == nil {
				commandpath, leadArgs = p, alt[1:]
			} else {
				log.Println("ERROR: the REPL program", alt[0], "of", command, "was not found:", lerr)
			}
		}
		prefix := utils.GetPrefix(command)
		if prefix != "" {
			commandlist = append(commandlist, prefix) // add prefix before calling the command
		}
	}
	commandlist = append(commandlist, commandpath)
	commandlist = append(commandlist, leadArgs...)
	commandlist = append(commandlist, argv...)
	if utils.Iscompiled(params) {
		filename := utils.GetIdeFileName(params)
		var arg0 string = utils.GetIdeContent(params)
		var otherargs string = utils.GetCompilerFlags(params)
		err = utils.SaveIdeContentToFile(params, filename)
		if err == nil {
			// file saved successfully now i can pass filename as arg0
			arg0 = filename
		}
		//this is a compilation request
		compilerOptions := []string{"-c", utils.GetCompilationScript(command), arg0, otherargs}
		commandlist = append(commandlist, compilerOptions...)
	}
	if ppid == -1 {
		commandArgs = append(commandArgs, commandlist...)
	} else {
		// this process will run in namespace of ppid (forked namespace)
		// syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET | syscall.CLONE_NEWUSER
		nsenterArgs := []string{"/usr/bin/nsenter", "-t" + strconv.Itoa(ppid)}
		if HasCAPSysAdmin {
			// Request the parent's namespaces only when the process has the
			// capability required to enter them, and only those that are not
			// ours already: a REPL that was started without namespaces (no
			// cgroups or user namespaces on this host) lives in the same ones
			// as this process, and nsenter fails to "enter" a user namespace
			// one is in.
			nsenterArgs = append(nsenterArgs, differingNamespaceFlags(ppid)...)
		}
		commandArgs = append(commandArgs, nsenterArgs...)
		commandArgs = append(commandArgs, commandlist...)
	}
	log.Println("commands Args: ", commandArgs)
	return commandArgs
}

// get working directory for a running process, pid
func GetWorkingDir(pid int) string {
	path := "/proc/" + strconv.Itoa(pid) + "/cwd"
	wd, err := os.Readlink(path)
	if err != nil {
		log.Println(err)
		return ""
	}
	return wd
}

// splitEnv splits a string containing null-separated environment variable entries into a slice
func splitEnv(env string) []string {
	return strings.Split(env, "\x00")
}

// parseEnv parses an environment variable entry of the form "NAME=value" and returns the name, value, and a boolean indicating success
func parseEnv(entry string) (name, value string, ok bool) {
	parts := strings.SplitN(entry, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// GetHomeDirFromEnv returns the home directory of the user associated with the given PID
func GetHomeDirFromEnv(pid int) (string, error) {
	// Get the environment variables of the process
	envPath := "/proc/" + strconv.Itoa(pid) + "/environ"
	file, err := os.Open(envPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Read the environment variables
	buf := make([]byte, 8192) // Allocate a buffer for reading
	n, err := file.Read(buf)
	if err != nil {
		return "", err
	}

	// Convert the buffer to a string and split it into environment variables
	env := string(buf[:n])
	var homeDir string
	for _, entry := range splitEnv(env) {
		if name, value, ok := parseEnv(entry); ok && name == "HOME" {
			homeDir = value
			break
		}
	}

	if homeDir == "" {
		return "", os.ErrNotExist // HOME environment variable not found
	}

	return homeDir, nil
}

// differingNamespaceFlags returns the nsenter flags of the namespaces of pid
// that are not the ones of this process. When they cannot be compared (a
// process that is gone, /proc not readable) it returns all of them, as before.
func differingNamespaceFlags(pid int) []string {
	pairs := []struct{ flag, ns string }{
		{CLONE_NEWUTS, "uts"}, {CLONE_NEWPID, "pid"}, {CLONE_NEWNET, "net"}, {CLONE_NEWUSER, "user"},
	}
	var flags []string
	for _, p := range pairs {
		theirs, err1 := os.Readlink("/proc/" + strconv.Itoa(pid) + "/ns/" + p.ns)
		ours, err2 := os.Readlink("/proc/self/ns/" + p.ns)
		if err1 != nil || err2 != nil || theirs != ours {
			flags = append(flags, p.flag)
		}
	}
	return flags
}
