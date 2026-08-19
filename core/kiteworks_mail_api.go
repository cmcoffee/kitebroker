package core

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"os"
	"strings"
	"time"
)

type kw_rest_mail struct {
	mail_id string
	KWSession
}

// Mail returns a mail accessor for the given mail ID.
func (s KWSession) Mail(mail_id string) kw_rest_mail {
	return kw_rest_mail{mail_id, s}
}

// MailList retrieves a list of emails from the API.
// If there is a problem with the call, it will return an error.
//
// This materializes the entire mailbox at once; prefer MailListEach for
// mailboxes of unknown size.
func (s KWSession) MailList(params ...interface{}) (output []KiteMail, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/mail"),
		Params: SetParams(params),
		Output: &output,
	}, -1, 1000)
	return
}

// MailListEach walks the mailbox a page at a time, calling cb once per page.
// Only one page is ever held in memory, making it safe for mailboxes too large
// to fit in RAM. The slice handed to cb is invalidated once cb returns.
func (s KWSession) MailListEach(page_size int, cb func(mail []KiteMail) error, params ...interface{}) (err error) {
	var page []KiteMail
	return s.DataStream(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/mail"),
		Params: SetParams(params),
	}, page_size, &page, func() error {
		return cb(page)
	})
}

type KiteRecipient struct {
	IsDistributionList bool   `json:"isDistributionList"`
	Email              string `json:"email"`
	Type               int    `json:"type"`
	UserID             string `json:"userId"`
}

type KiteDRMFile struct {
	Name string `json:"name"`
	Size int    `json:"size"`
}

type KiteTAG struct {
	Name string `json:"name"`
	GUID string `json:"guid"`
	Type string `json:"type"`
}

type KiteFingerprints struct {
	Hash      string `json:"hash"`
	Algorithm string `json:"algo"`
}

type KiteAttachment struct {
	AccessType     int         `json:"accessType"`
	DRMFile        KiteDRMFile `json:"drmFile"`
	AttachmentID   string      `json:"attachmentId"`
	EmailPackageID string      `json:"emailPackageId"`
	Withdrawn      bool        `json:"withdrawn"`
	//Permissions          KitePermission     `json:"permissions"`
	Tags                 []KiteTAG          `json:"tags"`
	ObjectID             string             `json:"objectId"`
	Name                 string             `json:"name"`
	Size                 int64              `json:"size"`
	MimeType             string             `json:"mime"`
	Created              string             `json:"created"`
	Deleted              bool               `json:"deleted"`
	Fingerprint          string             `json:"fingerprint"`
	FingerPrintAlgorithm string             `json:"fingerprintAlgo"`
	FingerPrints         []KiteFingerprints `json:"fingerprints"`
}

type KiteMailPackage struct {
	FileCount          string           `json:"fileCount"`
	IncludeFingerPrint bool             `json:"includeFingerprint"`
	Attachments        []KiteAttachment `json:"attachments"`
	ID                 string           `json:"id"`
	SelfCopy           bool             `json:"selfCopy"`
	Deleted            bool             `json:"deleted"`
	ACL                string           `json:"acl"`
	DownloadLink       string           `json:"downloadLink"`
	Expire             int              `json:"expire"`
}

type MailVariable struct {
	Variable string      `json:"variable"`
	Value    interface{} `json:"value"`
}

