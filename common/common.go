package common

import (
	"github.com/cmcoffee/go-kwlib"
	"github.com/cmcoffee/go-snuglib/eflag"
)

// Menu item flags
type FlagSet struct {
	Args []string
	*eflag.EFlagSet
}

// Prase flags assocaited with task.
func (f *FlagSet) Parse() (err error) {
	if err = f.EFlagSet.Parse(f.Args[0:]); err != nil {
		return err
	}
	return nil
}

type Passport struct {
	User kwlib.KWSession
	DB   *kwlib.Database
}

// Task Interface
type Task interface {
	Init(*FlagSet) error
	Main(*Passport) error
	New() Task
}
