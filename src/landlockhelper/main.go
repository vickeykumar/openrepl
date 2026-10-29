// Command landlock-helper applies the filesystem sandbox of a REPL (LLD 14) and
// then becomes the REPL: the same process, so the same PID and terminal, and
// the cgroup and namespaces gotty gave it stay.
//
//	landlock-helper --probe
//	landlock-helper --workspace DIR --home-root DIR -- program [arguments...]
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

	"landlock"
)

const failed = 126

func main() {
	probe := flag.Bool("probe", false, "print the Landlock ABI version of the kernel and stop")
	workspace := flag.String("workspace", "", "the folder of the REPL")
	homeRoot := flag.String("home-root", "", "the folder that holds all the workspaces")
	debug := flag.Bool("debug", false, "a Debug run: the debugger may write /proc/<pid>/mem")
	flag.Parse()

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
	policy, err := landlock.Build(landlock.Options{Workspace: *workspace, HomeRoot: *homeRoot, ABI: abi, Self: landlock.Self(), Debug: *debug}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Landlock sandbox not applied:", err)
		os.Exit(failed)
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
