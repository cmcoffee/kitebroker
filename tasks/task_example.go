package main

import (
	. "github.com/cmcoffee/kitebroker/core"
)

type ExampleTask struct {
	// input variables
	input struct {
		input_flag string
	}
	// Required for all tasks
	KiteBrokerTask
}

func (T ExampleTask) New() Task {
	return new(ExampleTask)
}

func (T ExampleTask) Name() string {
	return "example_task"
}

func (T ExampleTask) Desc() string {
	return "Example task."
}

func (T *ExampleTask) Init() (err error) {
	T.Flags.StringVar(&T.input.input_flag, "flag", "<example text>", "Example string value")
	T.Flags.Order("flag")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	return
}

func (T *ExampleTask) Main() (err error) {
	// Main function
}