// KiteMail represents an email in the system.
// It includes properties such as sender, recipients, subject, and body.
// It also has various status flags like read or deleted status.
type KiteMail struct {
	TemplateID           int             `json:"templateId,omitempty"`
	PolicyDirective      int             `json:"policyDirective,omitempty"`
	ID                   string          `json:"id,omitempty"`
	Date                 string          `json:"date,omitempty"`
	Watermark            interface{}     `json:"watermark,omitempty"`
	EmailPackageID       string          `json:"emailPackageId,omitempty"`
	Subject              string          `json:"subject,omitempty"`
	DLPStatus            string          `json:"dlpStatus,omitempty"`
	SenderID             string          `json:"senderId,omitempty"`
	IsUserSent           bool            `json:"isUserSent,omitempty"`
	AttachmentCount      int             `json:"attachmentCount,omitempty"`
	IsRead               bool            `json:"isRead,omitempty"`
	Bucket               string          `json:"bucket,omitempty"`
	AVStatus             string          `json:"avStatus,omitempty"`
	Error                string          `json:"error,omitempty"`
	Status               string          `json:"status,omitempty"`
	Body                 string          `json:"body,omitempty"`
	ExpirationDate       string          `json:"expirationDate,omitempty"`
	ParentEmailID        string          `json:"ParentEmailId,omitempty"`
	IsPreview            bool            `json:"isPreview,omitempty"`
	Deleted              bool            `json:"deleted,omitempty"`
	HTMLBody             string          `json:"htmlBody,omitempty"`
	Headline             string          `json:"headline,omitempty"`
	Recipients           []KiteRecipient `json:"recipients,omitempty"`
	EmailFrom            string          `json:"emailFrom,omitempty"`
	RawBody              string          `json:"rawBody,omitempty"`
	Type                 string          `json:"type,omitempty"`
	SecureBody           bool            `json:"secureBody,omitempty"`
	SharedMailboxID      string          `json:"sharedMailboxId,omitempty"`
	Variables            []MailVariable  `json:"variables,omitempty"`
	mailVarMap           map[string]interface{}
	attachmentLinks      map[string]string
	attachmentFolderLink string
	cachedAttachments    []KiteAttachment
}

func (s kw_rest_mail) CopyAllAttachments(destination_folder KiteObject) (err error) {
	found_files := make(map[string]interface{})
	attachments, err := s.Attachments()
	if err != nil {
		return err
	}
	var attachment_ids []Query
	for _, attachment := range attachments {
		if attachment.Deleted {
			continue
		}
		_, err := s.Folder(destination_folder.ID).Find(attachment.Name)
		if err != nil {
			err = nil
			if attachment.AccessType == 0 || attachment.AccessType == 2 {
				attachment_ids = append(attachment_ids, Query{"id:in": fmt.Sprintf("%s", attachment.AttachmentID)})
			}
		} else {
			found_files[attachment.Name] = struct{}{}
		}
	}

	var retry_copy bool

	if len(attachment_ids) > 0 {
		for _, v := range attachment_ids {
			if err := s.Call(APIRequest{
				Method: "POST",
				Path:   SetPath("/rest/mail/%s/actions/copy", s.mail_id),
				Params: SetParams(v, PostJSON{"destinationFolderId": destination_folder.ID}),
			}); err != nil {
				retry_copy = true
			}
		}
	}

	if retry_copy {
		for _, attachment := range attachments {
			if _, ok := found_files[attachment.Name]; ok {
				continue
			}
			if attachment.Deleted {
				continue
			}
			if attachment.AccessType != 0 && attachment.AccessType != 2 {
				continue
			}
			file, err := s.DownloadAttachment(attachment.AttachmentID)
			if err != nil {
				Err("Error while downloading attachment: %s %v", attachment.Name, err)
				continue
			}
			_, err = s.KWSession.Upload(attachment.Name, attachment.Size, time.Now(), false, false, true, destination_folder, file)
			if err != nil {
				Err("Error while uploading attachment to folder %v: %v", destination_folder.ID, err)
			}
			file.Close()
		}
	}
	return
}

// Attachments returns all the attachments of an email.
// If there is a problem with the API call, it will return an error.
func (s kw_rest_mail) Attachments() (attachments []KiteAttachment, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/mail/%s/attachments", s.mail_id),
		Output: &attachments,
	}, -1, 1000)
	return
}

