package admin

import (
	"fmt"
	"path/filepath"

	. "github.com/cmcoffee/kitebroker/core"
)

func init() { RegisterAdminTask(new(MailArchiveTask)) }

// MailArchiveTask archives the mailboxes of one or more users to the local
// filesystem. It is the local counterpart to the mail phase of the kiteworks
// migration task: same on-disk hierarchy, but nothing is uploaded.
type MailArchiveTask struct {
	input struct {
		dst              string
		user_emails      []string
		all_users        bool
		page_size        int
		threads          int
		embed            bool
		skip_attachments bool
		no_track         bool
	}
	db struct {
		archived Table
	}
	user_count  Tally
	mail_count  Tally
	skip_count  Tally
	transferred Tally
	KiteBrokerTask
}

func (T MailArchiveTask) Name() string {
	return "archive_user_mail"
}

func (T MailArchiveTask) Desc() string {
	return "Users:Archive Kiteworks Mail for users to a local folder."
}

func (T *MailArchiveTask) Init() (err error) {
	T.Flags.StringVar(&T.input.dst, "dst", "<local folder>", "Local folder to store the mail archive under.")
	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "User(s) whose mailbox should be archived.")
	T.Flags.BoolVar(&T.input.all_users, "all_users", "Archive mail for every user on the system.")
	T.Flags.BoolVar(&T.input.embed, "embed", "Embed attachments in each .eml file instead of writing them alongside it.")
	T.Flags.BoolVar(&T.input.skip_attachments, "no_attachments", "Archive messages only, do not download attachments.")
	T.Flags.BoolVar(&T.input.no_track, "no_track", "Re-archive mail that was already archived on a previous run.")
	T.Flags.IntVar(&T.input.page_size, "page_size", 250, "Number of messages to request per API page.")
	T.Flags.IntVar(&T.input.threads, "threads", 5, "Number of mailboxes to archive concurrently.")
	run := T.Flags.Bool("run", "Execute the task.")
	T.Flags.Order("dst", "users", "all_users", "embed", "no_attachments", "no_track", "page_size", "threads", "run")
	T.Flags.InlineArgs("dst")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	if !*run {
		return fmt.Errorf("Please specify --run to execute this task.")
	}

	if IsBlank(T.input.dst) {
		return fmt.Errorf("must specify a local destination folder with --dst.")
	}

	if len(T.input.user_emails) == 0 && !T.input.all_users {
		return fmt.Errorf("must specify --users or --all_users.")
	}

	if T.input.page_size < 1 || T.input.page_size > 1000 {
		return fmt.Errorf("--page_size must be between 1 and 1000.")
	}

	if T.input.threads < 1 {
		return fmt.Errorf("--threads must be at least 1.")
	}

	return nil
}

func (T *MailArchiveTask) Main() (err error) {
	T.db.archived = T.DB.Table("archived_mail")
	T.user_count = T.Report.Tally("Mailboxes")
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

	Log("Archiving mail to %s ...", T.input.dst)

	PleaseWait.Set(func() string {
		return fmt.Sprintf("Please wait ... [mailboxes: %d/mail: %d]", T.user_count.Value(), T.mail_count.Value())
	}, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})

	// Walk users a page at a time via the paged getter rather than materializing
	// the full user list, so a large system does not inflate memory before the
	// first mailbox is even touched.
	user_getter, err := T.KW.Admin().Users(T.input.user_emails, 0)
	if err != nil {
		return err
	}

	wg := NewLimitGroup(T.input.threads)

	for {
		users, err := user_getter.Next()
		if err != nil {
			return err
		}
		if len(users) == 0 {
			break
		}
		for _, u := range users {
			if u.Deleted || u.Suspended || !u.Verified {
				Debug("[%s]: Skipping deleted, suspended or unverified user.", u.Email)
				continue
			}
			wg.Add(1)
			go func(user KiteUser) {
				defer wg.Done()
				T.ArchiveUser(user)
			}(u)
		}
	}

	wg.Wait()

	return nil
}

// ArchiveUser archives a single user's mailbox.
func (T *MailArchiveTask) ArchiveUser(user KiteUser) {
	T.user_count.Add(1)

	// Each mailbox gets its own tracker sub-table so resume state for one user
	// can be dropped without disturbing the others.
	err := ArchiveMailbox(T.KW.Session(user.Email), MailboxArchiveConfig{
		Root:            T.input.dst,
		PageSize:        T.input.page_size,
		Embed:           T.input.embed,
		SkipAttachments: T.input.skip_attachments,
		Track:           !T.input.no_track,
		Tracker:         T.DB.Table(fmt.Sprintf("archived_mail:%s", user.Email)),
		Archived:        T.mail_count,
		Skipped:         T.skip_count,
		Transferred:     T.transferred,
	})
	if err != nil {
		Err("[%s]: Mail archive error: %v", user.Email, err)
	}
}
