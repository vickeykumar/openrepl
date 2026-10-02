package main

import (
	"flag"
	"io/ioutil"
	"os"
	"strings"

	"github.com/codegangsta/cli"

	"backend/localcommand"
	"server"
	"utils"
)

// envFileFlag names the env file (utils/envfile.go). Like every flag it can
// also be given as GOTTY_ENV_FILE.
var envFileFlag = cli.StringFlag{
	Name:   "env-file",
	Value:  utils.EnvFileDefault,
	Usage:  "File of OPENREPL_* and GOTTY_* settings, loaded before anything else (a default that does not exist is ignored, one you name must)",
	EnvVar: utils.EnvEnvFile,
}

// envFileFromArgs finds --env-file in the command line. The file has to be
// loaded before the flags are parsed, so that a GOTTY_* setting in it is seen
// as the flag's value, which is why this cannot wait for the parsed flags. It
// reads the arguments with the same rules as the real parsing: it knows which
// flags take a value, and stops at the first argument that is not a flag, so
// an --env-file that belongs to the command gotty runs is left alone.
func envFileFromArgs(args []string) (path string, explicit bool) {
	path = utils.EnvFileDefault
	if v := os.Getenv(utils.EnvEnvFile); v != "" {
		path, explicit = v, true
	}
	flags, _, _ := utils.GenerateFlags(&server.Options{}, &localcommand.Options{})
	flags = append(flags, cli.StringFlag{Name: "config"}, envFileFlag, cli.BoolFlag{Name: "version, v"})
	set := flag.NewFlagSet("gotty", flag.ContinueOnError)
	set.SetOutput(ioutil.Discard)
	for _, f := range flags {
		_, isBool := f.(cli.BoolFlag)
		for _, name := range strings.Split(f.GetName(), ",") {
			if name = strings.TrimSpace(name); isBool {
				set.Bool(name, false, "")
			} else {
				set.String(name, "", "")
			}
		}
	}
	set.Parse(args) // an unknown flag stops it; the real parsing reports that
	set.Visit(func(f *flag.Flag) {
		if f.Name == "env-file" {
			path, explicit = f.Value.String(), true
		}
	})
	return path, explicit
}

// loadEnvFile loads the env file the command line or the default names.
func loadEnvFile() error {
	path, explicit := envFileFromArgs(os.Args[1:])
	_, err := utils.LoadEnvFile(path, explicit)
	return err
}
