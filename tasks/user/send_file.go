package user

import (
	. "github.com/cmcoffee/kitebroker/core"
	"fmt"
)

// Object for task.
type SendFileTask struct {
	in struct {
		to []string
		cc []string
		bcc []string
		expire_days int
		subj string
		body string
		empty bool
		src []string
	}
	KiteBrokerTask
}

func (T SendFileTask) New() Task {
	return new(SendFileTask)
}

func (T SendFileTask) Name() string {
	return "send_file"
}

func (T SendFileTask) Desc() string {
	return "Send files/folders in kiteworks."
}

// Task init function, should parse flag, do pre-checks.
func (T *SendFileTask) Init() (err error) {
	T.Flags.MultiVar(&T.in.to, "to", "<email addresses>", "Recipient(s) to send file to.")
	T.Flags.MultiVar(&T.in.cc, "cc", "<email addresses>", "Recipient(s) to carbon copy send file to.")
	T.Flags.MultiVar(&T.in.bcc, "bcc", "<email addresses>", "Recipient(s) to blind carbon copy send file to.")
	T.Flags.StringVar(&T.in.subj, "subj", "<email subject>", "Subject of send file email.")
	T.Flags.StringVar(&T.in.body, "body", "<message body>", "Body of send file email.")
	T.Flags.MultiVar(&T.in.src, "src", "<folder/file>", "Folder or file you wish to send.")
	T.Flags.BoolVar(&T.in.empty, "allow_empty", false, "Allow email to be sent without files.")
	T.Flags.Order("to","cc","bcc","subj","body")

	err = T.Flags.Parse()
	if err != nil {
		return err
	}

	if len(T.in.to) == 0 {
		return fmt.Errorf("Please specify a recipient to send --to.")
	}

	if len(T.in.src) == 0 && !T.in.empty {
		return fmt.Errorf("--allow_empty is required if no --src's are provided.")
	}

	return nil
}

// Main function, Passport hands off KWAPI Session, a Database and a TaskReport object.
func (T *SendFileTask) Main() (err error) {
	return
}
