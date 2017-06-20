package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/cmcoffee/go-logger"
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
func (s Session) respError(resp *http.Response) (err error) {

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
		logger.Put("<-- RESPONSE STATUS: %s\n", resp.Status)
	}

	output, err := ioutil.ReadAll(body)
	if err != nil {
		return err
	}

	if snoop {
		logger.Put("\n")
	}

	var kite_err *KiteErr

	json.Unmarshal(output, &kite_err)

	defer resp.Body.Close()

	if kite_err != nil {
		e := NewKError()
		for _, v := range kite_err.Errors {
			e.AddError(v.Code, v.Message)
		}
		if kite_err.ErrorDesc != NONE {
			e.AddError(kite_err.Error, kite_err.ErrorDesc)
		}
		if e.IsSet(ERR_AUTH_PROFILE_CHANGED) {
			var kauth *KiteAuth
			DB.Get("tokens", s, &kauth)
			if kauth != nil {
				kauth.Expiry = 0
				DB.CryptSet("tokens", s, &kauth)
			}
		}
		return e
	}
	return fmt.Errorf(resp.Status)
}

// Wrapper around request and client to make simple requests for information to appliance.
func (s Session) Call(action, path string, output interface{}, input ...interface{}) (err error) {

	req, err := s.NewRequest(action, path)
	if err != nil {
		return err
	}

	action = strings.ToUpper(action)

	if snoop {
		logger.Put("\n--> ACTION: \"%s\" PATH: \"%s\"\n", action, path)
	}

	for _, in := range input {
		switch i := in.(type) {
		case PostFORM:
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			p := make(url.Values)
			for k, v := range i {
				p.Add(k, fmt.Sprintf("%v", v))
				if snoop {
					logger.Put("\\-> POST PARAM: \"%s\" VALUE: \"%s\"\n", k, p[k])
				}
			}
			req.Body = ioutil.NopCloser(bytes.NewReader([]byte(p.Encode())))
		case PostJSON:
			req.Header.Set("Content-Type", "application/json")
			json, err := json.Marshal(i)
			if err != nil {
				return err
			}
			if snoop {
				logger.Put("\\-> POST JSON: %s\n", string(json))
			}
			req.Body = ioutil.NopCloser(bytes.NewReader([]byte(json)))
		case Query:
			q := req.URL.Query()
			for k, v := range i {
				q.Set(k, fmt.Sprintf("%v", v))
				if snoop {
					logger.Put("\\-> QUERY: %s=%s\n", k, q[k])
				}
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

	return s.DecodeJSON(resp, output)
}

// Create new formed http request for appliance.
func (s Session) NewRequest(action, path string) (req *http.Request, err error) {
	req, err = http.NewRequest(action, fmt.Sprintf("https://%s%s", server, path), nil)
	if err != nil {
		return nil, err
	}

	req.URL.Host = server
	req.URL.Scheme = "https"
	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%s", KWAPI_VERSION))
	req.Header.Set("User-Agent", fmt.Sprintf("%s(v%s)", NAME, VERSION))
	req.Header.Set("Referer", "https://"+server+"/")

	access_token, err := s.GetToken()
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+access_token)

	return req, nil
}

// Create new client session to appliance.
func (s Session) NewClient() *http.Client {

	var transport http.Transport

	// Allows invalid certs if set to "no" in config.
	if strings.ToLower(Config.Get("configuration", "ssl_verify")) == "no" {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// Setup proxy setting.
	if proxy_host := Config.Get("configuration", "proxy"); proxy_host != NONE {
		proxyURL, err := url.Parse(proxy_host)
		errChk(err)
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	return &http.Client{Transport: &transport}
}

// Decodes JSON response body to provided interface.
func (s Session) DecodeJSON(resp *http.Response, output interface{}) (err error) {

	defer resp.Body.Close()
	defer func() {
		if snoop { logger.Put("} \n\n") }
	}()
	
	err = s.respError(resp)
	if err != nil {
		return
	}

	var body io.Reader

	if snoop {
		body = io.TeeReader(resp.Body, os.Stderr)
	} else {
		body = resp.Body
	}

	if output == nil && snoop {
		logger.Put("<-- RESPONSE STATUS: %s\n", resp.Status)
		ioutil.ReadAll(body)
		return nil
	} else if output == nil {
		return nil
	}

	if snoop {
		logger.Put("<-- RESPONSE STATUS: %s\n", resp.Status)
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
	err = s.Call("GET", "/rest/roles", &roles)
	if err != nil {
		return
	}

	return
}

// Pulls up all top level folders.
func (s Session) GetFolders() (output KiteArray, err error) {
	return output, s.Call("GET", "/rest/folders/top", &output, Query{"deleted": false})
}

// Find a user_id
func (s Session) FindUser(user_email string) (id int, err error) {
	id = -1

	var info struct {
		Users []KiteUser `json:"data"`
	}

	err = s.Call("GET", "/rest/users", &info, Query{"email":user_email, "mode":"compact"})
	if err != nil {
		return -1, err
	}

	if len(info.Users) == 0 {
		return -1, fmt.Errorf("No such user: %s", user_email)
	}
	return info.Users[0].ID, nil
}

func (s Session) FindChildFolder(parent_folder int, child_folder string) (id int, err error) {
	sub_folders, err := s.ListFolders(parent_folder)
	if err != nil {
		return -1, err
	}
	for _, folder := range sub_folders.Data {
		if folder.Name == child_folder {
			return folder.ID, nil
		}
	}
	return -1, fmt.Errorf("%s: Child folder not found.", child_folder)
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

func (s Session) NewUpload(folder_id int, filename string, modtime time.Time) (int, string, error) {
	type T struct {
		URI string `json:"uri"`
		ID  int    `json:"id"`
	}
	var o T
	return o.ID, o.URI, s.Call("POST", fmt.Sprintf("/rest/folders/%d/actions/initiateUpload", folder_id), &o, PostJSON{"filename": filename, "clientModified": write_kw_time(modtime)}, Query{"returnEntity": "true", "mode": "full"})
}

func (s Session) DeleteUpload(upload_id int) error {
	return s.Call("DELETE", fmt.Sprintf("/rest/uploads/%d", upload_id), nil)
}

func (s Session) AddUserToFolder(user_id int, folder_id int, role_id int, notify bool) (err error) {
	return s.Call("POST", fmt.Sprintf("/rest/folders/%d/members", folder_id), nil, PostJSON{"roleId": role_id, "userId": user_id, "notify": notify}, Query{"returnEntity": false})
}

// Get user information.
func (s Session) UserInfo(user_id int) (output KiteUser, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/users/%d", user_id), &output)
}

// List Folders.
func (s Session) ListFolders(folder_id int) (output KiteArray, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/folders/%d/folders", folder_id), &output, Query{"deleted": false})
}

// List Files.
func (s Session) ListFiles(folder_id int) (output KiteArray, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/folders/%d/files", folder_id), &output, Query{"deleted": false})
}

// Find Files.
func (s Session) FindFile(folder_id int, filename string) (output KiteArray, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/folders/%d/files", folder_id), &output, Query{"deleted": false, "name": filename, "mode": "full"})
}

// Get File Information
func (s Session) FileInfo(file_id int) (output KiteData, err error) {
	return output, s.Call("GET", fmt.Sprintf("/rest/files/%d", file_id), &output, Query{"deleted": false})
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
func (s Session) CreateFolder(parent_id int, name string) (folder_id int, err error) {
	var new_folder KiteData
	err = s.Call("POST", fmt.Sprintf("/rest/folders/%d/folders", parent_id), &new_folder, PostJSON{"name": name}, Query{"returnEntity": true})
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

// Find sent files
func (s Session) FindMail(filter Query) (mail_id []int, err error) {
	var m struct {
		Data []struct {
			ID int `json:"id"`
		} `json:"data"`
	}

	if err = s.Call("GET", "/rest/mail", &m, filter); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	for _, ent := range m.Data {
		mail_id = append(mail_id, ent.ID)
	}

	return
}
