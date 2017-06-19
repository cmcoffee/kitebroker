package main

import (
	"fmt"
	"github.com/cmcoffee/go-logger"
	"strings"
	"time"
	"strconv"
	"encoding/json"
	"path/filepath"
)

type marker struct{}

type KBMeta struct {
	Path string `json:"path"`
}

type Sendmail struct {
	Date *time.Time
	Files []string   `json:"files"`
	SentFiles []string `json:"sentfiles"`
	FileIDs []int    `json:"file_ids"`
}

func (s Session) SendFile() (err error) {
	rcpt := string(s)
	s = Session(Config.Get("configuration", "account"))

	var mail Sendmail

	if _, err = DB.Get("sendfile", rcpt, &mail); err != nil {
		return err
	}

	if mail.Date == nil {
		var t time.Time
		t = time.Now()
		mail.Date = &t
		if err := DB.Set("sendfile", rcpt, &mail); err != nil {
			return err
		}
	}

	To := Config.MGet("send_file:opts", "to")
	Cc := Config.MGet("send_file:opts", "cc")
	Bcc := Config.MGet("send_file:opts", "bcc")
	Subj := Config.Get("send_file:opts", "subject")

	var files []string

	if rcpt != string(s) {
		To = append(To, rcpt)
		_, files = scanPath(rcpt)
	} else {
		if To[0] == NONE {
			return fmt.Errorf("No recipients specified, please check config.")
		}
		_, files = scanPath(NONE)
	}

	To = cleanSlice(To)

	mail.Files = append(mail.Files, cleanSlice(files)[0:]...)

	mail_folder, err := s.MyMailFolderID()
	if err != nil {
		return err
	}

	// Uploads all files in folder.
	for i, file := range mail.Files {
		if file == NONE { continue }

		id, err := s.Upload(file, mail_folder)
		if err != nil && err != ErrUploaded {
			mail.Files[i] = NONE
			if err != ErrZeroByte {
				logger.Err(fmt.Sprintf("%s: %s", file, err))
			}
			continue
		}

		if id == -1 {
			mail.Files[i] = NONE
			continue
		}

		r_path := file
		r_path = strings.TrimPrefix(r_path, rcpt)
		dest := "/sent/" + rcpt + SLASH + folderDate(*mail.Date) + SLASH + r_path
		r_path = splitLast(r_path, SLASH)[0]

		meta := KBMeta{
			Path: filepath.ToSlash(SLASH + r_path),
		}

		metadata, err := json.Marshal(meta)
		if err != nil { return err }

		err = s.Call("POST", fmt.Sprintf("/rest/files/%d/comments", id), nil, PostJSON{"contents":"kitebroker_meta:" + string(metadata)})
		if err != nil {
			logger.Err(err)
		}

		mail.FileIDs = append(mail.FileIDs, id)
		mail.SentFiles = append(mail.SentFiles, file)
		mail.Files[i] = NONE

		if strings.ToLower(Config.Get("configuration", "delete_source_files_on_complete")) != "yes" {
			if err := moveFile(file, dest); err != nil {
				return err
			}
		}

		if err := DB.Set("sendfile", rcpt, &mail); err != nil {
			return err
		}
	}

	// Remove duplicates file ids.
	id_map := make(map[int]marker)

	for _, fid := range mail.FileIDs {
		if fid == -1 { continue }
		id_map[fid] = marker{}
	}

	if len(id_map) == 0 {
		logger.Log("No new files to send.")
		return nil
	}

	mail.FileIDs = mail.FileIDs[:0]

	for fid, _ := range id_map {
		mail.FileIDs = append(mail.FileIDs, fid)
	}

	JSON := PostJSON{
		"subject": Subj,
		"to":      To,
		"files":   mail.FileIDs,
	}

	if Cc[0] != NONE {
		JSON["cc"] = Cc
	}

	if Bcc[0] != NONE {
		JSON["bcc"] = Bcc
	}

	var Entity struct {
		ID int `json:"id"`
	}


	err = s.Call("POST", "/rest/mail/actions/sendFile", &Entity, JSON, Query{"returnEntity": true})
	if err != nil {
		return err
	}

	err = Rename("sent/" + rcpt + SLASH + folderDate(*mail.Date), fmt.Sprintf("sent/%s/%d-%s", rcpt, Entity.ID, folderDate(*mail.Date)))
	if err != nil {
		logger.Err(err)
	}

	var total_size int64

	var upload FileRecord

	for _, f := range mail.SentFiles {
		found, err := DB.Get("uploads", f, &upload)
		if found && err == nil {
			total_size = total_size + upload.TotalSize
		}
		DB.Unset("uploads", f)
	}

	logger.Log("Sent Files: %d / Total Size: %s", len(mail.FileIDs), showSize(total_size))

	err = DB.Unset("sendfile", rcpt)

	return

}

