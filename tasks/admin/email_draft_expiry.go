package admin

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"time"
)

type EmailDraftExpiryTask struct {
	limiter      LimitGroup
	resume       bool
	user_emails  []string
	drafts       Tally
	attachments  Tally
	size         Tally
	expire       time.Time
	dry_run      bool
	users        Table
	user_counter Tally
	KiteBrokerTask
}

func (T EmailDraftExpiryTask) New() Task {
	return new(EmailDraftExpiryTask)
}

func (T EmailDraftExpiryTask) Name() string {
	return "email_draft_expiry"
}

func (T EmailDraftExpiryTask) Desc() string {
	return "Expire email drafts and attachments older than specified date."
}

func (T *EmailDraftExpiryTask) Init() (err error) {
	T.Flags.BoolVar(&T.resume, "resume", "Resume previous cleanup.")
	expiry := T.Flags.String("expiry", "<YYYY-MM-DD>", "Expire drafts and their files older than specified date.")
	T.Flags.BoolVar(&T.dry_run, "dry-run", "Don't delete just display what would be deleted.")
	T.Flags.MultiVar(&T.user_emails, "users", "<user@domain.com>", "Users to specify.")
	T.Flags.Order("expiry", "users", "dry-run", "resume")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	if IsBlank(*expiry) {
		return fmt.Errorf("Please specify an --expiry date.")
	}

	T.expire, err = StringDate(*expiry)
	if err != nil {
		return err
	}
	return
}

func (T *EmailDraftExpiryTask) Main() (err error) {
	T.limiter = NewLimitGroup(5)

	report_bool := func(input int64) string {
		if input == 0 {
			return "No"
		} else {
			return "Yes"
		}
	}

	T.users = T.DB.Table("users")
	T.user_counter = T.Report.Tally("Users")
	T.drafts = T.Report.Tally("Drafts")
	T.attachments = T.Report.Tally("Attachments")
	T.size = T.Report.Tally("Attachment Size", HumanSize)
	report := T.Report.Tally("Dry-Run", report_bool)
	if T.dry_run {
		report.Add(1)
	}

	params := Query{"active": true, "verified": true}

	user_count, err := T.KW.Admin().UserCount(T.user_emails, params)
	if err != nil {
		return err
	}

	ProgressBar.New("users", user_count)
	defer ProgressBar.Done()

	user_getter := T.KW.Admin().Users(T.user_emails, params)

	for {
		users, err := user_getter.Next()
		if err != nil {
			return err
		}
		if len(users) == 0 {
			break
		}
		for _, user := range users {
			if T.resume && T.users.Get(user.Email, nil) {
				ProgressBar.Add(1)
				T.user_counter.Add(1)
				continue
			}
			T.limiter.Add(1)
			go func(user KiteUser) {
				defer T.limiter.Done()
				defer ProgressBar.Add(1)
				defer T.user_counter.Add(1)
				err = T.ProcessDrafts(user.Email)
				if err != nil {
					Err("%s: %v", user.Email, err)
					return
				}
				T.users.Set(user.Email, 1)
			}(user)
		}
	}

	T.limiter.Wait()

	if ErrCount() == 0 {
		T.DB.Drop("users")
	}

	return
}

func (T *EmailDraftExpiryTask) ProcessDrafts(user string) (err error) {
	sess := T.KW.Session(user)
	type kite_mail struct {
		ID     string `json:"ID"`
		Sender struct {
			Email string `json:"email"`
		} `json:"sender"`
		Recipients []struct {
			Email string `json:"email"`
		} `json:"recipients"`
		Subject string `json:"subject"`
		Date    string
	}

	offset := 0
	for {
		var mail_ids []kite_mail
		err = sess.DataCall(APIRequest{
			Method: "GET",
			Path:   "/rest/mail",
			Params: SetParams(Query{"deleted": false, "bucket": "draft", "date:lte": WriteKWTime(T.expire), "with": "(sender)"}),
			Output: &mail_ids,
		}, offset, 1000)
		if err != nil {
			return err
		}
		T.drafts.Add(len(mail_ids))
		if len(mail_ids) == 0 {
			break
		}
		offset = offset + len(mail_ids)
		for _, v := range mail_ids {
			err = T.ShowAttachments(&sess, v.ID)
			if err != nil && err != ErrNotFound {
				Err("%s: %v", user, err)
				continue
			} else if err == ErrNotFound {
				created, _ := ReadKWTime(v.Date)
				Log("[%d] User: %s Created: %v - No attachments in draft.", v.ID, user, created.Local())
			}

			if !T.dry_run {
				err = sess.Call(APIRequest{
					Method: "DELETE",
					Path:   SetPath("/rest/mail/%d", v.ID),
				})
				if err != nil {
					Err("[%d] %s: %v", v.ID, user, err)
				}
			}
		}
		continue
	}
	return T.CleanMailDir(&sess)
}

func (T *EmailDraftExpiryTask) ShowAttachments(sess *KWSession, mail_id string) (err error) {
	var attachments []struct {
		AttachmentID string `json:"attachmentId"`
		VersionID    string `json:"versionFileId"`
	}

	err = sess.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/mail/%s/attachments", mail_id),
		Params: SetParams(Query{"with": "(package)"}),
		Output: &attachments,
	}, -1, 1000)
	if err != nil {
		return err
	}

	T.attachments.Add(len(attachments))
	if len(attachments) == 0 {
		return ErrNotFound
	}
	for _, f := range attachments {
		finfo, err := sess.File(f.VersionID).Info()
		if err != nil && IsAPIError(err, "ERR_ACCESS_USER") {
			finfo, err = sess.File(f.AttachmentID).Info()
			if err != nil {
				Notice("[%d] Attachment information is unavailable: %v", mail_id, err)
			}
		}
		created, _ := ReadKWTime(finfo.Created)
		Log("[%d] User: %s, Filename: %s, Size: %s, Created: %v", mail_id, sess.Username, finfo.Name, HumanSize(finfo.Size), created.Local())
		T.size.Add64(finfo.Size)
	}

	return
}

func (T *EmailDraftExpiryTask) CleanMailDir(sess *KWSession) (err error) {
	my_user, err := sess.MyUser()
	if err != nil {
		return err
	}
	files, err := sess.Folder(my_user.MyDirID).Files(Query{"deleted": true})
	if err != nil {
		return err
	}
	for _, f := range files {
		created, _ := ReadKWTime(f.Created)
		Log("[Deleted Mail File] User: %s, Filename: %s, Size: %s, Created: %v", my_user.Email, f.Name, HumanSize(f.Size), created.Local())
		T.size.Add64(f.Size)
		if !T.dry_run {
			if err := sess.File(f.ID).PermDelete(); err != nil {
				Err("%s: %v", f.Name, err)
				continue
			}
		}
	}
	return
}
