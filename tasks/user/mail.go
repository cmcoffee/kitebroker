package user

import (
	. "github.com/cmcoffee/kitebroker/core"
)

//func init() { RegisterTask(new(MailTask)) }

type MailTask struct {
	input struct {
		dst string
	}
	KiteBrokerTask
}

func (T MailTask) Name() string {
	return "archive_mail"
}

func (T MailTask) Desc() string {
	return "Archive Kiteworks Mail."
}

// Init Task init function, should parse flag, do pre-checks.
func (T *MailTask) Init() (err error) {
	T.Flags.StringVar(&T.input.dst, "dst", "<remote folder>", "Specify root folder for mail archive.")
	T.Flags.Order("dst")
	T.Flags.InlineArgs("dst")
	if err = T.Flags.Parse(); err != nil {
		return err
	}
	return nil
}

func (T *MailTask) Main() (err error) {
	Log("Archiving all mail in %s's mailbox...", T.KW.Username)

	mail, err := T.KW.MailList(Query{"with": "(subject,body,rawBody,webFormId,emailFrom)", "deleted": false})
	if err != nil {
		return err
	}
	for _, m := range mail {
		err = T.KW.Mail(m.ID).Archive(T.input.dst)
		if err != nil {
			Err("%v\n", err)
		}
	}
	return
}
