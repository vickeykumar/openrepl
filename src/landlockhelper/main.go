// Command landlock-helper applies the filesystem sandbox of a REPL (LLD 14) and
// then becomes the REPL: the same process, so the same PID and terminal, and
// the cgroup and namespaces gotty gave it stay.
//
//	landlock-helper --probe
//	landlock-helper --net-probe
//	landlock-helper --workspace DIR --home-root DIR [--net-emulation] -- program [arguments...]
//	landlock-helper --no-landlock --net-emulation -- program [arguments...]
//
// It is built without cgo, so it is a static program: it loads nothing a user
// could have put in the environment (LD_PRELOAD and the like) before the
// policy is on. If the policy cannot be applied it stops with status 126 and
// does not run the program: there is no unrestricted fallback.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"appstore"
	"landlock"
	"netemu"
)

const failed = 126

func main() {
	probe := flag.Bool("probe", false, "print the Landlock ABI version of the kernel and stop")
	workspace := flag.String("workspace", "", "the folder of the REPL")
	homeRoot := flag.String("home-root", "", "the folder that holds all the workspaces")
	debug := flag.Bool("debug", false, "a Debug run: the debugger may write /proc/<pid>/mem")
	scopeSignals := flag.Bool("scope-signals", false, "the REPL may signal only its own sandbox (where the kernel has it)")
	maxFileMB := flag.Int("max-file-mb", 0, "the size one file the REPL writes may have, in MB (0: no limit)")
	noLandlock := flag.Bool("no-landlock", false, "no filesystem policy: only the network emulation (--net-emulation)")
	netEmulation := flag.Bool("net-emulation", false, "the REPL gets the private network of its workspace (LLD 16); the server gave this process a socket as descriptor 3")
	storeDir := flag.String("store-dir", "", "the App Store's folder (LLD 18): the bundles on the user's list are readable and on the PATH")
	netProbe := flag.Bool("net-probe", false, "the self test of the network emulation, run by the server at start")
	netSelftest := flag.Bool("net-selftest", false, "internal: the second half of --net-probe")
	flag.Parse()

	if *netProbe {
		// the filter, then exec: it is on the process the test runs in
		if err := netemu.Install(3); err != nil {
			fmt.Fprintln(os.Stderr, "network emulation not applied:", err)
			os.Exit(1)
		}
		self, _ := os.Executable()
		err := syscall.Exec(self, []string{self, "--net-selftest"}, os.Environ())
		fmt.Fprintln(os.Stderr, "exec:", err)
		os.Exit(1)
	}
	if *netSelftest {
		if err := netemu.SelfTest(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *noLandlock {
		runWithoutPolicy(*netEmulation, flag.Args())
		return
	}
	abi, err := landlock.ABI()
	if *probe {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("Landlock ABI %d\n", abi)
		return
	}
	args := flag.Args()
	if len(args) == 0 || *workspace == "" || *homeRoot == "" {
		fmt.Fprintln(os.Stderr, "landlock-helper: usage: --workspace DIR --home-root DIR -- program [arguments...]")
		os.Exit(failed)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Landlock sandbox not applied:", err)
		os.Exit(failed)
	}
	extra := storeFolders(*storeDir, *workspace)
	policy, err := landlock.Build(landlock.Options{Workspace: *workspace, HomeRoot: *homeRoot, ABI: abi, Self: landlock.Self(), Debug: *debug,
		ScopeSignals: *scopeSignals && landlock.ScopeSupported(), ExtraRO: extra}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Landlock sandbox not applied:", err)
		os.Exit(failed)
	}
	if *maxFileMB > 0 {
		// soft and hard, so that the REPL cannot raise it: a program that writes a bigger file is stopped by
		// SIGXFSZ (Python ignores that signal and gets an error instead)
		limit := uint64(*maxFileMB) << 20
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: limit, Max: limit}); err != nil {
			fmt.Fprintln(os.Stderr, "landlock-helper: limiting the size of a file:", err)
			os.Exit(failed)
		}
	}
	if policy.TmpClosed {
		// the workspaces are in /tmp, which is therefore closed: temporary files go in the workspace
		tmp := filepath.Join(*workspace, ".tmp")
		if err := os.MkdirAll(tmp, 0700); err == nil {
			for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
				os.Setenv(k, tmp)
			}
		}
	}
	if err := landlock.Restrict(policy); err != nil {
		fmt.Fprintln(os.Stderr, "Landlock sandbox not applied:", err)
		os.Exit(failed)
	}
	if *netEmulation {
		installNetwork()
	}
	// the program is found with the PATH the server gave it, as the shell would
	path, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "landlock-helper:", err)
		os.Exit(127)
	}
	if err := syscall.Exec(path, args, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "landlock-helper: exec", path+":", err)
		os.Exit(127)
	}
}

// installNetwork puts the private network of the workspace on this process (and so on the REPL it becomes). As
// with the policy, there is no unrestricted fallback.
func installNetwork() {
	if err := netemu.Install(3); err != nil {
		fmt.Fprintln(os.Stderr, "network emulation not applied:", err)
		os.Exit(failed)
	}
}

// runWithoutPolicy is for a server where only the network emulation is on.
func runWithoutPolicy(netEmulation bool, args []string) {
	if len(args) == 0 || !netEmulation {
		fmt.Fprintln(os.Stderr, "landlock-helper: usage: --no-landlock --net-emulation -- program [arguments...]")
		os.Exit(failed)
	}
	installNetwork()
	path, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "landlock-helper:", err)
		os.Exit(127)
	}
	if err := syscall.Exec(path, args, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "landlock-helper: exec", path+":", err)
		os.Exit(127)
	}
}

// storeFolders reads the list of the user's App Store bundles (the manifest of the user whose workspace this is) and puts them on
// the environment of the program: PATH (after the system's), PYTHONPATH, LD_LIBRARY_PATH. It returns the folders the sandbox
// must let through: the bundles and the user's own folder of the store (where env.sh is). It runs before the restriction, with
// the server's rights, and a terminal cannot write the store: it cannot add itself to a list. A bad entry stops the start.
func storeFolders(dir, workspace string) []string {
	if dir == "" {
		return nil
	}
	store := appstore.Store{Dir: dir}
	home := filepath.Base(filepath.Clean(workspace))
	own := store.HomeDir(home)
	if own == "" {
		return nil
	}
	ids, err := store.Manifest(home)
	if err != nil {
		fmt.Fprintln(os.Stderr, "landlock-helper: the App Store list of this user:", err)
		os.Exit(failed)
	}
	var folders []string
	var have []string
	for _, id := range ids {
		if store.HasBundle(id) { // a bundle this server does not have yet (it is being replayed) is left out
			folders = append(folders, store.BundlePath(id))
			have = append(have, id)
		}
	}
	if len(have) == 0 {
		return nil
	}
	folders = append(folders, own)
	env := appstore.VarsFor(dir, have).Apply(os.Environ())
	for _, e := range env {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				os.Setenv(e[:i], e[i+1:])
				break
			}
		}
	}
	// Python cannot write its bytecode into a bundle (the sandbox lets it only read them): it goes to the user's workspace instead
	if os.Getenv("PYTHONPYCACHEPREFIX") == "" {
		os.Setenv("PYTHONPYCACHEPREFIX", filepath.Join(workspace, ".pycache"))
	}
	return folders
}
