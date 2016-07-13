package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// API Session
type Session struct {
	account string
	*KiteAuth
}

var call_done struct{}
var api_call_bank chan struct{}
var transfer_call_bank chan struct{}

type PostJSON map[string]interface{}
type PostFORM map[string]string
type Query map[string]string

// Create a new Kiteworks API Session
func NewSession(account string) *Session {
	return &Session{account, nil}
}

// Wrapper around request and client to make simple requests for information to appliance.
func (s Session) Call(action, path string, output interface{}, input ...interface{}) (err error) {

	req, err := s.NewRequest(action, path)
	if err != nil {
		return err
	}

	action = strings.ToUpper(action)

	for _, in := range input {
		switch i := in.(type) {
		case PostFORM:
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			p := make(url.Values)
			for k, v := range i {
				p.Add(k, v)
			}
			req.Body = ioutil.NopCloser(bytes.NewReader([]byte(p.Encode())))
		case PostJSON:
			req.Header.Set("Content-Type", "application/json")
			json, err := json.Marshal(i)
			if err != nil {
				return err
			}
			if call_snoop {
				fmt.Println(string(json))
			}
			req.Body = ioutil.NopCloser(bytes.NewReader([]byte(json)))
		case Query:
			q := req.URL.Query()
			for k, v := range i {
				q.Set(k, v)
			}
			req.URL.RawQuery = q.Encode()
		case io.ReadCloser:
			req.Body = i
		}
	}

	client := s.NewClient()

	<-api_call_bank
	defer func() { api_call_bank <- call_done }()

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	err = s.DecodeJSON(resp, output)
	if err != nil {
		if strings.Contains(err.Error(), "invalid_grant") {
			DB.Unset("tokens", s.account)
			s.KiteAuth = nil
		}
	}
	return err
}

// Create new formed http request for appliance.
func (s Session) NewRequest(action, path string) (req *http.Request, err error) {

	// Get Access Token.
	if s.KiteAuth == nil || (s.KiteAuth.Expiry-3600) < time.Now().Unix() {
		s.KiteAuth, err = s.GetToken()
		if err != nil {
			return nil, err
		}
	}

	req, err = http.NewRequest(action, fmt.Sprintf("https://%s%s", server, path), nil)
	if err != nil {
		return nil, err
	}

	req.URL.Host = server
	req.URL.Scheme = "https"
	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%s", KWAPI_VERSION))
	req.Header.Set("User-Agent", fmt.Sprintf("%s(v%s)", NAME, VERSION))
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)

	return req, nil
}

// Create new client session to appliance.
func (s Session) NewClient() *http.Client {

	var ignore_cert bool

	// Allows invalid certs if set to "no" in config.
	if strings.ToLower(Config.SGet(NAME, "ssl_verify")) == "no" {
		ignore_cert = true
	}

	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: ignore_cert},
	},
	}
}

// Decodes JSON response body to provided interface.
func (s Session) DecodeJSON(resp *http.Response, output interface{}) (err error) {

	defer resp.Body.Close()

	if resp.StatusCode > 299 || resp.StatusCode < 200 {
		return s.respError(resp)
	}

	var body io.Reader

	if resp_snoop {
		body = io.TeeReader(resp.Body, os.Stderr)
	} else {
		body = resp.Body
	}

	if output == nil && resp_snoop {
		ioutil.ReadAll(body)
		return nil
	} else if output == nil {
		return nil
	}

	dec := json.NewDecoder(body)
	err = dec.Decode(output)
	if err == io.EOF {
		return nil
	}
	return
}

