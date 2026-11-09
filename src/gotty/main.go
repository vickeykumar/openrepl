package main

import (
	"utils"
	"backend/localcommand"
	"backup"
	"containers"
	"cookie"
	"context"
	"fmt"
	"github.com/codegangsta/cli"
	"homesnap"
	"log"
	"os"
	"os/signal"
	"persist"
	"reaper"
	"pkg/homedir"
	"server"
	"strings"
	"syscall"
	"time"
	"user"
)

func common_setup() {
	// as PID 1 in the image gotty adopts the orphans of its terminals: reap them when they end
	reaper.Start()
	// A disk that was wiped (a host that keeps nothing between deploys) is filled from the last backup in the
	// object store, before anything opens the databases. Without an object store this does nothing.
	backup.Default.Remote = persist.Remote
	restoreCtx, cancelRestore := context.WithTimeout(context.Background(), 2*time.Minute)
	if r, err := backup.Default.RestoreIfEmpty(restoreCtx); err != nil {
		log.Println("Error: restoring the last backup: ", err)
		// The disk is empty and the databases are files, so a server that goes on comes up with no accounts, blog or
		// settings. A server whose databases are remote loses only the audit log and the totals, and goes on.
		if persist.Remote() == "" && !utils.DBFallbackToFiles() {
			persist.Fatal("backup: the disk is empty and the last backup could not be restored (" + err.Error() + "). Not starting without the data. Fix the object store settings (OPENREPL_S3_*), or set " + utils.EnvDBFallback + "=files to start empty anyway.")
		}
	} else if r != nil {
		log.Printf("backup: this server's files were restored from the backup of %s (%s): %v", r.CreatedAt.Format(time.RFC3339), r.Key, r.Files)
	}
	cancelRestore()
	// the pending jobs of the last birth were loaded by utils.init (InitGottyJobs); they are saved
	// every minute from now on, and at a clean stop, and forgotten guest folders are swept
	utils.GottyJobs.StartJobSaver(utils.GOTTY_PATH + "/" + utils.JobFile)
	utils.StartGuestSweeper()
	server.InitFeedbackDBHandle()
	server.InitBlogDBHandle()
	server.InitSnippetDBHandle()
	server.InitPracticeDBHandle()
	server.InitTokenDBHandle()
	user.InitSessionDBHandle()
	cookie.InitSecret()
	containers.InitContainers()
	// /tmp is shared by the REPLs: delete what is old, when OPENREPL_TMP_MAX_AGE says so
	utils.StartTmpCleaner()
	// what is only on this disk is copied to the object store when it changed
	backup.Default.Start()
	homesnap.Default.Start()
}

var setup_done bool = false
var cleanup_done bool = false
func common_cleanup() {
	if cleanup_done || !setup_done {
		// cleanup already done
		return
	}

	utils.GottyJobs.SaveJobsToFile(utils.GOTTY_PATH+"/"+utils.JobFile)
	server.CloseFeedbackDBHandle()
	server.CloseBlogDBHandle()
	server.CloseSnippetDBHandle()
	server.ClosePracticeDBHandle()
	server.CloseTokenDBHandle()
	user.CloseSessionDBHandle()
	containers.DeleteContainers()
	cleanup_done = true
	// the sessions are over and the databases are closed: the last snapshots and one more backup hold what was
	// written last
	homesnap.Default.Final(20 * time.Second)
	backup.Default.Final(20 * time.Second)
}

