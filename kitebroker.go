package main

import (
	"fmt"
	. "github.com/cmcoffee/go-kwlib"
	"github.com/cmcoffee/go-snuglib/cfg"
	"github.com/cmcoffee/go-snuglib/eflag"
	"github.com/cmcoffee/go-snuglib/nfo"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	NAME    = "kitebroker"
	VERSION = "1.0.0a-ng"
)

const (
	SIGNATURE_AUTH = iota
	PASSWORD_AUTH
)

// Global Variables
var global struct {
	cfg       cfg.Store
	db        *Database
	auth_mode int
	user      KWSession
	kw        *KWAPI
	freq      time.Duration
	root      string
	setup     bool
	snoop     bool
}

var (
	FormatPath = filepath.FromSlash
	GetPath    = filepath.ToSlash
)

func init() {
	nfo.SignalCallback(syscall.SIGINT, func() bool { Log("Application interrupt received. (shutting down)"); return true })

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
	defer PleaseWait.Hide()

	config_file := FormatPath(fmt.Sprintf("%s/%s.cfg", global.root, NAME))

	if cwd, err := os.Getwd(); err == nil {
		local_conf := FormatPath(fmt.Sprintf("%s/%s.cfg", cwd, NAME))
		if _, err := os.Stat(local_conf); err == nil {
			config_file = FormatPath(local_conf)
		}
	}

	// Initial modifier flags and flag aliases.
	flags := eflag.NewFlagSet(os.Args[0], eflag.ReturnErrorOnly)
	flags.StringVar(&config_file, "config", config_file, fmt.Sprintf("%s configuration file.", NAME))
	setup := flags.Bool("setup", false, "kiteworks API Configuration.")
	task_files := flags.Array("file", "<task_file.tsk>", "Task file for request.\n\t(use multiple --file args for multi-task)")
	flags.DurationVar(&global.freq, "repeat", 0, "How often to repeat task, 0s = single run.")
	flags.Footer = " "

	flags.BoolVar(&global.snoop, "snoop", false, "Snoop on API calls to the kiteworks appliance.")

	f_err := flags.Parse(os.Args[1:])

	// We need to do a quick look to see what commands we display for --help

	err := load_config(config_file)

	if f_err != nil {
		if f_err != eflag.ErrHelp {
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
		var task_file cfg.Store
		Critical(task_file.File(f))
		for _, s := range task_file.Sections() {
			var args []string
			args = append(args, s)
			for _, k := range task_file.Keys(s) {
				for _, v := range task_file.MGet(s, k) {
					if !strings.Contains(v, " ") && !strings.Contains(v, ",") {
						switch strings.ToLower(v) {
						case "yes":
							v = "true"
						case "no":
							v = "false"
						}
						args = append(args, fmt.Sprintf("--%s=%s", k, v))
					} else {
						args = append(args, fmt.Sprintf("--%s=\"%s\"", k, v))
					}
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
		if err != eflag.ErrHelp {
			Stderr(err)
		}
		flags.Usage()
		jobs.Show()
		if err == eflag.ErrHelp {
			return
		} else {
			Exit(1)
		}
	}
}