// attachmentsFor returns the attachments for mail, preferring the copy already
// cached on the KiteMail so that callers which have fetched the list can avoid
// a second round trip to the API.
func (s kw_rest_mail) attachmentsFor(mail *KiteMail) (attachments []KiteAttachment, err error) {
	if mail != nil && mail.cachedAttachments != nil {
		return mail.cachedAttachments, nil
	}
	return s.Attachments()
}

// GetVariable returns the value of a variable from the KiteMail object. If the mailVarMap is nil,
// it creates a new map and populates it with variables from the Variables slice.
// Then, it returns the value corresponding to the provided name.
func (s *KiteMail) GetVariable(name string) interface{} {
	if s.mailVarMap == nil {
		s.mailVarMap = make(map[string]interface{})
		for _, v := range s.Variables {
			s.mailVarMap[v.Variable] = v.Value
		}
	}
	return s.mailVarMap[name]
}

// Get retrieves an email from the API. It returns the full email data along with any attachments. If there is a problem with the call, it will return an error.
func (s kw_rest_mail) Get(params ...interface{}) (output KiteMail, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/mail/%s", s.mail_id),
		Params: SetParams(Query{"mode": "full", "with": "(attachments,rawBody,body,emailFrom)"}),
		Output: &output,
	})
	return
}

// formatFileSize returns a human-readable file size string.
func formatFileSize(size int64) string {
	switch {
	case size >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(size)/float64(1<<30))
	case size >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(size)/float64(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(size)/float64(1<<10))
	default:
		return fmt.Sprintf("%d bytes", size)
	}
}

