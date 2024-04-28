package sftp_gw

import (
	. "github.com/cmcoffee/kitebroker/core"
	//"golang.org/x/crypto/ssh"
	//"net"
)

type SFTPGWTask struct {
	// input variables
	input struct {
		input_flag string
	}
	// Required for all tasks
	KiteBrokerTask
}

func (T SFTPGWTask) New() Task {
	return new(SFTPGWTask)
}

func (T SFTPGWTask) Name() string {
	return "sftp_gateway"
}

func (T SFTPGWTask) Desc() string {
	return "" // Incomplete
}

func (T *SFTPGWTask) Init() (err error) {
	T.Flags.StringVar(&T.input.input_flag, "flag", "<example text>", "Example string value")
	T.Flags.Order("flag")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	return
}

func (T *SFTPGWTask) Main() (err error) {
	// Main function
	return nil
}
