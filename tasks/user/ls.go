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
	ppt        Passport
}

// Task objects need to be able create a new copy of themself.
func (T *ListTask) New() Task {
	return new(ListTask)
}

// Task init function, should parse flag, do pre-checks.
func (T *ListTask) Init(flag *FlagSet) (err error) {
	flag.BoolVar(&T.input.human_readable, "human_readable", false, "Present sizes in human-readable format.")
	flag.Alias("human_readable", "h")
	err = flag.Parse()
	if err != nil {
		return err
	}
	if len(flag.Args()) > 0 {
		T.input.folder = flag.Args()[0]
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
func (T *ListTask) Main(passport Passport) (err error) {
	T.ppt = passport
	Info("\n")
	if IsBlank(T.input.folder) {
		Info("-- 'kiteworks Files' --")
		folders, err := T.ppt.TopFolders()
		if err != nil {
			return err
		}
		T.displayResult(folders[0:]...)
	} else {
		Info("-- '%s' --", T.input.folder)
		f, err := T.ppt.Folder(0).Find(T.input.folder)
		if err != nil {
			return err
		}
		if f.Type == "f" {
			T.displayResult(f)
			return nil
		}
		childs, err := T.ppt.Folder(f.ID).Contents()
		if err != nil {
			return err
		}
		T.displayResult(childs[0:]...)
	}

	return
}