func (s kw_rest_mail) Manifest(mail KiteMail, folder_link ...string) (manifest string, err error) {
	attachments, err := s.attachmentsFor(&mail)
	if err != nil {
		return "", err
	}
	if len(attachments) == 0 {
		return "", nil
	}

	// Count visible attachments and total size.
	var file_count int
	var total_size int64
	for _, a := range attachments {
		if len(a.Name) > 0 {
			file_count++
			total_size += a.Size
		}
	}

	var manifest_list []string

	title := "Kiteworks Mail Attachments"
	if len(folder_link) > 0 && folder_link[0] != "" {
		title = fmt.Sprintf("<a href=\"%s\" target=\"_blank\" style=\"color:#ffffff;text-decoration:none;\">Kiteworks Mail Attachments</a>", folder_link[0])
	}
	header := fmt.Sprintf(`
<hr style="border:none;border-top:1px solid #e0e0e0;margin:20px 0 10px 0;" />
<table align="center" border="0" width="100%%%%" cellpadding="0" cellspacing="0" style="font-family:Arial,Helvetica,sans-serif;">
<tr><td>
<table align="center" border="0" width="100%%%%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;border:1px solid #d0d0d0;border-radius:6px;overflow:hidden;">
	<tr style="background-color:#2b579a;">
		<td colspan="4" style="padding:12px 14px;font-size:16px;font-weight:bold;color:#ffffff;">%s</td>
	</tr>
	<tr style="background-color:#f7f9fc;border-bottom:2px solid #d0d0d0;">
		<th style="text-align:left;padding:10px 14px;font-size:13px;color:#555555;font-weight:600;border-bottom:2px solid #d0d0d0;">Filename</th>
		<th style="text-align:right;padding:10px 14px;font-size:13px;color:#555555;font-weight:600;border-bottom:2px solid #d0d0d0;">Size</th>
		<th style="text-align:center;padding:10px 14px;font-size:13px;color:#555555;font-weight:600;border-bottom:2px solid #d0d0d0;">Type</th>
		<th style="text-align:center;padding:10px 14px;font-size:13px;color:#555555;font-weight:600;border-bottom:2px solid #d0d0d0;">Status</th>
	</tr>`, title)
	manifest_list = append(manifest_list, header)

	row_num := 0
	for _, attachment := range attachments {
		if len(attachment.Name) == 0 {
			continue
		}

		var status_label, status_color string
		switch attachment.AccessType {
		case 0:
			status_label = "Available"
			status_color = "#2e7d32"
		case 1:
			status_label = "View-Only"
			status_color = "#ef6c00"
		case 2:
			status_label = "DRM Protected"
			status_color = "#1565c0"
		case 3:
			status_label = "Blocked"
			status_color = "#c62828"
		}
		if attachment.Withdrawn {
			status_label = "Withdrawn"
			status_color = "#c62828"
		}
		if attachment.Deleted && !attachment.Withdrawn {
			status_label = "Expired / Deleted"
			status_color = "#757575"
		}

		// Render filename as a link if a permalink is available.
		file_label := attachment.Name
		if link, ok := mail.attachmentLinks[attachment.Name]; ok && link != "" {
			file_label = fmt.Sprintf("<a href=\"%s\" target=\"_blank\" style=\"color:#1a73e8;text-decoration:none;\">%s</a>", link, attachment.Name)
		}

		// Alternate row background for readability.
		row_bg := "#ffffff"
		if row_num%2 == 1 {
			row_bg = "#f9fafb"
		}

		status_badge := fmt.Sprintf("<span style=\"display:inline-block;padding:3px 10px;border-radius:12px;font-size:12px;font-weight:600;color:#ffffff;background-color:%s;\">%s</span>", status_color, status_label)

		manifest_list = append(manifest_list, fmt.Sprintf("\t<tr style=\"background-color:%s;border-bottom:1px solid #eeeeee;\">", row_bg))
		manifest_list = append(manifest_list, fmt.Sprintf("\t\t<td style=\"text-align:left;padding:10px 14px;font-size:14px;\">%s</td>", file_label))
		manifest_list = append(manifest_list, fmt.Sprintf("\t\t<td style=\"text-align:right;padding:10px 14px;font-size:13px;color:#444444;\">%s</td>", formatFileSize(attachment.Size)))
		manifest_list = append(manifest_list, fmt.Sprintf("\t\t<td style=\"text-align:center;padding:10px 14px;font-size:13px;color:#666666;\">%s</td>", attachment.MimeType))
		manifest_list = append(manifest_list, fmt.Sprintf("\t\t<td style=\"text-align:center;padding:10px 14px;\">%s</td>", status_badge))
		manifest_list = append(manifest_list, "\t</tr>")
		row_num++
	}

	// Summary footer row.
	manifest_list = append(manifest_list, fmt.Sprintf("\t<tr style=\"background-color:#f7f9fc;border-top:2px solid #d0d0d0;\">"))
	manifest_list = append(manifest_list, fmt.Sprintf("\t\t<td style=\"padding:10px 14px;font-size:13px;color:#555555;font-weight:600;\">%d file(s)</td>", file_count))
	manifest_list = append(manifest_list, fmt.Sprintf("\t\t<td style=\"text-align:right;padding:10px 14px;font-size:13px;color:#555555;font-weight:600;\">%s</td>", formatFileSize(total_size)))
	manifest_list = append(manifest_list, "\t\t<td colspan=\"2\"></td>")
	manifest_list = append(manifest_list, "\t</tr>")
	manifest_list = append(manifest_list, "</table></td></tr></table>")
	return strings.Join(manifest_list, "\n"), nil
}

