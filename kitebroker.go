package main

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	APPNAME = "kitebroker"
	VERSION = "20.09.04"
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
	pause     bool
}

func init() {
	SignalCallback(syscall.SIGINT, func() bool {
		Defer(func() { Log("Application interrupt received. (shutting down)") })
		return true
	})

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

func main() {
	HideTS()
	defer Exit(0)

	// Initial modifier flags and flag aliases.
	flags := NewFlagSet(NONE, ReturnErrorOnly)
	setup := flags.Bool("setup", false, "kiteworks API Configuration.")
	task_files := flags.Array("task", "<task_file.tsk>", "Load a task file.")
	flags.DurationVar(&global.freq, "repeat", 0, "How often to repeat task, 0s = single run.")
	version := flags.Bool("version", false, "")
	flags.BoolVar(&global.pause, "pause", false, "Pause after execution.")
	flags.Order("task", "repeat", "setup", "pause")
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
		EnableDebug()
	}

	// We need to do a quick look to see what commands we display for --help

	err := load_config(FormatPath(fmt.Sprintf("%s/%s.ini", global.root, APPNAME)))

	if f_err != nil {
		if f_err != ErrHelp {
			Stderr(f_err)
			Stderr(NONE)
		}
		flags.Usage()
		jobs.Show()
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

	if err := jobs.Select(task_args); err != nil {
		if err != ErrHelp {
			Stderr(err)
		}
		flags.Usage()
		jobs.Show()
		if err == ErrHelp {
			return
		} else {
			Exit(1)
		}
	}

	if global.pause {
		PleaseWait.Hide()
		pause()
	}
}
