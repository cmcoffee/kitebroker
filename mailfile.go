package main

import (
	"time"
)

const (
	PREPARING = 1 << iota
	UPLOADING
)

type sendmail struct {
	Time int64      	 	  `json:"date"`
	To []string 		  `json:"to"`
	Cc []string 		  `json:"cc"`
	Bcc []string 		  `json:"bcc"`
	Subj string 		  `json:"subj"`
	Body string 		  `json:"body"`
	Files []int			  `json:"files"`
}

func (s Session) SendFile() (err error) {
	rcpt := string(s)
	s = Session(Config.Get("configuration", "account"))

	mail := &sendmail {
		Time: time.Now().Unix(),
		To: Config.MGet("send_mail:opts", "to"),
		Cc: Config.MGet("send_mail:opts", "cc"),
		Bcc: Config.MGet("send_mail:opts", "bcc"),
		Subj: NONE,
		Body: NONE,
	}

	mail.To = append(mail.To, rcpt)

	DB.Set("sendmail", rcpt, &mail)

	return

}

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
/*
func (s Session) SendFile() (err error) {
	myDir, err := s.MyMailFolderID()
	if err != nil { return err }

	var file_ids []int

	for _, f := range o.Files {
		fid, err := s.Upload(f, myDir)
		if err != nil { return err }
		files = append(file_ids, fid)
	}

	return s.Call("POST", "/rest/mail/actions/sendFile", nil, PostJSON{
		"to": o.To,
		"cc": o.Cc,
		"bcc": o.Bcc,
		"subject": o.Subj,
		"body": o.Body,
		"files": file_ids,
		}, Query{"returnEntity":false})
}


func (t *Task) DownloadInbox() (err error) {
	
}
*/