// mailHeaders builds the RFC-822 header lines for mail, excluding the
// Content-Type and Content-Transfer-Encoding headers which depend on whether
// the message is written as a single part or as a multipart container.
func (s kw_rest_mail) mailHeaders(mail KiteMail) (headers []string, err error) {
	mail_append := func(input string) {
		headers = append(headers, input)
	}
	if mail.EmailFrom != "" {
		mail_append(fmt.Sprintf("From: <%s>", mail.EmailFrom))
	} else {
		mail_append(fmt.Sprintf("From: <undisclosed-sender@%s>", s.Server))
	}
	var recipients, recipients_cc, recipients_bcc []string
	for _, recipient := range mail.Recipients {
		if recipient.Type == 0 {
			recipients = append(recipients, fmt.Sprintf("<%s>", recipient.Email))
		}
		if recipient.Type == 1 {
			recipients_cc = append(recipients_cc, fmt.Sprintf("<%s>", recipient.Email))
		}
		if recipient.Type == 2 {
			recipients_bcc = append(recipients_bcc, fmt.Sprintf("<%s>", recipient.Email))
		}
	}
	mail_append(fmt.Sprintf("To: %s", strings.Join(recipients, ", ")))
	if len(recipients_cc) > 0 {
		mail_append(fmt.Sprintf("CC: %s", strings.Join(recipients_cc, ", ")))
	}
	if len(recipients_bcc) > 0 {
		mail_append(fmt.Sprintf("BCC: %s", strings.Join(recipients_bcc, ", ")))
	}
	mail_append(fmt.Sprintf("Subject: %s", mime.QEncoding.Encode("utf-8", mail.Subject)))
	ts, err := ReadKWTime(mail.Date)
	if err != nil {
		return nil, err
	}
	mail_append(fmt.Sprintf("Date: %s", ts.UTC().Format(time.RFC1123Z)))
	mail_append(fmt.Sprintf("Message-ID: <%s@%s>", mail.ID, s.Server))
	mail_append("MIME-Version: 1.0")
	return headers, nil
}

// mailBodyHTML builds the HTML body for mail, including the attachment manifest
// and any trailing footers.
func (s kw_rest_mail) mailBodyHTML(mail KiteMail, trailing_footer ...string) (body string, err error) {
	var body_parts []string
	body_parts = append(body_parts, "<!DOCTYPE html>\r\n<html>\r\n<body>")
	mail_body := mail.GetVariable("BODY")
	if b, ok := mail_body.(string); ok && b != "" {
		body_parts = append(body_parts, b)
	} else {
		body_parts = append(body_parts, "<p>")
		for _, v := range strings.Split(mail.RawBody, "\n") {
			body_parts = append(body_parts, fmt.Sprintf("%s<br>", v))
		}
		body_parts = append(body_parts, "</p>")
	}
	if mail.AttachmentCount > 0 {
		attachment_list, err := s.Manifest(mail, mail.attachmentFolderLink)
		if err != nil {
			return "", err
		}
		body_parts = append(body_parts, attachment_list)
	}
	for _, footer := range trailing_footer {
		body_parts = append(body_parts, footer)
	}
	body_parts = append(body_parts, "</body>\r\n</html>")
	return strings.Join(body_parts, "\r\n"), nil
}

// Read method reads the email and returns it in a human-readable format.
func (s kw_rest_mail) Read(mail KiteMail, trailing_footer ...string) (output string, err error) {
	mail_output, err := s.mailHeaders(mail)
	if err != nil {
		return "", err
	}
	mail_output = append(mail_output,
		"Content-Type: text/html; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable")

	body, err := s.mailBodyHTML(mail, trailing_footer...)
	if err != nil {
		return "", err
	}

	// Encode the HTML body with quoted-printable.
	var qpBuf bytes.Buffer
	qpw := quotedprintable.NewWriter(&qpBuf)
	qpw.Write([]byte(body))
	qpw.Close()

	// Join headers with CRLF, add blank line separator, then encoded body.
	return strings.Join(mail_output, "\r\n") + "\r\n\r\n" + qpBuf.String(), nil
}

