package main

// Find Messages Sent to User.
func (s Session) FindMail(filter *Query) (mail_id []int, err error) {
	if filter == nil { filter = &Query{} }
	(*filter)["deleted"] = false
	(*filter)["mode"] = "compact"

	type MailSummary struct {
		Data []struct {
			ID int `json:"id"`
			SenderID int `json:"senderId"`
		} `json:"data"`
	}

	var wrapout MailSummary

	if err = s.Call("GET", "/rest/mail", &wrapout, *filter); err != nil { return nil, err }
	if err != nil { return nil, err }

	myUser, err := s.MyUser()
	if err != nil { return nil, err }

	for _, m := range wrapout.Data {
		if m.SenderID == myUser.ID {
			continue
		}
		mail_id = append(mail_id, m.ID)
	}

	return
}

type MailData struct {
	ID int 
	SenderID int
	Recipients []struct {
		ID int
		Flag int
	}
	Subject string
	Body string
	Files []int
}
/*
type MailData struct {
		ID   int `json:"id"`
		SenderID int `json:"senderId"`
		Date 	string `json:"status"`
		PackageID int `json:"emailPackageId"`
  		Recipients []struct {
			UserID int `json:"userId"`
			Flag   int `json:"type"`
		} `json:"recipients"`
		Vars []struct {
			Variable string `json:"variable"`
			Value string `json:"value"`
		} `json:"variables"`
}
*/

func (s Session) GetMail(mail_id int) (output []MailData, err error) {

return nil, nil
}

func (s Session) SendFile(to, cc, bcc []string, subj, body string, filename...string) (err error) {
	myDir, err := s.MyMailFolderID()
	if err != nil { return err }

	var files []int

	for _, f := range filename {
		fid, err := s.Upload(f, myDir)
		if err != nil { return err }
		files = append(files, fid)
	}

	return s.Call("POST", "/rest/mail/actions/sendFile", nil, PostJSON{
		"to": to,
		"cc": cc,
		"bcc": bcc,
		"subject": subj,
		"body": body,
		"files": files,
		}, Query{"returnEntity":false})
}

/*
func (t *Task) DownloadInbox() (err error) {
	
}
*/