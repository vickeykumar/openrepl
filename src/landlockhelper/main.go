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
	"strings"
	"syscall"

	"appstore"
	"landlock"
	"netemu"
	"scratch"
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
	scratchRoot := flag.String("scratch-root", "", "the folder of the scratch folders: this terminal gets one of its own, writable by it alone (LLD 18)")
	storeExtra := flag.String("store-extra", "", "one more App Store bundle that is readable and on the PATH for this run (the server checks that an app works for a user before it is on for them)")
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
		// no filesystem policy (an admin's terminal, or a server without Landlock): the user's App Store apps are still put on the environment
		if *storeDir != "" && *workspace != "" {
			storeFolders(*storeDir, *workspace, *storeExtra)
		}
		runWithoutPolicy(*netEmulation, finishEnv(makeScratch(*scratchRoot, *workspace), flag.Args()))
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
	extra := storeFolders(*storeDir, *workspace, *storeExtra)
	var rwExtra []string
	sd := makeScratch(*scratchRoot, *workspace)
	if sd != "" {
		rwExtra = []string{sd}
	}
	args = finishEnv(sd, args)
	policy, err := landlock.Build(landlock.Options{Workspace: *workspace, HomeRoot: *homeRoot, ABI: abi, Self: landlock.Self(), Debug: *debug,
		ScopeSignals: *scopeSignals && landlock.ScopeSupported(), ExtraRO: extra, ExtraRW: rwExtra}, nil)
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
	if policy.TmpClosed && len(rwExtra) == 0 {
		// the workspaces are in /tmp, which is therefore closed: temporary files go in the workspace (with a scratch folder, there)
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

// makeScratch makes the scratch folder of this terminal and puts its variables on the environment (they replace the server's: the caches and
// the builds of the terminal go there, not into the workspace). It returns the folder, or "" when there is no scratch on this server. A
// terminal that cannot get one starts without it: scratch is a convenience, never a reason for a terminal not to start.
func makeScratch(root, workspace string) string {
	if root == "" || workspace == "" {
		return ""
	}
	dir, err := scratch.Make(root, filepath.Base(filepath.Clean(workspace)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "landlock-helper: no scratch folder:", err)
		return ""
	}
	for _, kv := range scratch.Vars(dir) {
		if i := strings.IndexByte(kv, '='); i > 0 {
			os.Setenv(kv[:i], kv[i+1:])
		}
	}
	jo := scratch.JavaOption(dir)
	if cur := os.Getenv("JAVA_TOOL_OPTIONS"); cur != "" {
		jo = cur + " " + jo
	}
	os.Setenv("JAVA_TOOL_OPTIONS", jo)
	return dir
}

// finishEnv does what the lists of the user's apps need once the scratch folder is there: a Python 2.7 terminal gets the libraries of Python
// 2.7 and none of Python 3's, and the crates of the user's Rust apps replace crates.io for cargo (a cargo home in the scratch folder, with a
// folder of links to the vendored crates, and no network).
func finishEnv(scratchDir string, args []string) []string {
	if len(args) > 0 && strings.HasPrefix(filepath.Base(args[0]), "python2") {
		if p := os.Getenv("OPENREPL_PY27PATH"); p != "" {
			os.Setenv("PYTHONPATH", p)
		} else {
			os.Unsetenv("PYTHONPATH")
		}
	}
	args = jshellWithJars(args)
	v := os.Getenv("OPENREPL_CRATE_VENDORS")
	if v == "" || scratchDir == "" {
		return args
	}
	farm := filepath.Join(scratchDir, "cargo-vendor")
	home := filepath.Join(scratchDir, "cargo-home")
	if os.MkdirAll(farm, 0700) != nil || os.MkdirAll(home, 0700) != nil {
		return args
	}
	for _, dir := range strings.Split(v, ":") {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				os.Symlink(filepath.Join(dir, e.Name()), filepath.Join(farm, e.Name())) // the same crate twice is the same files
			}
		}
	}
	if os.WriteFile(filepath.Join(home, "config.toml"), []byte(appstore.CargoConfig(farm)), 0600) != nil {
		return args
	}
	os.Setenv("CARGO_HOME", home)
	if os.Getenv("RUSTUP_HOME") == "" { // the toolchains stay where rustup put them, whatever the cargo home is
		if fi, err := os.Stat("/root/.rustup"); err == nil && fi.IsDir() {
			os.Setenv("RUSTUP_HOME", "/root/.rustup")
		}
	}
	return args
}

// jshellWithJars gives jshell the jars of the user's Java apps. The REPL runs the snippets in the tool's own JVM (--execution local, to save memory),
// which does not read CLASSPATH: the jars go on the compile path (--class-path) and, one by one, on the boot class path of that JVM.
func jshellWithJars(args []string) []string {
	cp := os.Getenv("CLASSPATH")
	if len(args) == 0 || filepath.Base(args[0]) != "jshell" || cp == "" {
		return args
	}
	var jars []string
	for _, e := range strings.Split(cp, ":") {
		if strings.HasSuffix(e, "/*") {
			m, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(e, "/*"), "*.jar"))
			jars = append(jars, m...)
		} else if strings.HasSuffix(e, ".jar") {
			jars = append(jars, e)
		}
	}
	if len(jars) == 0 {
		return args
	}
	out := []string{args[0], "--class-path", cp, "-J-Xbootclasspath/a:" + strings.Join(jars, ":")}
	return append(out, args[1:]...)
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
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "landlock-helper: usage: --no-landlock [--net-emulation] [--store-dir DIR --workspace DIR] -- program [arguments...]")
		os.Exit(failed)
	}
	if netEmulation {
		installNetwork()
	}
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
func storeFolders(dir, workspace, extra string) []string {
	if dir == "" {
		return nil
	}
	store := appstore.Store{Dir: dir}
	home := filepath.Base(filepath.Clean(workspace))
	own := store.HomeDir(home)
	if own == "" {
		return nil
	}
	ids, err := store.Effective(home)
	if err != nil {
		fmt.Fprintln(os.Stderr, "landlock-helper: the App Store list of this user:", err)
		os.Exit(failed)
	}
	if extra != "" {
		if !appstore.ValidBundle(extra) {
			fmt.Fprintln(os.Stderr, "landlock-helper: --store-extra is not a bundle name")
			os.Exit(failed)
		}
		ids = append(ids, extra)
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
