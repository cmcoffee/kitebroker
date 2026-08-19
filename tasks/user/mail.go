package user

import (
	"fmt"
	"path/filepath"

	. "github.com/cmcoffee/kitebroker/core"
)

func init() { RegisterTask(new(MailTask)) }

type MailTask struct {
	input struct {
		dst              string
		page_size        int
		embed            bool
		skip_attachments bool
		no_track         bool
	}
	db struct {
		archived Table
	}
	mail_count  Tally
	skip_count  Tally
	transferred Tally
	KiteBrokerTask
}

func (T MailTask) Name() string {
	return "archive_mail"
}

func (T MailTask) Desc() string {
	return "Archive Kiteworks Mail to a local folder."
}

// Init Task init function, should parse flag, do pre-checks.
func (T *MailTask) Init() (err error) {
	T.Flags.StringVar(&T.input.dst, "dst", "<local folder>", "Specify local folder to store the mail archive.")
	T.Flags.BoolVar(&T.input.embed, "embed", "Embed attachments in each .eml file instead of writing them alongside it.")
	T.Flags.BoolVar(&T.input.skip_attachments, "no_attachments", "Archive messages only, do not download attachments.")
	T.Flags.BoolVar(&T.input.no_track, "no_track", "Re-archive mail that was already archived on a previous run.")
	T.Flags.IntVar(&T.input.page_size, "page_size", 250, "Number of messages to request per API page.")
	T.Flags.Order("dst", "embed", "no_attachments", "no_track", "page_size")
	T.Flags.InlineArgs("dst")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	if IsBlank(T.input.dst) {
		return fmt.Errorf("must specify a local destination folder with --dst.")
	}

	if T.input.page_size < 1 || T.input.page_size > 1000 {
		return fmt.Errorf("--page_size must be between 1 and 1000.")
	}

	return nil
}

func (T *MailTask) Main() (err error) {
	T.db.archived = T.DB.Table("archived_mail")
	T.mail_count = T.Report.Tally("Mail Archived")
	T.skip_count = T.Report.Tally("Mail Skipped")
	T.transferred = T.Report.Tally("Transferred", HumanSize)

	T.input.dst, err = filepath.Abs(T.input.dst)
	if err != nil {
		return err
	}
	if err = MkDir(T.input.dst); err != nil {
		return err
	}

	Log("Archiving all mail in %s's mailbox to %s ...", T.KW.Username, T.input.dst)

	return ArchiveMailbox(T.KW, MailboxArchiveConfig{
		Root:            T.input.dst,
		PageSize:        T.input.page_size,
		Embed:           T.input.embed,
		SkipAttachments: T.input.skip_attachments,
		Track:           !T.input.no_track,
		Tracker:         T.db.archived,
		Archived:        T.mail_count,
		Skipped:         T.skip_count,
		Transferred:     T.transferred,
	})
}
