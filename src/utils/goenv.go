package utils

import "path/filepath"

// GoEnv is the environment of the Go tool for a REPL whose home is home: its own GOPATH and GOBIN in the workspace (the server's /opt/gotty/ is not
// the user's to use), and no download of a newer toolchain (a go.mod that asks for one fails plainly). The module cache is the scratch folder's
// (landlock-helper), and the Go modules of the App Store are added by appstore.Vars.
func GoEnv(home string) []string {
	gopath := filepath.Join(home, "go")
	return []string{"GOPATH=" + gopath, "GOBIN=" + filepath.Join(gopath, "bin"), "GOTOOLCHAIN=local"}
}

// GoBinDir is the folder of the programs a user installs with go install: it goes to the end of PATH.
func GoBinDir(home string) string { return filepath.Join(home, "go", "bin") }

// UserToolEnv is what a REPL needs so that the package managers install into its workspace and not into the system folders (which the sandbox
// refuses): Go (GoEnv), RubyGems (GEM_HOME; GEM_PATH is left alone so the system gems are still found) and npm -g (NPM_CONFIG_PREFIX). pip needs
// nothing: it installs into ~/.local by itself when the system folder is not writable. Caches are the scratch folder's (landlock-helper).
func UserToolEnv(home string) []string {
	return append(GoEnv(home), "GEM_HOME="+filepath.Join(home, ".gem"), "NPM_CONFIG_PREFIX="+filepath.Join(home, ".npm-global"))
}

// UserBinDirs are the folders of the programs a user installs (go install, gem, npm -g, pip --user): they go at the END of PATH, so that nothing a
// user installs takes the place of a program of the system.
func UserBinDirs(home string) []string {
	return []string{GoBinDir(home), filepath.Join(home, ".gem", "bin"), filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".local", "bin")}
}