func main() {
	// Settings in an env file must be in the environment before anything reads
	// one, the GOTTY_* flags included, so this comes first.
	if err := loadEnvFile(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(2)
	}
	// where the workspaces live comes from the settings, so after the env file
	utils.InitHomeDir()
	utils.LogConfig()
	// The databases are opened once the mode is known (in app.Action): a
	// worker keeps its data in files, a gateway or a standalone server may keep
	// it in MongoDB.
	defer common_cleanup()

	app := cli.NewApp()
	app.Name = "gotty"
	app.Version = Version + "+" + CommitID
	app.Usage = "Share your terminal as a web application"
	app.HideHelp = true
	cli.AppHelpTemplate = helpTemplate

	appOptions := &server.Options{}
	if err := utils.ApplyDefaultValues(appOptions); err != nil {
		exit(err, 1)
	}
	backendOptions := &localcommand.Options{}
	if err := utils.ApplyDefaultValues(backendOptions); err != nil {
		exit(err, 1)
	}

	cliFlags, flagMappings, err := utils.GenerateFlags(appOptions, backendOptions)
	if err != nil {
		exit(err, 3)
	}

	app.Flags = append(
		cliFlags,
		cli.StringFlag{
			Name:   "config",
			Value:  "~/.gotty",
			Usage:  "Config file path",
			EnvVar: "GOTTY_CONFIG",
		},
		envFileFlag,
	)

	app.Action = func(c *cli.Context) {
		/*if len(c.Args()) == 0 {
			msg := "Error: No command given."
			cli.ShowAppHelp(c)
			exit(fmt.Errorf(msg), 1)
		}*/ // implementing cling interpreter by default

		configFile := c.String("config")
		listen := c.IsSet("port") || c.IsSet("address")
		_, err := os.Stat(homedir.Expand(configFile))
		if configFile != "~/.gotty" || !os.IsNotExist(err) {
			if err := utils.ApplyConfigFile(configFile, appOptions, backendOptions); err != nil {
				exit(err, 2)
			}
			keys := utils.ConfigKeys(configFile)
			listen = listen || keys["port"] || keys["address"]
		}

		utils.ApplyFlags(cliFlags, flagMappings, c, appOptions, backendOptions)
		appOptions.LocalListen = listen

		appOptions.EnableBasicAuth = c.IsSet("credential")
		appOptions.EnableTLSClientAuth = c.IsSet("tls-ca-crt")

		err = appOptions.Validate()
		if err != nil {
			exit(err, 6)
		}

		persist.Configure(appOptions.Mode != server.ModeWorker)
		// the one test of where the databases live; its choice is logged and stays
		persist.Init()
		// The workspaces of signed-in users are kept in the object store by the node that is their authority (a
		// gateway or a single server); a worker holds no object store keys.
		if appOptions.Mode != server.ModeWorker {
			homesnap.Default.Enable()
		}
		common_setup()
		setup_done = true

		args := c.Args()
		if len(args) == 0 {
			args = append(args, "cling")
		}
		factory, err := localcommand.NewFactory(args[0], args[1:], backendOptions)
		if err != nil {
			exit(err, 3)
		}

		hostname, _ := os.Hostname()
		appOptions.TitleVariables = map[string]interface{}{
			"command":  args[0],
			"argv":     args[1:],
			"hostname": hostname,
		}

		server.SetBuildInfo(Version, CommitID)
		srv, err := server.New(factory, appOptions)
		if err != nil {
			exit(err, 3)
		}

		ctx, cancel := context.WithCancel(context.Background())
		gCtx, gCancel := context.WithCancel(context.Background())

		log.Printf("GoTTY is starting with command: %s", strings.Join(args, " "))

		errs := make(chan error, 1)
		go func() {
			errs <- srv.Run(ctx, server.WithGracefullContext(gCtx))
		}()
		err = waitSignals(errs, cancel, gCancel)
		
		if err != nil && err != context.Canceled {
			fmt.Printf("Error: %s\n", err)
			exit(err, 8)
		}

	}
	app.Run(os.Args)
}

func exit(err error, code int) {
	if err != nil {
		fmt.Println(err)
	}
	// anyway this will cause shutdown
	common_cleanup()
	os.Exit(code)
}

func waitSignals(errs chan error, cancel context.CancelFunc, gracefullCancel context.CancelFunc) error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(
		sigChan,
		syscall.SIGINT,
		syscall.SIGTERM,
	)

	select {
	case err := <-errs:
		return err

	case s := <-sigChan:
		switch s {
		case syscall.SIGINT:
			gracefullCancel()
			fmt.Println("C-C to force close")
			select {
			case err := <-errs:
				return err
			case <-sigChan:
				fmt.Println("Force closing...")
				cancel()
				return <-errs
			}
		default:
			cancel()
			return <-errs
		}
	}
}
