//go:build linux
// +build linux

package containers

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/containerd/cgroups"
	specs "github.com/opencontainers/runtime-spec/specs-go"
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
)

// flags for nsenter
const (
	CLONE_NEWUTS  = "-u"
	CLONE_NEWPID  = "-p"
	CLONE_NEWMNT  = "-m"
	CLONE_NEWNET  = "-n"
	CLONE_NEWUSER = "-U"
)

var ns_flags = []string{
	CLONE_NEWUTS,
	CLONE_NEWPID,
	CLONE_NEWMNT,
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
	// Control holds container specific control/limits.
	Control cgroups.Cgroup

	// SysProcAttr holds optional, operating system-specific attributes to containerize the Process.
	SysProcAttr *syscall.SysProcAttr

	SubCgroups      map[int]cgroups.Cgroup // map of pid to cgroup control of subcgroups
	MemoryAvailable bool
	v2Base          string
	v2SubCgroups    map[int]string
	mu              sync.Mutex
}

var cgroupV2State struct {
	sync.Once
	base string
	err  error
}

// setupCgroupV2 delegates the memory controller beneath this service's
// systemd cgroup. The service moves into a child before controllers are
// enabled, leaving the parent empty as required by cgroup v2.
func setupCgroupV2() (string, error) {
	cgroupV2State.Do(func() {
		data, err := os.ReadFile("/proc/self/cgroup")
		if err != nil {
			cgroupV2State.err = err
			return
		}
		var rel string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "0::") {
				rel = strings.TrimPrefix(line, "0::")
				break
			}
		}
		if rel == "" {
			cgroupV2State.err = fmt.Errorf("no unified cgroup v2 membership")
			return
		}
		root := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(filepath.Clean("/"+rel), string(filepath.Separator)))
		controllers, err := os.ReadFile(filepath.Join(root, "cgroup.controllers"))
		if err != nil || !strings.Contains(" "+string(controllers)+" ", " memory ") {
			cgroupV2State.err = fmt.Errorf("cgroup v2 memory controller is unavailable")
			return
		}
		base := filepath.Join(root, "openrepl")
		if err = os.Mkdir(base, 0755); err != nil && !os.IsExist(err) {
			cgroupV2State.err = err
			return
		}
		if err = writeCgroup(filepath.Join(base, "cgroup.procs"), strconv.Itoa(os.Getpid())); err != nil {
			cgroupV2State.err = fmt.Errorf("move service into delegated cgroup: %w", err)
			return
		}
		if err = enableCgroupController(root, "memory"); err != nil {
			cgroupV2State.err = err
			return
		}
		control := filepath.Join(base, "control")
		if err = os.Mkdir(control, 0755); err != nil && !os.IsExist(err) {
			cgroupV2State.err = err
			return
		}
		if err = writeCgroup(filepath.Join(control, "cgroup.procs"), strconv.Itoa(os.Getpid())); err != nil {
			cgroupV2State.err = fmt.Errorf("move service into control cgroup: %w", err)
			return
		}
		if err = enableCgroupController(base, "memory"); err != nil {
			cgroupV2State.err = err
			return
		}
		cgroupV2State.base = base
	})
	return cgroupV2State.base, cgroupV2State.err
}

func writeCgroup(path, value string) error {
	return os.WriteFile(path, []byte(value), 0644)
}

func enableCgroupController(dir, name string) error {
	path := filepath.Join(dir, "cgroup.subtree_control")
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(" "+string(contents)+" ", " "+name+" ") {
		return nil
	}
	if err := writeCgroup(path, "+"+name); err != nil {
		return fmt.Errorf("enable cgroup v2 %s controller: %w", name, err)
	}
	return nil
}

var namespaceProfile struct {
	sync.Once
	flags uintptr
	names map[string]bool
}

func probeNamespaces() {
	namespaceProfile.names = map[string]bool{}
	probes := []struct {
		name string
		flag uintptr
	}{
		{"uts", syscall.CLONE_NEWUTS}, {"pid", syscall.CLONE_NEWPID},
		{"mount", syscall.CLONE_NEWNS}, {"network", syscall.CLONE_NEWNET},
	}
	for _, p := range probes {
		cmd := exec.Command("/bin/true")
		cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: p.flag}
		if err := cmd.Run(); err == nil {
			namespaceProfile.flags |= p.flag
			namespaceProfile.names[p.name] = true
		} else {
			log.Printf("sandbox: %s namespace unavailable: %v", p.name, err)
			namespaceProfile.names[p.name] = false
		}
	}
	for namespaceProfile.flags != 0 {
		cmd := exec.Command("/bin/true")
		cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: namespaceProfile.flags}
		if cmd.Run() == nil {
			break
		}
		for _, p := range probes {
			if namespaceProfile.flags&p.flag != 0 {
				namespaceProfile.flags &^= p.flag
				namespaceProfile.names[p.name] = false
				log.Printf("sandbox: namespace combination unavailable; disabling %s", p.name)
				break
			}
		}
	}
}

