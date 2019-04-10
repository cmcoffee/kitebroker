package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/cmcoffee/go-nfo"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// API Session
type Session string

var call_done struct{}
var api_call_bank chan struct{}
var transfer_call_bank chan struct{}

type PostJSON map[string]interface{}
type PostFORM map[string]interface{}
type Query map[string]interface{}

const (
	ErrBadAuth = "ERR_AUTH_UNAUTHORIZED"
)

var ErrFileChanged = fmt.Errorf("File has been changed.")
var ErrUploaded = fmt.Errorf("File is already uploaded.")
var ErrZeroByte = fmt.Errorf("File has no content.")
var ErrDownloaded = fmt.Errorf("File is already downloaded.")

// Converts kiteworks API errors to standard golang error message.
func respError(resp *http.Response) (err error) {
	if resp == nil {
		return
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	type KiteErr struct {
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
		Errors    []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}

	var body io.Reader

	if snoop {
		body = io.TeeReader(resp.Body, os.Stderr)
	} else {
		body = resp.Body
	}

	if snoop {
		nfo.Stdout("<-- RESPONSE STATUS: %s", resp.Status)
	}

	output, err := ioutil.ReadAll(body)
	if err != nil {
		return err
	}

	if snoop {
		nfo.Stdout("\n")
	}

	var kite_err *KiteErr

	json.Unmarshal(output, &kite_err)

	if kite_err != nil {
		e := NewKError()
		for _, v := range kite_err.Errors {
			e.AddError(v.Code, v.Message)
		}
		if kite_err.ErrorDesc != NONE {
			e.AddError(kite_err.Error, kite_err.ErrorDesc)
		}
		return e
	}

	if resp.Status == "401 Unauthorized" {
		e := NewKError()
		e.AddError("ERR_AUTH_UNAUTHORIZED", "Unathorized Access Token")
		return e
	}

	return fmt.Errorf("%s says \"%s.\"", Config.Get("configuration", "server"), resp.Status)
}

type KiteRequest struct {
	APIVer int
	Action string
	Path   string
	Params []interface{}
	Output interface{}
}

func (s Session) Call(kw_req KiteRequest) (err error) {

	req, err := s.NewRequest(kw_req.Action, kw_req.Path, kw_req.APIVer)
	if err != nil {
		return err
	}

	if snoop {
		nfo.Stdout("\n--> ACTION: \"%s\" PATH: \"%s\"", strings.ToUpper(kw_req.Action), kw_req.Path)
	}

	var body []byte

	for _, in := range kw_req.Params {
		switch i := in.(type) {
		case PostFORM:
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			p := make(url.Values)
			for k, v := range i {
				p.Add(k, fmt.Sprintf("%v", v))
				if snoop {
					nfo.Stdout("\\-> POST PARAM: \"%s\" VALUE: \"%s\"", k, p[k])
				}
			}
			body = []byte(p.Encode())
		case PostJSON:
			req.Header.Set("Content-Type", "application/json")
			json, err := json.Marshal(i)
			if err != nil {
				return err
			}
			if snoop {
				nfo.Stdout("\\-> POST JSON: %s", string(json))
			}
			body = json
		case Query:
			q := req.URL.Query()
			for k, v := range i {
				q.Set(k, fmt.Sprintf("%v", v))
				if snoop {
					nfo.Stdout("\\-> QUERY: %s=%s", k, q[k])
				}
			}
			req.URL.RawQuery = q.Encode()
		default:
			return fmt.Errorf("kitebroker error, unknown request exception.")
		}
	}

	var resp *http.Response

	// Retry call on failures.
	for i := 0; i < MAX_RETRY; i++ {
		req.Body = ioutil.NopCloser(bytes.NewReader(body))
		client := s.NewClient()
		resp, err = client.Do(req)
		if err != nil && KiteError(err, ERR_INTERNAL_SERVER_ERROR|TOKEN_ERR) {
			if s.ChkToken(err) {
				if err := s.SetToken(req); err != nil {
					return err
				}
			}
			time.Sleep(time.Second)
			continue
		} else if err != nil {
			return err
		}

		err = s.DecodeJSON(resp, kw_req.Output)
		if err != nil && KiteError(err, ERR_INTERNAL_SERVER_ERROR|TOKEN_ERR) {
			nfo.Debug("(JSON) %s -> %s: %s (%d/%d)", s, kw_req.Path, err.Error(), i+1, MAX_RETRY)
			if s.ChkToken(err) {
				if err := s.SetToken(req); err != nil {
					return err
				}
			}
			time.Sleep(time.Second)
			continue
		} else {
			break
		}
	}

	return err
}

func (s Session) SetToken(req *http.Request) (err error) {
	access_token, err := s.GetToken()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access_token)
	return
}

