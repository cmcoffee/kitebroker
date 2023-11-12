package main

import (
	"fmt"
	"github.com/cmcoffee/go-snuglib/eflag"
	//"github.com/cmcoffee/go-snuglib/nfo"
	. "github.com/cmcoffee/kitebroker/core"
	"os"
	"runtime"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

var command menu

// Menu for tasks.
type menu struct {
	mutex        sync.RWMutex
	text         *tabwriter.Writer
	entries      map[string]*menu_elem
	tasks        []string
	admin_tasks  []string
	hidden_tasks []string
}

// Write out menu item.
func (m *menu) cmd_text(cmd string, desc string) {
	m.text.Write([]byte(fmt.Sprintf("  %s \t \"%s\"\n", cmd, desc)))
}

const (
	_normal_task = 1 << iota
	_admin_task
	_hidden_task
)

// Menu item.
type menu_elem struct {
	name   string
	desc   string
	parsed bool
	t_flag uint
	task   Task
	flags  *FlagSet
}

// Registers an admin task.
func (m *menu) RegisterAdmin(task Task) {
	m.register(task.Name(), _admin_task, task)
}

// Registers an admin task.
func (m *menu) Register(task Task) {
	m.register(task.Name(), _normal_task, task)
}

func (m *menu) RegisterName(name string, task Task) {
	m.register(name, _hidden_task, task)
}

func (m *menu) RegisterHidden(task Task) {
	m.register(task.Name(), _hidden_task, task)
}

// Sets the flags for the task.
func set_task_flags(task Task, Flags FlagSet) {
	T := task.Get()
	T.Flags = Flags
}

// Initializes the task's database
func set_task_db(task Task, DB Database) {
	T := task.Get()
	T.DB = DB
	T.Cache = global.cache
	for _, k := range T.Cache.Tables() {
		T.Cache.Drop(k)
	}
}

// Sets a report for the task.
func set_task_report(task Task, input *TaskReport) {
	T := task.Get()
	T.Report = input
}

// Sets the kw api session for the task
func set_task_session(task Task, user KWSession) {
	T := task.Get()
	T.KW = user
}

// Provides summary of the task after completion.
func task_report_summary(task Task, errors uint32) {
	T := task.Get()
	if T.Report == nil {
		return
	}
	T.Report.Summary(errors)
}

// Registers a task with the task menu.
func (m *menu) register(name string, t_flag uint, task Task) {
	desc := task.Desc()

	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.entries == nil {
		m.entries = make(map[string]*menu_elem)
	}
	cmd_name := strings.Split(name, ":")[0]
	flags := &FlagSet{EFlagSet: eflag.NewFlagSet(cmd_name, eflag.ReturnErrorOnly)}
	flags.SyntaxName(fmt.Sprintf("%s %s", os.Args[0], cmd_name))
	flags.ShowSyntax = true
	flags.AdaptArgs = true

	m.entries[name] = &menu_elem{
		name:   name,
		desc:   desc,
		t_flag: t_flag,
		task:   task,
		flags:  flags,
		parsed: false,
	}
	my_entry := m.entries[name]

	//my_entry.flags.Header = fmt.Sprintf("[%s]: \"%s\"\n", strings.Split(fmt.Sprintf("%s", name), ":")[0], desc)
	my_entry.flags.BoolVar(&global.debug, "debug", NONE)
	my_entry.flags.BoolVar(&global.snoop, "snoop", NONE)
	my_entry.flags.BoolVar(&global.sysmode, "quiet", NONE)
	my_entry.flags.DurationVar(&global.freq, "repeat", global.freq, NONE)
	my_entry.flags.BoolVar(&global.new_task_file, "new_task", NONE)
	my_entry.flags.BoolVar(&global.pause, "pause", NONE)
	if global.auth_mode == SIGNATURE_AUTH {
		flags.StringVar(&global.as_user, "run_as", "<user@domain.com>", NONE)
	}

	switch t_flag {
	case _admin_task:
		m.admin_tasks = append(m.admin_tasks, name)
	case _hidden_task:
		m.hidden_tasks = append(m.hidden_tasks, name)
	default:
		m.tasks = append(m.tasks, name)
	}
}

// Read menu items.
func (m *menu) Show() {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	var items []string

	if m.text == nil {
		m.text = tabwriter.NewWriter(os.Stderr, 25, 1, 3, '.', 0)
	}

	for _, k := range m.tasks {
		m.cmd_text(k, m.entries[k].desc)
	}

	var user_cmd_prompt string

	if global.auth_mode == SIGNATURE_AUTH {
		user_cmd_prompt = "User Commands:\n"
	} else {
		user_cmd_prompt = "Commands:\n"
	}

	if m.tasks != nil && len(m.tasks) > 0 {
		os.Stderr.Write([]byte(user_cmd_prompt))
		m.text.Write([]byte(fmt.Sprintf("\n")))
		m.text.Flush()
	}

	items = items[0:0]

	if global.auth_mode == SIGNATURE_AUTH {
		for _, k := range m.admin_tasks {
			m.cmd_text(k, m.entries[k].desc)
		}
		if m.admin_tasks != nil && len(m.admin_tasks) > 0 {
			os.Stderr.Write([]byte("Admin Commands:\n"))
			m.text.Write([]byte(fmt.Sprintf("\n")))
			m.text.Flush()
		}
	}

	os.Stderr.Write([]byte(fmt.Sprintf("For extended help on any task, type %s <command> --help.\n", os.Args[0])))
}

func get_taskstore(name string, is_admin bool) Database {
	if is_admin {
		return global.db.Bucket(name)
	} else {
		global.db.Get("kitebroker", "account", &global.user.Username)
		return global.db.Sub(global.user.Username).Sub(name)
	}
}

func write_task_file(name, desc string, flags *FlagSet) {
	Stdout("### %s: %s ###\n\n", name, desc)
	Stdout("[%s]", name)
	Stdout("\n")
	get_flag := func(input *eflag.Flag) {
		if input.Usage == NONE {
			return
		}
		Stdout("# %s\n#\n", input.Usage)
		if len(input.Value.String()) == 0 {
			if input.DefValue[0] == '"' {
				Stdout("#%s = \"%s\"", input.Name, input.DefValue[2:len(input.DefValue)-2])
			} else {
				Stdout("#%s = \"%s\"", input.Name, input.DefValue[1:len(input.DefValue)-1])
			}
		} else {
			Stdout("%s = %s", input.Name, input.Value.String())
		}
		Stdout("\n")
	}
	flags.VisitAll(get_flag)
}

func (m *menu) Select(input [][]string) (err error) {
	for input == nil || len(input) == 0 {
		return eflag.ErrHelp
	}

	// Remove admin tools if not set to SIGNATURE_AUTH
	if global.auth_mode != SIGNATURE_AUTH {
		m.mutex.Lock()
		for _, k := range m.admin_tasks {
			delete(m.entries, k)
		}
		m.mutex.Unlock()
	}

	// Initialize selected task.
	init := func(args []string) (err error) {
		source := args[len(args)-1]
		if x, ok := m.entries[args[0]]; ok {
			x.flags.FlagArgs = args[1 : len(args)-1]
			var show_help bool
			if len(x.flags.FlagArgs) == 0 && !global.new_task_file {
				show_help = true
			}

			set_task_db(x.task, get_taskstore(strings.Split(x.name, ":")[0], x.t_flag != _normal_task))
			set_task_flags(x.task, *x.flags)

			if err := x.task.Init(); err != nil {
				if show_help || err == eflag.ErrHelp {
					x.flags.Usage()
					Exit(0)
				}
				if len(x.flags.Args()) == 0 && global.new_task_file {
					write_task_file(x.name, x.desc, x.flags)
					Exit(0)
				}
				if err != eflag.ErrHelp {
					if source != "cli" {
						Stderr("err [%s]: %s\n\n", source, err.Error())
					} else {
						Stderr("err: %s\n\n", err.Error())
					}
				}
				x.flags.Usage()
				Exit(1)
			} else {
				if global.new_task_file {
					write_task_file(x.name, x.desc, x.flags)
					Exit(0)
				}
				x.parsed = true
			}
		}
		return nil
	}

	// Initilize database
	init_database()

	// Initialize all tasks from task files and cli arguments.
	for n, args := range input {
		m.mutex.RLock()
		source := args[len(args)-1]
		if x, ok := m.entries[args[0]]; ok {
			if !x.parsed {
				init(args)
			} else {
				// Task already is initialized, so clone the task and parse new variables provided by task file.
				i := 0
				for k := range m.entries {
					if strings.Contains(k, args[0]) {
						i++
					}
				}
				new_name := fmt.Sprintf("%s:%d", x.name, i)
				new_task := x.task.New()
				m.mutex.RUnlock()
				command.RegisterName(new_name, new_task)
				m.mutex.RLock()
				input[n][0] = new_name
				init(args)
			}
		} else {
			if source == "cli" {
				return fmt.Errorf("No such command: '%s' found.\n\n", args[0])
			} else {
				if source != "exit" {
					return fmt.Errorf("%s: No such command: '%s' found.\n\n", args[len(args)-1], args[0])
				}
			}
		}
		m.mutex.RUnlock()
	}

	if global.debug || global.snoop {
		enable_debug()
	}

	init_kw_api()
	/*if !global.sysmode {
		nfo.ShowTS()
	}*/

	if global.gen_token {
		return
	}

	Info("### %s v%s ###", APPNAME, VERSION)
	Info(NONE)

	// Main task loop.
	for {
		tasks_loop_start := time.Now().Round(time.Millisecond)
		task_count := len(input) - 1
		for i, args := range input {
			m.mutex.RLock()
			if x, ok := m.entries[args[0]]; ok {
				if x.parsed {
					ProgressBar.Done()
					DefaultPleaseWait()
					PleaseWait.Show()
					name := strings.Split(x.name, ":")[0]
					source := args[len(args)-1]
					pre_errors := ErrCount()
					if source == "cli" {
						Info("<-- task '%s' started -->", name)
					} else {
						Info("<-- task '%s' (%s) started -->", name, source)
					}
					Info("\n")

					set_task_session(x.task, global.user)
					set_task_report(x.task, NewTaskReport(name, source, x.flags))
					report := Defer(func() error {
						task_report_summary(x.task, ErrCount()-pre_errors)
						return nil
					})
					if err := x.task.Main(); err != nil {
						Err(err)
					}
					DefaultPleaseWait()
					report()
					if source == "cli" {
						Info("<-- task '%s' stopped -->", name)
					} else {
						Info("<-- task '%s' (%s) stopped -->", name, source)
					}
					if i < task_count {
						Info(NONE)
					}
				}
			}
			m.mutex.RUnlock()
		}

		PleaseWait.Hide()

		// Stop here if this is non-continuous.
		if global.freq == 0 {
			return nil
		}

		runtime.GC()

		// Task Loop
		if ctime := time.Now().Add(time.Duration(tasks_loop_start.Round(time.Second).Sub(time.Now().Round(time.Second)) + global.freq)).Round(time.Second); ctime.Unix() > time.Now().Round(time.Second).Unix() && ctime.Sub(time.Now().Round(time.Second)) >= time.Second {
			Info(NONE)
			Info("Next task cycle will begin at %s.", ctime)
			for time.Now().Sub(tasks_loop_start) < global.freq {
				ctime := time.Duration(global.freq - time.Now().Round(time.Second).Sub(tasks_loop_start)).Round(time.Second)
				Flash("* Task cycle will restart in %s.", ctime.String())
				if ctime > time.Second {
					time.Sleep(time.Duration(time.Second))
				} else {
					time.Sleep(ctime)
					break
				}
			}
		}
		Info("Restarting task cycle ... (%s has elapsed since last run.)", time.Now().Round(time.Second).Sub(tasks_loop_start).Round(time.Second))
		Info(NONE)
	}
	return nil
}
