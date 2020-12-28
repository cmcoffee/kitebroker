package user

import (
	. "github.com/cmcoffee/kitebroker/core"
	"time"
)

// Object for task.
type ListTask struct {
	input struct {
		folder string
		human_readable bool
	}
	KiteBrokerTask
}

// Task objects need to be able create a new copy of themself.
func (T ListTask) New() Task {
	return new(ListTask)
}

func (T ListTask) Name() string {
	return "ls"
}

func (T ListTask) Desc() string { 
	return "List folders and/or files in kiteworks."
}

// Task init function, should parse flag, do pre-checks.
func (T *ListTask) Init() (err error) {
	T.Flags.BoolVar(&T.input.human_readable, "human_readable", false, "Present sizes in human-readable format.")
	T.Flags.Alias("human_readable", "h")
	T.Flags.StringVar(&T.input.folder, "dest", "<destination file/folder>", "Folder/Files you want to list.")
	err = T.Flags.Parse()
	if err != nil {
		return err
	}
	if len(T.input.folder) == 0 && len(T.Flags.Args()) > 0 {
		T.input.folder = T.Flags.Args()[0]
	}


	return nil
}

func (T ListTask) displayResult(object...KiteObject) {
	for _, v := range object {
		if v.Type == "d" {
			var t_str string
			if mod, err := ReadKWTime(v.Modified); err != nil {
				t_str = v.Modified
			} else {
				t_str = mod.In(time.Local).String()
			}
			Log("%s [folder] %s", t_str, v.Name)
		} else {
			var t_str string
			if mod, err := ReadKWTime(v.ClientModified); err != nil {
				t_str = v.ClientModified
			} else {
				t_str = mod.In(time.Local).String()
			}
			if !T.input.human_readable {
				Log("%s %d %s", t_str, v.Size, v.Name)
			} else {
				Log("%s %s %s", t_str, HumanSize(v.Size), v.Name)
			}
		}
	}
}

// Main function, Passport hands off KWAPI Session, a Database and a TaskReport object.
func (T *ListTask) Main() (err error) {
	Info("\n")
	if IsBlank(T.input.folder) {
		Info("-- 'kiteworks Files' --")
		folders, err := T.KW.TopFolders()
		if err != nil {
			return err
		}
		T.displayResult(folders[0:]...)
	} else {
		Info("-- '%s' --", T.input.folder)
		f, err := T.KW.Folder(0).Find(T.input.folder)
		if err != nil {
			return err
		}
		if f.Type == "f" {
			T.displayResult(f)
			return nil
		}
		childs, err := T.KW.Folder(f.ID).Contents()
		if err != nil {
			return err
		}
		T.displayResult(childs[0:]...)
	}

	return
}