// NamespaceStatus reports each startup probe result.
func NamespaceStatus() map[string]bool {
	namespaceProfile.Do(probeNamespaces)
	out := make(map[string]bool, len(namespaceProfile.names))
	for k, v := range namespaceProfile.names {
		out[k] = v
	}
	return out
}

func (c *container) AddContainerAttributes(containerAttribs *syscall.SysProcAttr, params url.Values) {
	containerAttribs.Cloneflags = c.SysProcAttr.Cloneflags
	if utils.IsUserAdmin(params) {
		// give special rights to admin
		for _, flags := range admin_privileges {
			containerAttribs.Cloneflags &^= flags
		}
	}
}

func (c *container) AddProcess(pid int) {
	TrackProcess(pid)
	if c.Control == nil {
		log.Printf("sandbox: memory cgroup unavailable for pid %d", pid)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.Control.Add(cgroups.Process{Pid: pid}); err != nil {
		log.Printf("ERROR: Error while adding process : %d to cgroups: %s\n", pid, err.Error())
	}
	log.Println("INFO: Waiting for command to finish...", pid)
}

func (c *container) IsProcess(pid int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.SubCgroups[pid]
	return ok
}

func (c *container) Delete() {
	if c.Control != nil {
		c.Control.Delete()
	}
	log.Println("controller deleted for : ", c.Name)
	// TBD : delete sub cgroup MAP
}

func (c *container) AddProcesstoNewSubCgroup(pid int, iscompiled bool) {
	TrackProcess(pid)
	if c.Control == nil && c.v2Base == "" {
		return
	}
	pidstr := strconv.Itoa(pid)
	memlimit := Commands2memLimitMap[c.Name] * MB // mem limit in MBs
	if iscompiled {
		memlimit = int64(math.Floor(3.0 * float64(memlimit)))
		log.Println("effective memlimit: ", memlimit)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Control == nil {
		path := filepath.Join(c.v2Base, c.Name+"-"+pidstr)
		if err := os.Mkdir(path, 0755); err != nil {
			log.Printf("sandbox: unable to create cgroup v2 for %s pid %d: %v", c.Name, pid, err)
			return
		}
		if err := writeCgroup(filepath.Join(path, "memory.max"), strconv.FormatInt(memlimit, 10)); err != nil {
			log.Printf("sandbox: unable to set cgroup v2 memory limit for %s pid %d: %v", c.Name, pid, err)
			os.Remove(path)
			return
		}
		if err := writeCgroup(filepath.Join(path, "cgroup.procs"), pidstr); err != nil {
			log.Printf("sandbox: unable to move pid %d into cgroup v2: %v", pid, err)
			os.Remove(path)
			return
		}
		c.v2SubCgroups[pid] = path
		return
	}
	control, err := c.Control.New(pidstr, &specs.LinuxResources{
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
		log.Println("ERROR: unable to create sub cgroup for : " + c.Name + " pid: " + pidstr + " error: " + err.Error())
		return
	}
	err = control.Add(cgroups.Process{Pid: pid})
	if err != nil {
		log.Println("ERROR: Unable to add process to sub cgroup: " + c.Name + " pid: " + pidstr + " error: " + err.Error())
		control.Delete()
		return
	}
	c.SubCgroups[pid] = control
}

func (c *container) DeleteProcessFromSubCgroup(pid int) {
	UntrackProcess(pid)
	c.mu.Lock()
	defer c.mu.Unlock()
	if path, ok := c.v2SubCgroups[pid]; ok {
		if err := os.Remove(path); err != nil {
			log.Printf("sandbox: unable to remove cgroup v2 for pid %d: %v", pid, err)
		}
		delete(c.v2SubCgroups, pid)
	}
	cgroup, ok := c.SubCgroups[pid]
	if ok {
		err := cgroup.Delete()
		if err != nil {
			log.Println("ERROR: Unable to delete cgroup for : " + c.Name + " pid: " + strconv.Itoa(pid) + " ERROR: " + err.Error())
		}
		delete(c.SubCgroups, pid)
		log.Println("container deleted for "+c.Name+" pid: ", pid)
	}
}

func NewContainer(name string, memlimit int64) (*container, error) {
	var containerObj container
	var err error
	containerObj.Name = name
	containerObj.SubCgroups = make(map[int]cgroups.Cgroup)
	containerObj.v2SubCgroups = make(map[int]string)
	namespaceProfile.Do(probeNamespaces)
	containerObj.SysProcAttr = &syscall.SysProcAttr{Cloneflags: namespaceProfile.flags}
	// Load and clean up the containers if already exists
	ctrl, err := cgroups.Load(cgroups.V1, cgroups.StaticPath("/"+name+"_container"))
	if err == nil {
		log.Println("INFO: cgroup already exists, deleting: " + name + "_container")
		ctrl.Delete()
	}
	containerObj.Control, err = cgroups.New(cgroups.V1, cgroups.StaticPath("/"+name+"_container"), &specs.LinuxResources{
		/*CPU: &specs.LinuxCPU{
		        Shares: &CPUshares,
		        Cpus:   "0",
		        Mems:   "0",
		},*/
		Memory: &specs.LinuxMemory{
			Limit: nil, // unlimited, will impose memlimit when adding it to subgroups
		},
	})
	if err != nil {
		log.Printf("sandbox: memory cgroup unavailable for %s: %v", name, err)
		containerObj.Control = nil
		if base, v2err := setupCgroupV2(); v2err == nil {
			containerObj.v2Base = base
			containerObj.MemoryAvailable = true
		} else {
			log.Printf("sandbox: cgroup v2 memory unavailable for %s: %v", name, v2err)
		}
	} else {
		containerObj.MemoryAvailable = true
	}
	log.Println("INFO: New sandbox profile created for : ", name, memlimit)
	return &containerObj, nil
}

func EnableNetworking(pid int) {
	// REPLs normally share the service's network namespace when namespace
	// creation is unavailable. nsenter -n still calls setns in that case and
	// fails without CAP_SYS_ADMIN, even though there is nothing to configure.
	ours, ourErr := os.Readlink("/proc/self/ns/net")
	theirs, theirErr := os.Readlink("/proc/" + strconv.Itoa(pid) + "/ns/net")
	if ourErr == nil && theirErr == nil && ours == theirs {
		return
	}
	if !HasCAPSysAdmin {
		return
	}
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
	if ppid == -1 || !HasCAPSysAdmin {
		// Without a distinct namespace to join (or permission to join one),
		// launch directly. In particular, bare `nsenter -t PID` may try to
		// enter every namespace and fail even though the parent is usable.
		commandArgs = append(commandArgs, commandlist...)
	} else {
		// this process will run in namespace of ppid (forked namespace)
		// syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET | syscall.CLONE_NEWUSER
		// Enter only namespaces which differ from this process. A REPL that
		// started without namespaces already shares ours; invoking nsenter with
		// only -t can attempt to enter all of them unnecessarily.
		namespaceFlags := differingNamespaceFlags(ppid)
		if len(namespaceFlags) == 0 {
			commandArgs = append(commandArgs, commandlist...)
		} else {
			nsenterArgs := []string{"/usr/bin/nsenter", "-t" + strconv.Itoa(ppid)}
			nsenterArgs = append(nsenterArgs, namespaceFlags...)
			commandArgs = append(commandArgs, nsenterArgs...)
			commandArgs = append(commandArgs, commandlist...)
		}
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

// GetHomeDirFromEnv retains its historical name, but HOME is no longer read
// from /proc: runtime UIDs intentionally make that file unreadable to the
// server. Only processes started and registered by this server are resolvable.
func GetHomeDirFromEnv(pid int) (string, error) {
	if home, ok := WorkspaceHome(pid); ok {
		return home, nil
	}
	return "", os.ErrNotExist
}

// differingNamespaceFlags returns the nsenter flags of the namespaces of pid
// that are not the ones of this process. When they cannot be compared (a
// process that is gone, /proc not readable) it returns all of them, as before.
func differingNamespaceFlags(pid int) []string {
	pairs := []struct{ flag, ns string }{
		{CLONE_NEWUTS, "uts"}, {CLONE_NEWPID, "pid"}, {CLONE_NEWMNT, "mnt"},
		{CLONE_NEWNET, "net"}, {CLONE_NEWUSER, "user"},
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
