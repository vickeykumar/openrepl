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
	perl := filepath.Join(home, "perl5")
	return append(GoEnv(home), "GEM_HOME="+filepath.Join(home, ".gem"), "NPM_CONFIG_PREFIX="+filepath.Join(home, ".npm-global"),
		// cpan and cpanm install into ~/perl5 (the ExtUtils::MakeMaker and Module::Build settings of local::lib); cargo install puts its programs in ~/.cargo/bin
		"PERL_MM_OPT=INSTALL_BASE="+perl, "PERL_MB_OPT=--install_base "+perl, "PERL5LIB="+filepath.Join(perl, "lib", "perl5"),
		"CARGO_INSTALL_ROOT="+filepath.Join(home, ".cargo"),
		// pip installs with --user: the system folder looks writable to pip (a REPL is root) and the sandbox refuses it, so pip's own fallback to the
		// user folder never happens (found in a real terminal: "Permission denied: /usr/local/lib/python3.10/dist-packages")
		"PIP_USER=1")
}

// UserBinDirs are the folders of the programs a user installs (go install, gem, npm -g, pip --user): they go at the END of PATH, so that nothing a
// user installs takes the place of a program of the system.
func UserBinDirs(home string) []string {
	return []string{GoBinDir(home), filepath.Join(home, ".gem", "bin"), filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "perl5", "bin"), filepath.Join(home, ".cargo", "bin")}
}

// PipBashrc is added to the .bashrc of a bash terminal. A virtual environment cannot take a --user install ("User site-packages are not visible in
// this virtualenv"), so the prompt takes PIP_USER away while one is active and puts it back when it is not.
const PipBashrc = "\n_openrepl_pip() { if [ -n \"$VIRTUAL_ENV\" ]; then unset PIP_USER; else export PIP_USER=1; fi; }\nPROMPT_COMMAND=\"_openrepl_pip${PROMPT_COMMAND:+;$PROMPT_COMMAND}\"\n"
