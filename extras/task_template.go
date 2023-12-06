package tasks

import (
	. "github.com/cmcoffee/kitebroker/core"
)

// Object for task.
type TaskObject struct {
	bool_value     bool
	counter        Tally
	KiteBrokerTask // Every task object needs this!
}

// Task objects need to be able create a new copy of themself.
func (T *TaskObject) New() Task {
	return new(TaskObject)
}

// Task init function, should parse flag, do pre-checks.
func (T *TaskObject) Init() (err error) {
	T.Flags.BoolVar(&T.bool_value, "im_a_task", false, "I'm a task.")
	err = T.Flags.Parse()
	if err != nil {
		return err
	}
	// Put checks for values needed here.
	if T.bool_value {
		return fmt.Errorf("No you're not.")
	}

	return nil
}

// Main function, Passport hands off KWAPI Session, a Database and a TaskReport object.
func (T *TaskObject) Main() (err error) {
	T.counter = T.Report.Tally("Description for report here")
	T.counter.Report.Add(1)
	return
}
