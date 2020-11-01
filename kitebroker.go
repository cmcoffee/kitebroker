package main

import (
	"fmt"
	"github.com/cmcoffee/go-snuglib/eflag"
	"github.com/cmcoffee/go-snuglib/nfo"
	. "github.com/cmcoffee/kitebroker/core"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	APPNAME = "kitebroker"
	VERSION = "20.11.01"
)

const (
	SIGNATURE_AUTH = iota
	PASSWORD_AUTH
)

// Global Variables
var global struct {
	cfg       ConfigStore
	db        Database
	auth_mode int
	user      KWSession
	kw        *KWAPI
	freq      time.Duration
	root      string
	setup     bool
	snoop     bool
	debug     bool
	sysmode   bool
}

func init() {
	exec, err := os.Executable()
	Critical(err)

	global.root, err = filepath.Abs(filepath.Dir(exec))
	Critical(err)

	global.root = GetPath(global.root)
}

func load_config(config_file string) (err error) {
	if err := load_config_defaults(); err != nil {
		global.auth_mode = PASSWORD_AUTH
		return err
	}
	err = global.cfg.File(config_file)
	if err != nil {
		global.auth_mode = PASSWORD_AUTH
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
	return nil
}

// Enable Debug Logging Output
func enable_debug() {
	nfo.SetOutput(nfo.DEBUG, os.Stdout)
	nfo.SetFile(nfo.DEBUG, nfo.GetFile(nfo.ERROR))
}

func main() {
	nfo.HideTS()
	defer Exit(0)

	// Initial modifier flags and flag aliases.
	flags := eflag.NewFlagSet(NONE, eflag.ReturnErrorOnly)
	setup := flags.Bool("setup", false, "kiteworks API Configuration.")
	task_files := flags.Array("task", "<task_file.tsk>", "Load a task file.")
	flags.DurationVar(&global.freq, "repeat", 0, "How often to repeat task, 0s = single run.")
	version := flags.Bool("version", false, "")
	flags.BoolVar(&global.sysmode, "quiet", false, "Minimal output for non-interactive processes.")
	flags.Order("task", "repeat", "setup", "quiet", "pause")
	flags.Footer = " "

	flags.BoolVar(&global.debug, "debug", false, NONE)
	flags.BoolVar(&global.snoop, "snoop", false, NONE)
	flags.Header = fmt.Sprintf("Usage: %s [options]... <command> [parameters]...\n", os.Args[0])

	f_err := flags.Parse(os.Args[1:])

	if *version {
		Stdout("### %s v%s ###", APPNAME, VERSION)
		Stdout("\n")
		Stdout("Written by Craig M. Coffee. (craig@snuglab.com)")
		Exit(0)
	}

	if global.debug || global.snoop {
		enable_debug()
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

	err := load_config(FormatPath(fmt.Sprintf("%s/%s.ini", global.root, APPNAME)))

	if f_err != nil {
		if f_err != eflag.ErrHelp {
			Stderr(f_err)
			Stderr(NONE)
		}
		flags.Usage()
		command.Show()
		return
	}

	// Now we will go crtical on the config file not loading.
	if err != nil {
		Critical(err)
	}

	if *setup {
		init_database()
		config_api(true)
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
				for _, v := range task_file.MGet(s, k) {
					args = append(args, fmt.Sprintf("--%s=%s", k, v))
				}
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