// Kiteworks User Data
type KiteUser struct {
	ID         int         `json:"id"`
	Active     bool        `json:"active"`
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
	Href         string `json:"href"`
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
	Size         int         `json:"size"`
	Mime         string      `json:"mime"`
	Quarantined  bool        `json:"quarantined"`
	DLPLocked    bool        `json:"dlpLocked"`
	Filelifetime interface{} `json:"fileLifetime"`
	Type         string      `json:"type"`
	Links        []KiteLinks `json:"links"`
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

func (s Session) SetProgress(url string, flag int) (err error) {
	return
}

func (s Session) GetProgress(url string) (flag int) {
	return 0
}

// Get My User information.
func (s Session) MyUser() (output KiteUser, err error) {
	return output, s.Call("GET", "/rest/users/me", &output)
}

// Returns Folder ID of the Account's My Folder.
func (s Session) MyFolderID() (file_id int, err error) {
	out, err := s.MyUser()
	if err != nil {
		return -1, err
	}

	for _, e := range out.Links {
		if strings.ToLower(e.Relationship) == "syncdir" {
			return e.ID, nil
		}
	}
	return -1, nil
}

// Get url to self.
func (s Session) GetSelfHref(input *[]KiteLinks) string {
	for _, link := range *input {
		if strings.ToLower(link.Relationship) == "self" {
			return link.Href
		}
	}
	return NONE
}

func (s Session) GetRoles() (roles KiteArray, err error) {
	err = s.Call("GET", "/rest/roles", &roles)
	if err != nil {
		return
	}

	return
}

// Pulls up all top level folders.
func (s Session) GetFolders() (output KiteArray, err error) {
	return output, s.Call("GET", "/rest/folders/top", &output)
}

// Find a user_id
func (s Session) FindUser(user_email string) (id int, err error) {
	id = -1
	sub_session := NewSession(user_email)
	info, err := sub_session.MyUser()
	if err != nil {
		if strings.Contains(err.Error(), "Invalid user") {
			return -1, fmt.Errorf("No such user: %s", user_email)
		}
		return -1, err
	}
	return info.ID, nil
}

// Returns the folder id of folder, can be specified as TopFolder/Nested or TopFolder\Nested.
func (s Session) FindFolder(remote_folder string) (id int, err error) {

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

	if folder_names[0] == "My Folder" {
		id, err = s.MyFolderID()
		if err != nil {
			return
		}
	} else {
		top_shared, err := s.GetFolders()
		if err != nil {
			return -1, err
		}
		for _, e := range top_shared.Data {
			if e.Name == folder_names[0] {
				id = e.ID
				break
			}
		}
	}

	if id < 0 {
		return -1, fmt.Errorf("Couldn't find top level folder: %s", folder_names[0])
	}

	for shift_name() {
		found := false
		nested, err := s.ListFolders(id)
		if err != nil {
			return -1, err
		}

		for _, elem := range nested.Data {
			if elem.Name == folder_names[0] {
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

func (s Session) NewFile(folder_id int, filename string) (string, error) {
	type T struct {
		URI string `json:"uri"`
	}
	var o T
	return o.URI, s.Call("POST", fmt.Sprintf("/rest/folders/%d/actions/initiateUpload", folder_id), &o, PostJSON{"filename": filename}, Query{"returnEntity": "true", "mode": "full"})

}

func (s Session) AddUserToFolder(user_id int, folder_id int, role_id int, notify bool) (err error) {
	return s.Call("POST", fmt.Sprintf("/rest/folders/%d/members", folder_id), nil, PostJSON{"roleId": role_id, "userId": user_id, "notify": notify}, Query{"returnEntity": "false"})
}

// Get user information.
func (s Session) UserInfo(user_id int) (output KiteUser, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/users/%d", user_id), &output)
}

// List Folders.
func (s Session) ListFolders(folder_id int) (output KiteArray, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/folders/%d/folders", folder_id), &output, Query{"deleted": "false"})
}

// List Files.
func (s Session) ListFiles(folder_id int) (output KiteArray, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/folders/%d/files", folder_id), &output, Query{"deleted": "false"})
}

// Get File Information
func (s Session) FileInfo(file_id int) (output KiteData, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/files/%d", file_id), &output)
}

// Returns Folder information.
func (s Session) FolderInfo(folder_id int) (output KiteData, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/folders/%d", folder_id), &output, Query{"mode": "full"})
}

// Deletes file from system, can be recovered.
func (s Session) DeleteFile(file_id int) (err error) {
	return s.Call("DELETE", fmt.Sprintf("/rest/files/%d", file_id), nil)
}

// Create remote folder
func (s Session) CreateFolder(name string, parent_id int) (folder_id int, err error) {
	var new_folder KiteData
	err = s.Call("POST", fmt.Sprintf("/rest/folders/%d/folders", parent_id), &new_folder, PostJSON{"name": name}, Query{"returnEntity": "true"})
	return new_folder.ID, err
}

// Deletes file from system permanently.
func (s Session) EraseFile(file_id int) (err error) {
	err = s.Call("DELETE", fmt.Sprintf("/rest/files/%d", file_id), nil)
	if err != nil {
		return
	}
	return s.Call("DELETE", fmt.Sprintf("/rest/files/%d/actions/permanent", file_id), nil)
}

// Add User to system.
func (s *Session) AddUser(email string, verify, notify bool) (err error) {
	var (
		new_user KiteData
		verified bool
	)
	if !verify {
		verified = true
	}
	if err = s.Call("POST", "/rest/admin/users", &new_user, PostJSON{"email": email, "sendNotification": notify}, Query{"returnEntity": "true"}); err != nil {
		return err
	}
	return s.Call("PUT", fmt.Sprintf("/rest/admin/users/%d", new_user.ID), nil, PostJSON{"active": true, "verified": verified})
}
