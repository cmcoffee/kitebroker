package main

import (
	_ "embed"
	"fmt"
	"github.com/cmcoffee/snugforge/eflag"
	"github.com/cmcoffee/snugforge/nfo"
	. "kitebroker/core"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// APPNAME is the application name "kitebroker
const APPNAME = "kitebroker"

// VERSION holds the application's version string.
//
//go:embed version.txt
var VERSION string

// Authentication method constants.
// SIGNATURE_AUTH represents signature-based authentication.
// PASSWORD_AUTH represents password-based authentication.
const (
	SIGNATURE_AUTH = iota
	PASSWORD_AUTH
)

// global holds application-wide configuration and state.
var global struct {
	cfg           ConfigStore
	db            Database
	cache         Database
	auth_mode     int
	user          KWSession
	kw            *KWAPI
	freq          time.Duration
	root          string
	setup         bool
	snoop         bool
	debug         bool
	sysmode       bool
	new_task_file bool
	pause         bool
	as_user       string
	gen_token     bool
	show_custom   bool
	single_thread bool
}

// get_runtime_info returns the name of the executable.
func get_runtime_info() string {
	exec, err := os.Executable()
	Critical(err)

	localExec := filepath.Base(exec)

	global.root, err = filepath.Abs(filepath.Dir(exec))
	Critical(err)

	global.root = GetPath(global.root)

	return localExec
}

// load_config loads the configuration file and sets the authentication mode.
// It also sets the custom tasks flag.
func load_config(config_file string) (err error) {
	if err := load_config_defaults(); err != nil {
		global.auth_mode = PASSWORD_AUTH
		return err
	}
	err = global.cfg.File(config_file)
	if err != nil {
		global.auth_mode = SIGNATURE_AUTH
		return err
	}
	switch strings.ToLower(global.cfg.Get("configuration", "auth_flow")) {
	case "signature":
		global.auth_mode = SIGNATURE_AUTH
	case "password":
		global.auth_mode = PASSWORD_AUTH
	default:
		global.auth_mode = PASSWORD_AUTH
		return fmt.Errorf("Unknown auth_flow setting in %s: %s", config_file, global.cfg.Get("configuration", "auth_flow"))
	}

	global.show_custom = global.cfg.GetBool("configuration", "custom_tasks")

	return nil
}

// enable_debug enables debug output to stdout.
func enable_debug() {
	nfo.SetOutput(nfo.DEBUG, os.Stdout)
	nfo.SetFile(nfo.DEBUG, nfo.GetFile(nfo.ERROR))
}

// enable_trace enables trace output to stdout.
func enable_trace() {
	nfo.SetOutput(nfo.TRACE, os.Stdout)
	nfo.SetFile(nfo.TRACE, nfo.GetFile(nfo.ERROR))
}

func main() {
	nfo.HideTS()
	defer Exit(0)

	localExec := get_runtime_info()

	cfg_err := load_config(FormatPath(fmt.Sprintf("%s/%s.ini", global.root, APPNAME)))

	// Initial modifier flags and flag aliases.
	flags := eflag.NewFlagSet(NONE, eflag.ReturnErrorOnly)
	setup := flags.Bool("setup", "kiteworks API Configuration.")
	task_files := flags.Multi("task", "<task_file.tsk>", "Load a task file.")
	flags.DurationVar(&global.freq, "repeat", 0, "How often to repeat task, 0s = single run.")
	version := flags.Bool("version", "")
	flags.BoolVar(&global.sysmode, "quiet", "Minimal output for non-interactive processes.")
	flags.BoolVar(&global.pause, "pause", "Pause after execution.")

	if global.cfg.Get("configuration", "auth_flow") == "signature" {
		flags.StringVar(&global.as_user, "run_as", "<user@domain.com>", "Run command as a specific user.")
	}
	flags.BoolVar(&global.gen_token, "auth_token_only", "Returns the generated auth token, then exits.")
	update := flags.Bool("update", fmt.Sprintf("Checks for newer version of %s.", APPNAME))

	flags.Order("task", "new_task", "repeat", "setup", "quiet", "pause")
	flags.Footer = " "

	flags.BoolVar(&global.single_thread, "serial", NONE)
	flags.BoolVar(&global.debug, "debug", NONE)
	flags.BoolVar(&global.snoop, "snoop", NONE)

	flags.Header = fmt.Sprintf("Usage: %s [options]... <command> [parameters]...\n", os.Args[0])
	flags.BoolVar(&global.new_task_file, "new_task", "Creates a task file template for loading with --task.")

	f_err := flags.Parse(os.Args[1:])

	if global.debug {
		enable_debug()
	}

	if global.snoop {
		enable_trace()
	}

	if *version {
		Stdout("### %s v%s ###", APPNAME, VERSION)
		Stdout("\n")
		Stdout("Written by Craig M. Coffee. (craig@snuglab.com)")
		Exit(0)
	}

	if *update {
		UpdateKitebroker(APPNAME, VERSION, global.root, localExec, global.cfg.GetBool("configuration", "ssl_verify"), global.cfg.Get("configuration", "proxy_uri"))
		Exit(0)
	}

	Defer(func() {
		if global.pause {
			pause()
		}
	})

	if global.gen_token {
		nfo.Animations = false
		command.Select([][]string{{"exit"}})
		if _, err := global.user.MyUser(); err != nil {
			Fatal(err)
			Exit(1)
		}
		auth, err := global.user.KWAPI.TokenStore.Load(global.user.Username)
		if err != nil {
			Fatal(err)
			Exit(1)
		}
		Stdout(auth.AccessToken)
		Exit(0)
	}

	if global.sysmode && !*setup {
		nfo.Animations = false
		nfo.SignalCallback(syscall.SIGINT, func() bool {
			return true
		})
	} else {
		nfo.SignalCallback(syscall.SIGINT, func() bool {
			Log("Application interrupt received. (shutting down)")
			return true
		})
	}

	// We need to do a quick look to see what commands we display for --help
	// err := load_config(FormatPath(fmt.Sprintf("%s/%s.ini", global.root, APPNAME)))

	if f_err != nil {
		if f_err != eflag.ErrHelp {
			Stderr(f_err)
			Stderr(NONE)
		}
		flags.Usage()
		command.Show()
		return
	}

	// Now we will go critical on the config file not loading.
	if cfg_err != nil {
		Notice("No config file found at %s, creating new configuration file.", FormatPath(fmt.Sprintf("%s/%s.ini", global.root, APPNAME)))
		global.cfg.Save()
		*setup = true
		//Critical(cfg_err)
	}

	if *setup {
		init_database()
		config_api(true, false)
		return
	}

	var task_args [][]string

	// Read and process task files.
	for _, f := range *task_files {
		var task_file ConfigStore
		Critical(task_file.File(f))
		for _, s := range task_file.Sections() {
			var args []string
			args = append(args, s)
			for _, k := range task_file.Keys(s) {
				args = append(args, fmt.Sprintf("--%s=%s", k, strings.Join(task_file.MGet(s, k), ",")))
			}
			args = append(args, f)
			task_args = append(task_args, args)
		}
	}

	// Read and process CLI arguments.
	if args := flags.Args(); len(args) > 0 {
		args = append(args, "cli")
		task_args = append(task_args, args)
	}

	if err := command.Select(task_args); err != nil {
		if err != eflag.ErrHelp {
			Stderr(err)
		}
		flags.Usage()
		command.Show()
		if err == eflag.ErrHelp {
			return
		} else {
			Exit(1)
		}
	}
}
