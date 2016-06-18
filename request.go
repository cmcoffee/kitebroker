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
	task    string
	account string
	server  string
	*KiteAuth
}

// Create a new Kiteworks API Session
func NewSession(task, account string) *Session {
	return &Session{task, account, Config.Get(task, "server")[0], nil}
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
		case map[string]string:
			if action == "POST" {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				p := new(url.Values)
				for k, v := range i {
					p.Add(k, v)
				}
				req.Body = ioutil.NopCloser(bytes.NewReader([]byte(p.Encode())))
			} else {
				q := req.URL.Query()
				for k, v := range i {
					q.Set(k, v)
				}
				req.URL.RawQuery = q.Encode()
			}
		case io.ReadCloser:
			req.Body = i
		}
	}

	client := s.NewClient()

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	return s.DecodeJSON(resp, output)
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

	req, err = http.NewRequest(action, fmt.Sprintf("https://%s/%s", s.server, path), nil)
	if err != nil {
		return nil, err
	}

	req.URL.Host = s.server
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
	if strings.ToLower(Config.Get(s.task, "ssl_verify")[0]) == "no" {
		ignore_cert = true
	}

	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: ignore_cert},
	},
	}
}

// Decodes JSON response body to provided interface.
func (s Session) DecodeJSON(resp *http.Response, output interface{}) (err error) {

	if resp.StatusCode > 299 || resp.StatusCode < 200 {
		return s.respError(resp)
	}

	// Allows us to snoop on the connection, check for bad JSON data.
	snoop := false

	var body io.Reader

	if snoop == true {
		body = io.TeeReader(resp.Body, os.Stderr)
	} else {
		body = resp.Body
	}

	if output == nil && snoop == true {
		ioutil.ReadAll(body)
		return nil
	} else if output == nil {
		return nil
	}

	dec := json.NewDecoder(body)
	return dec.Decode(output)
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
	Filelifetime string      `json:"fileLifetime"`
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
		if e.Relationship == "syncdir" {
			return e.ID, nil
		}
	}
	return -1, nil
}

// Get user information.
func (s Session) UserInfo(user_id int) (output KiteUser, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/users/%d", user_id), &output)
}

// List Folders.
func (s Session) ListFolders(folder_id int) (output KiteArray, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/folders/%d/folders", folder_id), &output)
}

// List Files.
func (s Session) ListFiles(folder_id int) (output KiteArray, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/folders/%d/files", folder_id), &output)
}

// Get File Information
func (s Session) FileInfo(file_id int) (output KiteData, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/files/%d", file_id), &output)
}

// Returns Folder information.
func (s Session) FolderInfo(folder_id int) (output *KiteData, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/folders/%d", folder_id), output, map[string]string{"mode": "full"})
}

// Deletes file from system.
func (s Session) DeleteFile(file_id int) (err error) {
	return s.Call("DELETE", fmt.Sprintf("/rest/files/%d", file_id), nil)
}

// Deletes file from system. (permanent)
func (s Session) KillFile(file_id int) (err error) {
	return s.Call("DELETE", fmt.Sprintf("/rest/files/%d/actions/permanent", file_id), nil)
}
