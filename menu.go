package main

import (
	"fmt"
	. "github.com/cmcoffee/go-kwlib"
	"github.com/cmcoffee/go-snuglib/eflag"
	"github.com/cmcoffee/go-snuglib/nfo"
	"github.com/cmcoffee/kitebroker/common"
	"os"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

var jobs menu

// Menu for tasks.
type menu struct {
	mutex   sync.RWMutex
	text    *tabwriter.Writer
	entries map[string]*menu_elem
}

// Write out menu item.
func (m *menu) cmd_text(cmd string, desc string) {
	if m.text == nil {
		m.text = tabwriter.NewWriter(os.Stderr, 34, 8, 1, ' ', 0)
	}
	m.text.Write([]byte(fmt.Sprintf("  %s\t%s\n", cmd, desc)))
}

// Menu item.
type menu_elem struct {
	name   string
	desc   string
	admin  bool
	parsed bool
	task   common.Task
	flags  *common.FlagSet
}

// Registers an admin task.
func (m *menu) RegisterAdmin(name, desc string, task common.Task) {
	m.Register(name, desc, task)
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.entries[name].admin = true
}

// Registers a task with the task menu.
func (m *menu) Register(name, desc string, task common.Task) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.entries == nil {
		m.entries = make(map[string]*menu_elem)
	}
	flags := &common.FlagSet{EFlagSet: eflag.NewFlagSet(strings.Split(fmt.Sprintf("%s", name), ":")[0], eflag.ReturnErrorOnly)}

	m.entries[name] = &menu_elem{
		name:  name,
		desc:  desc,
		task:  task,
		flags: flags,
	}
	my_entry := m.entries[name]
	my_entry.flags.Header = fmt.Sprintf("desc: \"%s\"\n", desc)
	my_entry.flags.BoolVar(&global.snoop, "snoop", global.snoop, NONE)
	my_entry.flags.DurationVar(&global.freq, "repeat", global.freq, NONE)
}

// Read menu items.
func (m *menu) Show() {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	var items []string
	for k, v := range m.entries {
		if !v.admin {
			items = append(items, k)
		}
	}
	sort.Strings(items)
	for _, k := range items {
		m.cmd_text(k, m.entries[k].desc)
	}

	if m.text == nil {
		m.text = tabwriter.NewWriter(os.Stderr, 34, 8, 1, ' ', 0)
	}

	if len(items) > 0 {
		os.Stderr.Write([]byte(fmt.Sprintf("Available '%s' commands:\n", os.Args[0])))
		m.text.Write([]byte(fmt.Sprintf("\n")))
		m.text.Flush()
	}

	items = items[0:0]

	if global.auth_mode == SIGNATURE_AUTH {
		for k, v := range m.entries {
			if v.admin {
				items = append(items, k)
			}
		}
		for _, k := range items {
			m.cmd_text(k, m.entries[k].desc)
		}
		if len(items) > 0 {
			os.Stderr.Write([]byte(fmt.Sprintf("Available '%s' administrative commands:\n", os.Args[0])))
			m.text.Write([]byte(fmt.Sprintf("\n")))
			m.text.Flush()
		}
	}

	os.Stderr.Write([]byte(fmt.Sprintf("For extended help on any task, type %s <command> --help.\n", os.Args[0])))
}

func (m *menu) Select(input [][]string) (err error) {
	for input == nil || len(input) == 0 {
		return eflag.ErrHelp
	}

	// Remove admin tools if not set to SIGNATURE_AUTH
	if global.auth_mode != SIGNATURE_AUTH {
		m.mutex.Lock()
		for k, v := range m.entries {
			if v.admin {
				delete(m.entries, k)
			}
		}
		m.mutex.Unlock()
	}

	// Initialize selected task.
	init := func(args []string) (err error) {
		source := args[len(args)-1]
		if x, ok := m.entries[args[0]]; ok {
			x.flags.Args = args[1 : len(args)-1]
			if err := x.task.Init(x.flags); err != nil {
				if err != eflag.ErrHelp {
					if source != "cli" {
						Stderr("error [%s]: %s\n\n", source, err.Error())
					} else {
						Stderr("error: %s\n\n", err.Error())
					}
				}
				x.flags.Usage()
				if err == eflag.ErrHelp {
					os.Exit(0)
				} else {
					os.Exit(1)
				}
			} else {
				x.parsed = true
			}
		}
		return nil
	}

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
				for k, _ := range m.entries {
					if strings.Contains(k, args[0]) {
						i++
					}
				}
				new_name := fmt.Sprintf("%s:%d", x.name, i)
				new_task := x.task.New()
				m.mutex.RUnlock()
				jobs.Register(new_name, x.desc, new_task)
				m.mutex.RLock()
				input[n][0] = new_name
				init(args)
			}
		} else {
			if source == "cli" {
				return fmt.Errorf("No such command: '%s' found.\n\n", args[0])
			} else {
				return fmt.Errorf("%s: No such command: '%s' found.\n\n", args[len(args)-1], args[0])
			}
		}
		m.mutex.RUnlock()
	}

	init_kw_api()
	ShowTS()
	Log("### %s %s ###", NAME, VERSION)
	Log(NONE)

	// Main task loop.
	for {
		tasks_loop_start := time.Now().Round(time.Second)
		task_count := len(input) - 1
		for i, args := range input {
			m.mutex.RLock()
			if x, ok := m.entries[args[0]]; ok {
				if x.parsed {
					nfo.PleaseWait.Show()
					name := strings.Split(x.name, ":")[0]
					source := args[len(args)-1]
					if source == "cli" {
						Log("<-- task '%s' started. -->", name)
					} else {
						Log("<-- task '%s' (%s) started. -->", name, source)
					}
					start := time.Now().Round(time.Second)
					if err := x.task.Main(common.Passport{User: common.Session{&global.user}, DB: common.NewDS(name, global.db)}); err != nil {
						Err(err)
					}
					if source == "cli" {
						Log("<-- task '%s' took %s to complete. -->", name, time.Now().Sub(start).Round(time.Second))
					} else {
						Log("<-- task '%s' (%s) took %s to complete. -->", name, source, time.Now().Sub(start).Round(time.Second))
					}
					if i < task_count {
						Log(NONE)
					}
				}
			}
			m.mutex.RUnlock()
		}

		PleaseWait.Hide()
		Log(NONE)
		Log("Total task time: %s.", time.Now().Sub(tasks_loop_start).Round(time.Second))

		// Stop here if this is non-continous.
		if global.freq == 0 {
			return nil
		}

		// Task Loop
		if ctime := time.Now().Add(time.Duration(tasks_loop_start.Round(time.Second).Sub(time.Now().Round(time.Second)) + global.freq)).Round(time.Second); ctime.Unix() > time.Now().Round(time.Second).Unix() && ctime.Sub(time.Now().Round(time.Second)) >= time.Second {
			Log("Next task cycle will begin at %s.", ctime)
			for time.Now().Sub(tasks_loop_start) < global.freq {
				ctime := time.Duration(global.freq - time.Now().Round(time.Second).Sub(tasks_loop_start))
				Flash("* Task cycle will restart in %s.", ctime.String())
				if ctime > time.Second {
					time.Sleep(time.Duration(time.Second))
				} else {
					time.Sleep(ctime)
					break
				}
			}
		}
		Log("Restarting task cycle ... (%s has elapsed since last run.)", time.Now().Round(time.Second).Sub(tasks_loop_start))
		Log(NONE)
	}
	return nil
}
