package core

import (
	"fmt"
)

// MailboxArchiveConfig configures a local mailbox archive run.
type MailboxArchiveConfig struct {
	// Root is the local directory the archive is written under. Each mailbox
	// gets its own [user@domain] subfolder beneath it.
	Root string
	// PageSize is the number of messages requested per API page. The mailbox is
	// walked one page at a time, so this also bounds peak memory.
	PageSize int
	// Embed writes attachments into each .eml as multipart parts rather than to
	// a sidecar Attachments folder.
	Embed bool
	// SkipAttachments archives message bodies only.
	SkipAttachments bool
	// Track records archived mail ids in Tracker so re-runs resume rather than
	// re-downloading. Requires Tracker to be set.
	Track   bool
	Tracker Table
	// Optional tallies, updated as the run proceeds.
	Archived    Tally
	Skipped     Tally
	Transferred Tally
}

// tallyAdd increments a tally only when the caller supplied one; a zero-value
// Tally carries no counter and would panic.
func tallyAdd(t Tally, n int) {
	if t.count != nil {
		t.Add(n)
	}
}

// ArchiveMailbox archives every message in the session user's mailbox to the
// local filesystem, walking the mailbox one page at a time so that memory use
// does not scale with mailbox size.
//
// Individual message failures are reported and skipped; only an error
// enumerating the mailbox itself aborts the run.
func ArchiveMailbox(sess KWSession, cfg MailboxArchiveConfig) (err error) {
	if IsBlank(cfg.Root) {
		return fmt.Errorf("ArchiveMailbox: a local root path is required.")
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 250
	}

	opts := LocalArchiveOptions{
		RootPath:        cfg.Root,
		Embed:           cfg.Embed,
		SkipAttachments: cfg.SkipAttachments,
	}
	if cfg.Transferred.count != nil {
		opts.Transferred = cfg.Transferred.Add
	}

	track := cfg.Track && cfg.Tracker.table != nil

	var total int

	err = sess.MailListEach(cfg.PageSize, func(page []KiteMail) error {
		for _, m := range page {
			// Key on the mail id plus its date so an edited or re-sent message
			// with a recycled id is not mistaken for one already archived.
			record := fmt.Sprintf("%s:%s", m.ID, m.Date)

			if track && cfg.Tracker.Get(record, nil) {
				Debug("[%s]: Mail %s already archived; skipping.", sess.Username, m.ID)
				tallyAdd(cfg.Skipped, 1)
				continue
			}

			if err := sess.Mail(m.ID).ArchiveLocal(opts); err != nil {
				Err("[%s]: Error archiving mail '%s': %v", sess.Username, m.Subject, err)
				continue
			}

			if track {
				cfg.Tracker.Set(record, 1)
			}
			tallyAdd(cfg.Archived, 1)
			total++
		}
		Debug("[%s]: Archived %d messages so far.", sess.Username, total)
		return nil
	}, Query{"with": "(subject,body,rawBody,webFormId,emailFrom)", "deleted": false})

	if err != nil {
		return err
	}

	if total == 0 {
		Log("[%s]: No new mail found to archive.", sess.Username)
	}

	return nil
}