// Create new formed http request for appliance.
func (s Session) NewRequest(action, path string, api_ver int) (req *http.Request, err error) {

	if api_ver == 0 {
		api_ver = 11
	}

	req, err = http.NewRequest(action, fmt.Sprintf("https://%s%s", server, path), nil)
	if err != nil {
		return nil, err
	}

	req.URL.Host = server
	req.URL.Scheme = "https"
	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", api_ver))
	req.Header.Set("User-Agent", fmt.Sprintf("%s-%s", NAME, VERSION))
	req.Header.Set("Referer", "https://"+server+"/")

	err = s.SetToken(req)
	if err != nil {
		return nil, err
	}

	return req, nil
}

type KCall struct {
	*http.Client
}

// Perform request.
func (c *KCall) Do(req *http.Request) (*http.Response, error) {
	<-api_call_bank
	defer func() { api_call_bank <- call_done }()
	resp, err := c.Client.Do(req)

	kerr := respError(resp)
	if kerr != nil {
		return resp, kerr
	}

	return resp, err
}

// Create new client session to appliance.
func (s Session) NewClient() *KCall {

	var transport http.Transport

	// Allows invalid certs if set to "no" in config.
	if strings.ToLower(Config.Get("configuration", "ssl_verify")) == "no" {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// Setup proxy setting.
	if proxy_host := Config.Get("configuration", "proxy_uri"); proxy_host != NONE {
		proxyURL, err := url.Parse(proxy_host)
		errChk(err)
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	return &KCall{&http.Client{Transport: &transport, Timeout: timeout}}
}

// Decodes JSON response body to provided interface.
func (s Session) DecodeJSON(resp *http.Response, output interface{}) (err error) {

	defer resp.Body.Close()

	var body io.Reader

	if snoop {
		if _, ok := output.(**KiteAuth); !ok {
			body = io.TeeReader(resp.Body, os.Stderr)
			defer func() {
				nfo.Stdout("\n")
			}()
		} else {
			body = resp.Body
		}
	} else {
		body = resp.Body
	}

	if output == nil && snoop {
		nfo.Stdout("<-- RESPONSE STATUS: %s", resp.Status)
		ioutil.ReadAll(body)
		return nil
	} else if output == nil {
		return nil
	}

	if snoop {
		nfo.Stdout("<-- RESPONSE STATUS: %s", resp.Status)
	}

	dec := json.NewDecoder(body)
	err = dec.Decode(output)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		if snoop {
			ioutil.ReadAll(body)
			err = fmt.Errorf("I cannot understand what %s is saying.", Config.Get("configuration", "server"))
		} else {
			err = fmt.Errorf("I cannot understand what %s is saying. (Try running %s --rest_snoop)", Config.Get("configuration", "server"), os.Args[0])
		}
	}
	return
}

// Kiteworks User Data
type KiteUser struct {
	ID         int         `json:"id"`
	Active     bool        `json:"active"`
	Suspended  bool        `json:"suspended"`
	BaseDirID  int         `json:"basedirId"`
	Deleted    bool        `json:"deleted"`
	Email      string      `json:"email"`
	MyDirID    int         `json:"mydirId"`
	Name       string      `json:"name"`
	SyncDirID  int         `json:"syncdirId"`
	UserTypeID int         `json:"userTypeId"`
	Verified   bool        `json:"verified"`
	Internal   bool        `json:"internal"`
	Links      []KiteLinks `json:"links"`
}

// Kiteworks Links Data
type KiteLinks struct {
	Relationship string `json:"rel"`
	Entity       string `json:"entity"`
	ID           int    `json:"id"`
	URL          string `json:"href"`
}

// Kiteworks Folder/File Data
type KiteData struct {
	ID           int         `json:"id"`
	Created      string      `json:"created"`
	Deleted      bool        `json:"deleted"`
	Expire       interface{} `json:"expire"`
	Modified     string      `json:"modified"`
	Name         string      `json:"name"`
	Description  string      `json:"description"`
	ParentID     int         `json:"parentId"`
	UserID       int         `json:"userId"`
	Permalink    string      `json:"permalink"`
	Locked       int         `json:"locked"`
	Fingerprint  string      `json:"fingerprint"`
	Status       string      `json:"status"`
	Size         int64       `json:"size"`
	Mime         string      `json:"mime"`
	Quarantined  bool        `json:"quarantined"`
	DLPLocked    bool        `json:"dlpLocked"`
	Filelifetime interface{} `json:"fileLifetime"`
	Type         string      `json:"type"`
	Links        []KiteLinks `json:"links"`
	MailID       int         `json:"mail_id"`
}

// Array response, such as list of folders, files or users.
type KiteArray struct {
	Data     []KiteData  `json:"data"`
	Links    []KiteLinks `json:"links"`
	Metadata struct {
		Total  uint `json:"total"`
		Offset int  `json:"offset"`
	} `json:"metadata"`
}

type KiteUserArray struct {
	Users    []KiteUser `json:"data"`
	Metadata struct {
		Total  uint `json:"total"`
		Offset int  `json:"offset"`
	} `json:"metadata"`
}

var SetPath = fmt.Sprintf

func SetParams(vars ...interface{}) (output []interface{}) {
	for _, v := range vars {
		output = append(output, v)
	}
	return
}

// Get My User information.
func (s Session) MyUser() (output KiteUser, err error) {
	req := KiteRequest{
		Action: "GET",
		Path:   "/rest/users/me",
		Output: &output,
	}
	return output, s.Call(req)
}

// Returns Folder ID of the Account's My Folder.
func (s Session) MyBaseDirID() (file_id int, err error) {
	out, err := s.MyUser()
	if err != nil {
		return -1, err
	}
	return out.BaseDirID, nil
}

// Returns Folder ID for sending files.
func (s Session) MyMailFolderID() (fild_id int, err error) {
	out, err := s.MyUser()
	if err != nil {
		return -1, err
	}
	return out.MyDirID, nil
}

// Get url to self.
func SelfLink(input *[]KiteLinks) string {
	for _, link := range *input {
		if strings.ToLower(link.Relationship) == "self" {
			return link.URL
		}
	}
	return NONE
}

func (s Session) GetRoles() (roles KiteArray, err error) {
	err = s.Call(KiteRequest{
		Action: "GET",
		Path:   "/rest/roles",
		Output: &roles,
	})
	if err != nil {
		return
	}

	return
}

// Pulls up all top level folders.
func (s Session) GetFolders() (output KiteArray, err error) {
	req := KiteRequest{
		Action: "GET",
		Path:   "/rest/folders/top",
		Params: SetParams(Query{"deleted": false}),
		Output: &output,
	}
	return output, s.Call(req)
}

// Find a user_id
func (s Session) FindUser(user_email string) (id int, err error) {
	id = -1

	var info struct {
		Users []KiteUser `json:"data"`
	}

	req := KiteRequest{
		Action: "GET",
		Path:   "/rest/users",
		Params: SetParams(Query{"email": user_email, "mode": "compact"}),
		Output: &info,
	}

	err = s.Call(req)
	if err != nil {
		return -1, err
	}

	if len(info.Users) == 0 {
		return -1, fmt.Errorf("No such user: %s", user_email)
	}
	return info.Users[0].ID, nil
}

// Creates a new user on the system.
func (s Session) NewUser(user_email string, type_id int, verified, notify bool) (id int, err error) {
	id = -1

	var info KiteUser

	req := KiteRequest{
		Action: "POST",
		Path:   "/rest/users",
		Params: SetParams(PostJSON{"email": user_email, "userTypeId": type_id, "verified": verified, "sendNotification": notify}, Query{"returnEntity": true}),
		Output: &info,
	}

	if err = s.Call(req); err != nil {
		id = info.ID
	}

	return id, err
}

// Returns the folder id of folder, can be specified as TopFolder/Nested or TopFolder\Nested.
func (s Session) FindFolder(base_folder_id int, remote_folder string) (id int, err error) {

	id = -1

	folder_names := strings.Split(remote_folder, "\\")
	if len(folder_names) == 1 {
		folder_names = strings.Split(remote_folder, "/")
	}

	shift_name := func() bool {
		if len(folder_names) > 1 {
			folder_names = folder_names[1:]
			return true
		}
		return false
	}

	var folders KiteArray

	if base_folder_id < 1 {
		folders, err = s.GetFolders()
		if err != nil {
			return
		}
	} else {
		folders, err = s.ListFolders(base_folder_id)
		if err != nil {
			return
		}
	}

	for _, e := range folders.Data {
		if strings.ToLower(e.Name) == strings.ToLower(folder_names[0]) {
			id = e.ID
			break
		}
	}

	if id < 0 {
		return -1, fmt.Errorf("Couldn't find folder: %s", folder_names[0])
	}

	for shift_name() {
		found := false
		nested, err := s.ListFolders(id)
		if err != nil {
			return -1, err
		}

		for _, elem := range nested.Data {
			if strings.ToLower(elem.Name) == strings.ToLower(folder_names[0]) {
				id = elem.ID
				found = true
				break
			}
		}

		if !found {
			return -1, fmt.Errorf("Couldn't find folder: %s", folder_names[0])
		}
	}

	return
}

func (s Session) ChangeFolder(folder_id int, body string) error {
	pj := make(PostJSON)
	if err := json.Unmarshal([]byte(body), &pj); err != nil {
		return err
	}
	req := KiteRequest{
		Action: "PUT",
		Path:   SetPath("/rest/folders/%d", folder_id),
		Params: SetParams(pj),
	}
	return s.Call(req)
}

func (s Session) NewUpload(folder_id int, filename string, modtime time.Time) (int, string, error) {
	type T struct {
		URI string `json:"uri"`
		ID  int    `json:"id"`
	}

	var o T

	req := KiteRequest{
		Action: "POST",
		Path:   SetPath("/rest/folders/%d/actions/initiateUpload", folder_id),
		Params: SetParams(PostJSON{"filename": filename, "clientModified": write_kw_time(modtime)}, Query{"returnEntity": "true", "mode": "full"}),
		Output: &o,
	}

	return o.ID, o.URI, s.Call(req)
}

func (s Session) DeleteUpload(upload_id int) error {
	req := KiteRequest{
		Action: "DELETE",
		Path:   SetPath("/rest/uploads/%d", upload_id),
	}
	return s.Call(req)
}

func (s Session) AddUserToFolder(user_id int, folder_id int, role_id int, notify bool) (err error) {
	req := KiteRequest{
		Action: "POST",
		Path:   SetPath("/rest/folders/%d/members", folder_id),
		Params: SetParams(PostJSON{"roleId": role_id, "userIds": []int{user_id}, "notify": notify}, Query{"returnEntity": false}),
	}
	return s.Call(req)
}

func (s Session) AddEmailToFolder(email string, folder_id int, role_id int, notify bool, file_notifications bool) (err error) {
	req := KiteRequest{
		Action: "POST",
		Path:   SetPath("/rest/folders/%d/members", folder_id),
		Params: SetParams(PostJSON{"roleId": role_id, "emails": []string{email}, "notify": notify, "notifyFileAdded": file_notifications}, Query{"returnEntity": false}, Query{"updateIfExists": true, "partialSuccess": true}),
	}
	return s.Call(req)
}

func (s Session) RemoveEmailFromFolder(email string, folder_id int, nested bool) (err error) {
	user_id, err := s.FindUser(email)
	if err != nil {
		return err
	}
	req := KiteRequest{
		Action: "DELETE",
		Path:   SetPath("/rest/folders/%d/members/%d", folder_id, user_id),
		Params: SetParams(Query{"downgradeNested": nested}),
	}
	return s.Call(req)
}

// Get user information.
func (s Session) UserInfo(user_id int) (output KiteUser, err error) {
	req := KiteRequest{
		Action: "GET",
		Path:   SetPath("/rest/users/%d", user_id),
		Output: &output,
	}
	return output, s.Call(req)
}

// List Folders.
func (s Session) ListFolders(folder_id int) (output KiteArray, err error) {
	req := KiteRequest{
		APIVer: 7,
		Action: "GET",
		Path:   SetPath("/rest/folders/%d/folders", folder_id),
		Params: SetParams(Query{"deleted": false}),
		Output: &output,
	}
	return output, s.Call(req)
}

// List Files.
func (s Session) ListFiles(folder_id int) (output KiteArray, err error) {
	req := KiteRequest{
		APIVer: 5,
		Action: "GET",
		Path:   SetPath("/rest/folders/%d/files", folder_id),
		Params: SetParams(Query{"deleted": false}),
		Output: &output,
	}
	return output, s.Call(req)
}

// Find Files.
func (s Session) FindFile(folder_id int, filename string) (output KiteArray, err error) {
	req := KiteRequest{
		Action: "GET",
		Path:   SetPath("/rest/folders/%d/files", folder_id),
		Params: SetParams(Query{"deleted": false, "name": filename, "mode": "full"}),
		Output: &output,
	}
	return output, s.Call(req)
}

// Get File Information
func (s Session) FileInfo(file_id int) (output KiteData, err error) {
	req := KiteRequest{
		Action: "GET",
		Path:   SetPath("/rest/files/%d", file_id),
		Params: SetParams(Query{"deleted": false}),
		Output: &output,
	}
	return output, s.Call(req)
}

// Returns Folder information.
func (s Session) FolderInfo(folder_id int) (output KiteData, err error) {
	req := KiteRequest{
		Action: "GET",
		Path:   SetPath("/rest/folders/%d", folder_id),
		Params: SetParams(Query{"mode": "full", "deleted": false}),
		Output: &output,
	}
	return output, s.Call(req)
}

// Deletes file from system, can be recovered.
func (s Session) DeleteFile(file_id int) (err error) {
	req := KiteRequest{
		Action: "DELETE",
		Path:   SetPath("/rest/files/%d", file_id),
	}
	return s.Call(req)
}

// Create remote folder
func (s Session) CreateFolder(parent_id int, name string) (folder_id int, err error) {
	var data KiteData

	req := KiteRequest{
		Action: "POST",
		Path:   SetPath("/rest/folders/%d/folders", parent_id),
		Params: SetParams(PostJSON{"name": name}, Query{"returnEntity": true}),
		Output: &data,
	}

	err = s.Call(req)
	return data.ID, err
}

// Find sent files
func (s Session) FindMail(filter Query) (mail_id []int, err error) {
	var m struct {
		Data []struct {
			ID int `json:"id"`
		} `json:"data"`
	}

	req := KiteRequest{
		Action: "GET",
		Path:   "/rest/mail",
		Params: SetParams(filter),
		Output: &m,
	}

	if err = s.Call(req); err != nil {
		return nil, err
	}

	for _, ent := range m.Data {
		mail_id = append(mail_id, ent.ID)
	}

	return
}