func (s Session) RecvFile() (err error) {

	const (
		file_allowed = 1 << iota
		file_quarantined
		file_withdrawn
		file_deleted
		file_downloaded
	)

	type attachment struct {
		Filename string
		Filesize int64
		Fingerprint string
		FileID int
		OriginalFileID int
		Flag int
	}

	type MailEnt struct {
		Date time.Time
		To []string
		Cc []string
		File []attachment
	}

	var mail_ids []int

	q := Query{
		"status": "sent",
		"mode": "compact",
		"deleted": false,
		"isRecipient": true,
	}

	days, err := strconv.Atoi(Config.Get("recv_file:opts", "email_age_days"))
	if err != nil { days = 0 }

	if days > 0 {
		q["date:gte"] = write_kw_time(time.Now().AddDate(0, 0, days * -1))
	}

	sender_filter := Config.MGet("recv_file:opts", "sender")

	if mail_ids, err = s.FindMail(q); err != nil {
		return err
	}

	o := mail_ids[:0]
	for _, v := range mail_ids {
		found, err := DB.Get("inbox", v, nil)
		if err != nil { return err }
		if found { continue }
		o = append(o, v)
	}
	mail_ids = o

	var mail_cnt int

	mail_loop:
	// Get all mail_ids gathered.
	for _, id := range mail_ids {

		var record MailEnt

		var r struct {
			Date string `json:"date"`
			Recipients []struct {
				UserID int `json:"userId"`
				UserType int `json:"type"`
			} `json:"recipients"`

			Variables []struct {
				Variable string `json:"variable"`
				Value string `json:"value"`
			} `json:"variables"`
		}

		err = s.Call("GET", fmt.Sprintf("/rest/mail/%d", id), &r, Query{"mode":"compact", "with": "(variables, recipients)"})
		if err != nil {
			logger.Err(err)
			continue
		}

		record.Date, err = read_kw_time(r.Date)
		if err != nil {
			logger.Err(err)
			continue
		}

		vars := make(map[string]string)

		for _, e := range r.Variables {
			vars[e.Variable] = e.Value
		}

		for i, s := range sender_filter {
			if s == NONE { break }
			if strings.ToLower(s) == strings.ToLower(vars["SENDER_EMAIL"]) {
				break
			} else {
				if i == len(sender_filter) - 1 {
					continue mail_loop
				}
			}
		}

		if _, found := vars["FROM_EMAIL"]; found {
			vars["SENDER_EMAIL"] = vars["FROM_EMAIL"]
		}

		if _, found := vars["FILE_COUNT"]; !found {
			vars["FILE_COUNT"] = "0"
		}

		logger.Log(NONE)
		logger.Log("[%d] Sender:%s, TS:%s, FILES:%s", id, vars["SENDER_EMAIL"], r.Date, vars["FILE_COUNT"])
		MkPath(vars["SENDER_EMAIL"])
		mail_cnt++

		// Record recipient information
		for _, e := range r.Recipients {
			var email string
			user_data, err := s.UserInfo(e.UserID)
			if err != nil {
				logger.Err(err)
				email = fmt.Sprintf("Unknown User ID: %d", strconv.Itoa(e.UserID))
			} else {
				email = user_data.Email
			}
			switch e.UserType {
			case 0:
				record.To = append(record.To, email)
			case 1: 
				record.Cc = append(record.Cc, email)
			}
		}

		var a struct {
			Attachments []struct {
				Withdrawn bool `json:"withdrawn"`
				OriginalFile struct {
					ID int `json:"id"`
				} `json:"originalFile"`
				File struct {
					Name string `json:"name"`
					ID int  `json:"objectId"`
					Size int64 `json:"size"`
					Blocked string `json:"adminQuarantineStatus"`
					Fingerprint string `json:"fingerprint"`
					DLPStatus string `json:"dlpStatus"`
					AVStatus string `json:"avStatus"`
					Deleted bool `json:"deleted"`
				} `json:"frozenFile"`
			} `json:"data"`
		}

		err = s.Call("GET", fmt.Sprintf("/rest/mail/%d/attachments", id), &a, Query{"mode":"full_no_links", "orderBy":"originalFileId:asc","with":"(frozenFile,originalFile)"})
		if err != nil {
			logger.Err(err)
			continue
		}

		for _, e := range a.Attachments {

			file_attach := attachment{
				Filename: e.File.Name,
				Filesize: e.File.Size,
				Fingerprint: e.File.Fingerprint,
			}

			// Files not ready for download.
			if e.File.DLPStatus == "scanning" || e.File.AVStatus == "scanning" {
				logger.Notice("[%d] Not yet ready, skipping for now..", id)
				continue mail_loop
			}

			// Provide log information if file is unable to be downloaded.
			if e.Withdrawn {
				logger.Log("[%d] %s(%s) was withdrawn by sender.", id, e.File.Name, showSize(e.File.Size))
				file_attach.Flag = file_withdrawn
				file_attach.FileID = 0
			} else if e.File.Blocked == "disallowed" {
				logger.Log("[%d] %s(%s) was quarantined by kiteworks.", id, e.File.Name, showSize(e.File.Size))
				file_attach.Flag = file_quarantined
				file_attach.FileID = 0
			} else if e.File.Deleted {
				logger.Log("[%d] %s(%s) was deleted from kiteworks.", id, e.File.Name, showSize(e.File.Size))
				file_attach.Flag = file_deleted
				file_attach.FileID = 0
			} else {
				file_attach.Flag = file_allowed
				file_attach.FileID = e.File.ID
			}
			file_attach.OriginalFileID = e.OriginalFile.ID
			record.File = append(record.File, file_attach)
		}

		folder := fmt.Sprintf("%s/%d-%s", vars["SENDER_EMAIL"], id, folderDate(record.Date))

		for i, f := range record.File {
			fid := f.FileID
			if fid == 0 { continue }
			finfo, err := s.FileInfo(fid)
			if err != nil {
				if err != nil {
					if IsKiteError(err) {
						logger.Log("[%d] %s(%s) cannot be downloaded. %s.", id, record.File[i].Filename, showSize(record.File[i].Filesize), err.Error())
						continue
					} else {
						logger.Err(err)
						continue mail_loop
					}
				} 
			}

			DB.Unset("downloads", fid)

			var Comment struct {
				Data []struct {
					Content string `json:"contents"`
				} `json:"data"`
			}

			err = s.Call("GET", fmt.Sprintf("/rest/files/%d/comments", f.OriginalFileID), &Comment, Query{"contents:contains": "kitebroker_meta:", "orderBy": "created:asc", "limit": 1, "mode": "compact"})
			if err != nil { return err }

			var metadata KBMeta

			if len(Comment.Data) > 0 {
				if len(Comment.Data[0].Content) > 0 {
					j := strings.TrimPrefix(Comment.Data[0].Content, "kitebroker_meta:")
					err = json.Unmarshal([]byte(j), &metadata)
					if err != nil { return err }
					metadata.Path = filepath.Clean(metadata.Path)
				}
			}

			err = s.Download(finfo, folder + metadata.Path)
			if err != nil {
				if err == ErrDownloaded  {
					logger.Log("[%d] %s(%s) was previously downloaded already.", id, record.File[i].Filename, showSize(record.File[i].Filesize))
					f.Flag |= file_downloaded
				} else if err == nil {
					f.Flag |= file_downloaded
				} else {
					logger.Err(err)
					continue mail_loop
				}
			} 
			DB.Unset("downloads", fid)
		}

		// Save full email.
		if strings.ToLower(Config.Get("recv_file:opts", "download_full_email")) == "yes" {
			MkPath(folder)
			f, err := Create(fmt.Sprintf("%s/kw_mail.txt", folder))
			if err != nil {
				logger.Err("[%d] %s", id, err.Error())
			} else {

				_, err = fmt.Fprint(f, "from: ", vars["SENDER_EMAIL"],
									   "\ndate: ", record.Date.String(),
									   "\nto: ", strings.Join(record.To, ";"),
									   "\ncc: ", strings.Join(record.Cc, ";"),
									   "\nsubject: ", vars["SUBJECT"],
									   "\n\n", vars["BODY"], "\n")
				if err != nil {
					logger.Err("[%d] %s", id, err.Error())
				}
			}
		}

		// Save Email body.
		if strings.ToLower(Config.Get("recv_file:opts", "download_email_body")) == "yes" {
			MkPath(folder)
			f, err := Create(fmt.Sprintf("%s/kw_mailbody.txt", folder))
			if err != nil {
				logger.Err("[%d] %s", id, err.Error())
			} else {
				_, err = fmt.Fprint(f, vars["BODY"] + "\n")
				if err != nil {
					logger.Err("[%d] %s", id, err.Error())
				}
			}
		}

		// Generate file manifest.
		if strings.ToLower(Config.Get("recv_file:opts", "download_file_manifest")) == "yes" {
			MkPath(folder)
			f, err := Create(fmt.Sprintf("%s/kw_manifest.csv", folder))
			if err != nil {
				logger.Err("[%d] %s", id, err.Error())
			} else {
				manifest := make([]string, 0)
				manifest = append(manifest, "filename,filesize,fingerprint,status,downloaded")
				for _, info := range  record.File {
					var status, flag string
					switch {
						case info.Flag & file_allowed != 0:
							flag = "allowed"
						case info.Flag & file_quarantined != 0:
							flag = "quarantined"
						case info.Flag & file_withdrawn != 0:
							flag = "withdrawn"
						case info.Flag & file_deleted != 0:
							flag = "deleted"
					}

					if info.Flag & file_downloaded != 0 {
						status = "yes"
					} else {
						status = "no"
					}

					manifest = append(manifest, fmt.Sprintf("%s,%d,%s,%s,%s", info.Filename, info.Filesize, info.Fingerprint, flag, status))
				}
				manifest = append(manifest, NONE)
				_, err = fmt.Fprint(f, strings.Join(manifest, "\n"))
				if err != nil {
					logger.Err("[%d] %s", id, err.Error())
				}
			}
		}

		err = DB.Set("inbox", id, 0)
		if err != nil {
			return err
		}

	}

	if mail_cnt == 0 {
		logger.Log("Nothing found in inbox to download.")
		return nil
	}

	return nil
}