// WriteEML streams mail to w as a complete RFC-822 message. When embed is
// non-empty the message is written as a multipart/mixed container and each
// listed attachment is downloaded and base64 encoded directly into w, so no
// attachment is ever held in memory in full. When embed is empty the output is
// a single-part message identical to Read.
//
// Attachments that fail to download are reported and skipped rather than
// aborting the message, so a partially recoverable mail still produces a file.
func (s kw_rest_mail) WriteEML(w io.Writer, mail KiteMail, embed []KiteAttachment, trailing_footer ...string) (err error) {
	headers, err := s.mailHeaders(mail)
	if err != nil {
		return err
	}

	body, err := s.mailBodyHTML(mail, trailing_footer...)
	if err != nil {
		return err
	}

	// No attachments to embed: single-part message.
	if len(embed) == 0 {
		headers = append(headers,
			"Content-Type: text/html; charset=UTF-8",
			"Content-Transfer-Encoding: quoted-printable")
		if _, err = io.WriteString(w, strings.Join(headers, "\r\n")+"\r\n\r\n"); err != nil {
			return err
		}
		qpw := quotedprintable.NewWriter(w)
		if _, err = qpw.Write([]byte(body)); err != nil {
			return err
		}
		return qpw.Close()
	}

	mp := multipart.NewWriter(w)

	headers = append(headers, fmt.Sprintf("Content-Type: multipart/mixed; boundary=%q", mp.Boundary()))
	if _, err = io.WriteString(w, strings.Join(headers, "\r\n")+"\r\n\r\n"); err != nil {
		return err
	}

	// First part is the HTML body.
	body_part, err := mp.CreatePart(map[string][]string{
		"Content-Type":              {"text/html; charset=UTF-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}
	qpw := quotedprintable.NewWriter(body_part)
	if _, err = qpw.Write([]byte(body)); err != nil {
		return err
	}
	if err = qpw.Close(); err != nil {
		return err
	}

	// Remaining parts are the attachments, streamed one at a time.
	for _, attachment := range embed {
		mime_type := attachment.MimeType
		if IsBlank(mime_type) {
			mime_type = "application/octet-stream"
		}
		part, err := mp.CreatePart(map[string][]string{
			"Content-Type":              {fmt.Sprintf("%s; name=%q", mime_type, attachment.Name)},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {fmt.Sprintf("attachment; filename=%q", attachment.Name)},
		})
		if err != nil {
			return err
		}

		src, err := s.DownloadAttachment(attachment.AttachmentID)
		if err != nil {
			Err("Error while downloading attachment %s: %v", attachment.Name, err)
			continue
		}

		// Wrap in a line-splitting writer; base64 in MIME must wrap at 76 chars.
		b64 := base64.NewEncoder(base64.StdEncoding, &mimeLineWriter{w: part})
		_, err = io.Copy(b64, src)
		src.Close()
		if err != nil {
			b64.Close()
			return err
		}
		if err = b64.Close(); err != nil {
			return err
		}
	}

	return mp.Close()
}

// mimeLineWriter wraps output at 76 characters per line with CRLF endings, as
// required for base64 encoded MIME parts.
type mimeLineWriter struct {
	w     io.Writer
	count int
}

func (m *mimeLineWriter) Write(p []byte) (n int, err error) {
	for len(p) > 0 {
		remaining := 76 - m.count
		chunk := p
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
		}
		written, err := m.w.Write(chunk)
		n += written
		m.count += written
		if err != nil {
			return n, err
		}
		p = p[written:]
		if m.count >= 76 {
			if _, err = m.w.Write([]byte("\r\n")); err != nil {
				return n, err
			}
			m.count = 0
		}
	}
	return n, nil
}

type stringSeeker struct {
	io.ReadSeeker
}

func (n stringSeeker) Close() (err error) {
	return nil
}

// archivePaths derives the archive layout for mail: the sender label used in
// the message filename, and the relative folder path (bucket plus an optional
// per-correspondent subfolder) the message belongs in. Shared between the
// Kiteworks-folder and local filesystem archivers so both produce the same
// hierarchy.
func (T kw_rest_mail) archivePaths(mail KiteMail, root_path string) (sender, folder_path string) {
	bucket := strings.Title(strings.ToLower(mail.Bucket))

	if mail.EmailFrom == "" {
		sender = "Kiteworks System"
		bucket = "Inbox"
	} else {
		sender = mail.EmailFrom
	}

	if root_path == "" {
		root_path = fmt.Sprintf("/[%s]", T.Username)
	}

	// Build the end_path based on the sender and bucket.
	var end_path string
	if mail.EmailFrom != "" && mail.EmailFrom != T.Username {
		end_path = fmt.Sprintf("[%s]", mail.EmailFrom)
	}
	if bucket == "Inbox" && mail.SharedMailboxID != "" {
		for _, r := range mail.Recipients {
			if r.UserID == mail.SharedMailboxID {
				end_path = fmt.Sprintf("[%s]", r.Email)
				break
			}
		}
	}

	if end_path == "" {
		folder_path = fmt.Sprintf("%s/%s", root_path, bucket)
	} else {
		folder_path = fmt.Sprintf("%s/%s/%s", root_path, bucket, end_path)
	}
	return sender, folder_path
}

// cleanArchiveName strips characters that are illegal in Kiteworks folder names
// and on local filesystems, and replaces spaces with underscores.
func cleanArchiveName(input string) (output string) {
	var output_string []string
	for _, v := range input {
		if v == ' ' {
			output_string = append(output_string, "_")
			continue
		}
		if v == '\\' || v == '/' || v == '*' || v == '"' || v == '<' || v == '>' || v == '|' || v == '?' || v == ':' {
			continue
		}
		output_string = append(output_string, string(v))
	}
	return strings.Join(output_string, "")
}

func (T kw_rest_mail) Archive(root_path string) (err error) {
	mail, err := T.Get()
	if err != nil {
		return err
	}

	sender, folder_path := T.archivePaths(mail, root_path)

	folder, err := T.Folder("0").ResolvePath(folder_path)
	if err != nil {
		return err
	}

	CleanString := cleanArchiveName

	archive_email := func(mail KiteMail, footer string, folder KiteObject) (err error) {
		message, err := T.Read(mail, footer)
		if err != nil {
			return err
		}
		f := strings.NewReader(message)
		file := &stringSeeker{f}

		_, err = T.KWSession.Upload(CleanString(fmt.Sprintf("%s-%s-%s.eml", sender, mail.Subject, mail.Date)), int64(len(message)), time.Now(), false, false, true, folder, file)
		return
	}

	if mail.AttachmentCount > 0 {
		var downloadable int
		attachments, err := T.Attachments()
		if err != nil {
			return err
		}
		for _, a := range attachments {
			if !a.Deleted && (a.AccessType == 0 || a.AccessType == 3) {
				downloadable++
			}
		}
		if downloadable > 0 {
			attachments_folder, err := T.Folder("0").ResolvePath(fmt.Sprintf("%s/Attachments/%s", folder_path, mail.ID))
			if err != nil {
				return err
			}
			err = T.CopyAllAttachments(attachments_folder)
			if err != nil {
				return err
			}

			// Build per-file permalink map from the attachments folder.
			copied_files, err := T.Folder(attachments_folder.ID).Contents()
			if err == nil {
				mail.attachmentLinks = make(map[string]string)
				for _, f := range copied_files {
					if f.Permalink != "" {
						mail.attachmentLinks[f.Name] = f.Permalink
					}
				}
			}

			mail.attachmentFolderLink = attachments_folder.Permalink
		}
	}
	err = archive_email(mail, "", folder)
	if err != nil {
		return err
	}

	return nil
}

// LocalArchiveOptions controls how ArchiveLocal writes mail to disk.
type LocalArchiveOptions struct {
	// RootPath is the local directory the archive is rooted at. Required.
	RootPath string
	// Embed writes attachments into the .eml as multipart/mixed parts, producing
	// a single self-contained file per message. When false, attachments are
	// written to an Attachments/<mail id>/ subfolder alongside the .eml and the
	// message body links to them with relative paths.
	Embed bool
	// SkipAttachments skips attachment retrieval entirely; the .eml still carries
	// the manifest table describing what was attached.
	SkipAttachments bool
	// Transferred, when set, is called with the byte count of each chunk written.
	Transferred func(int)
}

// countingWriter reports bytes written to a callback, for transfer tallies.
type countingWriter struct {
	w  io.Writer
	cb func(int)
}

func (c countingWriter) Write(p []byte) (n int, err error) {
	n, err = c.w.Write(p)
	if n > 0 && c.cb != nil {
		c.cb(n)
	}
	return n, err
}

// ArchiveLocal writes a mail message to the local filesystem as an .eml file,
// using the same bucket/correspondent hierarchy as Archive. It is the local
// counterpart to Archive: nothing is uploaded and no destination Kiteworks
// folders are created or resolved.
//
// Attachments and the message body are streamed to disk, so memory use is
// bounded regardless of message or attachment size.
func (T kw_rest_mail) ArchiveLocal(opts LocalArchiveOptions) (err error) {
	mail, err := T.Get()
	if err != nil {
		return err
	}

	if IsBlank(opts.RootPath) {
		return fmt.Errorf("ArchiveLocal: a local root path is required.")
	}

	sender, rel_path := T.archivePaths(mail, fmt.Sprintf("[%s]", T.Username))
	local_path := CombinePath(opts.RootPath, LocalPath(rel_path))

	if err = MkDir(local_path); err != nil {
		return err
	}

	// Fetch the attachment list once and cache it on the mail so the manifest
	// builder does not repeat the call.
	var embed []KiteAttachment
	if mail.AttachmentCount > 0 && !opts.SkipAttachments {
		attachments, err := T.Attachments()
		if err != nil {
			return err
		}
		mail.cachedAttachments = attachments

		// AccessType 0 and 3 are the retrievable ones; view-only and DRM
		// protected attachments cannot be pulled down as files.
		for _, a := range attachments {
			if a.Deleted || IsBlank(a.Name) {
				continue
			}
			if a.AccessType == 0 || a.AccessType == 3 {
				embed = append(embed, a)
			}
		}

		if len(embed) > 0 && !opts.Embed {
			// Sidecar mode: write attachments to their own folder and point the
			// manifest at them with paths relative to the .eml.
			attach_rel := CombinePath("Attachments", mail.ID)
			attach_path := CombinePath(local_path, attach_rel)
			if err = MkDir(attach_path); err != nil {
				return err
			}

			mail.attachmentLinks = make(map[string]string)
			for _, a := range embed {
				name := cleanArchiveName(a.Name)
				if err := T.downloadAttachmentTo(a, CombinePath(attach_path, name), opts.Transferred); err != nil {
					Err("[%s]: Error saving attachment %s: %v", T.Username, a.Name, err)
					continue
				}
				mail.attachmentLinks[a.Name] = fmt.Sprintf("Attachments/%s/%s", mail.ID, name)
			}
			mail.attachmentFolderLink = fmt.Sprintf("Attachments/%s", mail.ID)

			// Already on disk; do not embed them a second time.
			embed = nil
		}
	}

	file_name := cleanArchiveName(fmt.Sprintf("%s-%s-%s.eml", sender, mail.Subject, mail.Date))
	dest := CombinePath(local_path, file_name)

	// Write to a temporary file and rename on success so an interrupted run
	// never leaves a truncated .eml that a later run would treat as complete.
	tmp := fmt.Sprintf("%s.incomplete", dest)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	var w io.Writer = f
	if opts.Transferred != nil {
		w = countingWriter{f, opts.Transferred}
	}

	if err = T.WriteEML(w, mail, embed); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err = f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	return Rename(tmp, dest)
}

// downloadAttachmentTo streams a single attachment to a local file path.
func (T kw_rest_mail) downloadAttachmentTo(attachment KiteAttachment, dest string, counter func(int)) (err error) {
	src, err := T.DownloadAttachment(attachment.AttachmentID)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp := fmt.Sprintf("%s.incomplete", dest)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	var w io.Writer = f
	if counter != nil {
		w = countingWriter{f, counter}
	}

	if _, err = io.Copy(w, src); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err = f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	return Rename(tmp, dest)
}
