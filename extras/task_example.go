package main

import (
	. "github.com/cmcoffee/kitebroker/core"
)

// ExampleTask encapsulates the components required to execute a specific task.
// It manages input flags and integrates with the KiteBrokerTask framework.
type ExampleTask struct {
	// input variables
	input struct {
		input_flag string
	}
	// Required for all tasks
	KiteBrokerTask
}

// New returns a new instance of the ExampleTask.
func (T ExampleTask) New() Task {
	return new(ExampleTask)
}

// Name returns the name of the task.
func (T ExampleTask) Name() string {
	return "example_task"
}

// Desc returns a description of the task.
// It provides a human-readable explanation of the task's purpose.
func (T ExampleTask) Desc() string {
	return "Example task."
}

// Init initializes the task flags and parses command-line arguments.
// It sets the input flag and handles potential parsing errors.
func (T *ExampleTask) Init() (err error) {
	T.Flags.StringVar(&T.input.input_flag, "flag", "<example text>", "Example string value")
	T.Flags.Order("flag")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	return nil
}

// Main is the primary function for task execution.
// It performs the core logic of the task.
func (T *ExampleTask) Main() (err error) {
	// Main function
	return nil
}
