package core

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"os"
)

var ErrNotFound = errors.New("Requested item not found.")

// KiteFile/Folder/Attachment
type KiteObject struct {
	Type                  string         `json:"type"`
	Status                string         `json:"status"`
	ID                    int            `json:"id"`
	Name                  string         `json:"name"`
	Description           string         `json:"description"`
	Created               string         `json:"created"`
	Modified              string         `json:"modified"`
	ClientCreated         string         `json:"clientCreated"`
	ClientModified        string         `json:"clientModified"`
	Deleted               bool           `json:"deleted"`
	PermDeleted           bool           `json:"permDeleted"`
	Expire                interface{}    `json:"expire"`
	Path                  string         `json:"path"`
	ParentID              int            `json:"parentId"`
	UserID                int            `json:"userId"`
	Permalink             string         `json:"permalink"`
	Secure                bool           `json:"secure"`
	LockUser              int            `json:"lockUser"`
	Fingerprint           string         `json:"fingerprint"`
	ProfileID             int            `json:"typeID`
	Size                  int64          `json:"size"`
	Mime                  string         `json:"mime"`
	AVStatus              string         `json:"avStatus"`
	DLPStatus             string         `json:"dlpStatus"`
	AdminQuarantineStatus string         `json:"adminQuarantineStatus`
	Quarantined           bool           `json:"quarantined"`
	DLPLocked             bool           `json:"dlpLocked"`
	FileLifetime          int            `json:"fileLifetime"`
	MailID                int            `json:"mail_id"`
	Links                 []KiteLinks    `json:"links"`
	CurrentUserRole       KitePermission `json:"currentUserRole"`
}

// Returns the Expiration in time.Time.
func (K *KiteObject) Expiry() time.Time {
	var exp_time time.Time

	if exp_string, ok := K.Expire.(string); ok {
		exp_time, _ = ReadKWTime(exp_string)
	}

	return exp_time
}

// Kiteworks Links Data
type KiteLinks struct {
	Relationship string `json:"rel"`
	Entity       string `json:"entity"`
	ID           int    `json:"id"`
	URL          string `json:"href"`
}

// Permission information
type KitePermission struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Rank       int    `json:"rank"`
	Modifiable bool   `json:"modifiable"`
	Disabled   bool   `json:"disabled"`
}

type kw_rest_folder struct {
	folder_id int
	*KWSession
}

func (s KWSession) Folder(folder_id int) kw_rest_folder {
	return kw_rest_folder{
		folder_id,
		&s,
	}
}

// Find item in folder, using folder path, if folder_id > 0, start search there.
func (s kw_rest_folder) Find(path string, params ...interface{}) (result KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}

	folder_path := SplitPath(path)

	if len(folder_path) == 0 {
		folder_path = append(folder_path, path)
	}

	var current []KiteObject

	if s.folder_id <= 0 {
		current, err = s.TopFolders(params)
	} else {
		current, err = s.Folder(s.folder_id).Contents(params)
	}
	if err != nil {
		return
	}

	var found bool

	folder_len := len(folder_path) - 1

	for i, f := range folder_path {
		found = false
		for _, c := range current {
			if strings.ToLower(f) == strings.ToLower(c.Name) {
				if i < folder_len && c.Type == "d" {
					current, err = s.Folder(c.ID).Contents(params)
					if err != nil {
						return
					}
					found = true
					break
				} else if i == folder_len {
					result = c
					return
				}
			}
		}
		if found == false {
			return result, ErrNotFound
		}
	}

	return result, ErrNotFound
}

type kw_rest_admin struct {
	*KWSession
}

func (s KWSession) Admin() kw_rest_admin {
	return kw_rest_admin{&s}
}

func (s kw_rest_admin) FindProfileUsers(profile_id int, params ...interface{}) (emails []string, err error) {
	var users []struct {
		Email string `json:"email"`
	}
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/admin/profiles/%d/users", profile_id),
		Params: SetParams(params),
		Output: &users,
	}, -1, 1000)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		emails = append(emails, u.Email)
	}
	return
}

type kw_rest_file struct {
	file_id int
	*KWSession
}

func (s KWSession) File(file_id int) kw_rest_file {
	return kw_rest_file{file_id, &s}
}

func (s kw_rest_file) Info(params ...interface{}) (result KiteObject, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/files/%d", s.file_id),
		Params: SetParams(params),
		Output: &result,
	})
	return
}

func (s kw_rest_file) Delete(params ...interface{}) (err error) {
	err = s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/files/%d", s.file_id),
		Params: SetParams(params),
	})
	return
}

func (s kw_rest_file) PermDelete() (err error) {
	err = s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/files/%d/actions/permanent", s.file_id),
	})
	return
}

// Create a new file version for an existing file.
func (S kw_rest_file) NewVersion(file os.FileInfo) (int, error) {
	var upload struct {
		ID int `json:"id"`
	}

	if err := S.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/files/%d/actions/initiateUpload", S.file_id),
		Params: SetParams(PostJSON{"filename": file.Name(), "totalSize": file.Size(), "clientModified": WriteKWTime(file.ModTime().UTC()), "totalChunks": S.Chunks(file.Size())}, Query{"returnEntity": true}),
		Output: &upload,
	}); err != nil {
		return -1, err
	}
	return upload.ID, nil
}

/*
// Drills down specific folder and returns all results.
func (s KWSession) CrawlFolder(folder_id int, params...interface{}) (results []KiteObject, err error) {
	if len(params) == 0 {

	}
}*/

// Get list of all top folders
func (s KWSession) TopFolders(params ...interface{}) (folders []KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}

	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   "/rest/folders/top",
		Output: &folders,
		Params: SetParams(params, Query{"with": "(path,currentUserRole)"}),
	}, -1, 1000)
	return
}

// Creates a new upload for a folder.
func (S kw_rest_folder) NewUpload(file os.FileInfo) (int, error) {
	var upload struct {
		ID int `json:"id"`
	}

	if err := S.Call(APIRequest{
		Version: 5,
		Method:  "POST",
		Path:    SetPath("/rest/folders/%d/actions/initiateUpload", S.folder_id),
		Params:  SetParams(PostJSON{"filename": file.Name(), "totalSize": file.Size(), "clientModified": WriteKWTime(file.ModTime().UTC()), "totalChunks": S.Chunks(file.Size())}, Query{"returnEntity": true}),
		Output:  &upload,
	}); err != nil {
		return -1, err
	}
	return upload.ID, nil
}

// Returns all items with listed folder_id.
func (s kw_rest_folder) Contents(params ...interface{}) (children []KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%d/children", s.folder_id),
		Output: &children,
		Params: SetParams(params, Query{"with": "(path,currentUserRole)"}),
	}, -1, 1000)

	return
}

// Returns all items with listed folder_id.
func (s kw_rest_folder) Folders(params ...interface{}) (children []KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%d/folders", s.folder_id),
		Output: &children,
		Params: SetParams(params, Query{"with": "(path,currentUserRole)"}),
	}, -1, 1000)

	return
}

func (s kw_rest_folder) Files(params ...interface{}) (children []KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%d/files", s.folder_id),
		Output: &children,
		Params: SetParams(params),
	}, -1, 1000)

	return
}

func (s kw_rest_folder) Info(params ...interface{}) (output KiteObject, err error) {
	if params == nil {
		params = SetParams(Query{"deleted": false})
	}
	if s.folder_id == 0 {
		return
	}
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%d", s.folder_id),
		Params: SetParams(params, Query{"mode": "full", "with": "(currentUserRole, fileLifetime, path)"}),
		Output: &output,
	})
	return
}

func (s kw_rest_folder) NewFolder(name string, params ...interface{}) (output KiteObject, err error) {
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/folders/%d/folders", s.folder_id),
		Params: SetParams(PostJSON{"name": name}, Query{"returnEntity": true}, params),
		Output: &output,
	})
	return
}

// Kiteworks User Data
type KiteUser struct {
	ID          int    `json:"id"`
	Active      bool   `json:"active"`
	Deactivated bool   `json:"deactivated"`
	Suspended   bool   `json:"suspended"`
	BaseDirID   int    `json:"basedirId"`
	Deleted     bool   `json:"deleted"`
	Email       string `json:"email"`
	MyDirID     int    `json:"mydirId"`
	Name        string `json:"name"`
	SyncDirID   int    `json:"syncdirId"`
	UserTypeID  int    `json:"userTypeId"`
	Verified    bool   `json:"verified"`
	Internal    bool   `json:"internal"`
}

// Retrieve my user info.
func (s KWSession) MyUser() (user KiteUser, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   "/rest/users/me",
		Output: &user,
	})
	return
}

// Get total count of users.
func (s kw_rest_admin) UserCount(emails []string, params ...interface{}) (users int, err error) {
	var user []struct{}
	if emails != nil && emails[0] != NONE {
		for _, u := range emails {
			err = s.DataCall(APIRequest{
				Method: "GET",
				Path:   "/rest/admin/users",
				Params: SetParams(Query{"email": u}, params),
				Output: &user}, -1, 1000)
			if err != nil {
				return
			}
			users = len(user) + users
		}
		return
	}
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   "/rest/admin/users",
		Params: SetParams(params),
		Output: &user}, -1, 1000)
	return len(user), err
}

// Get Users
type GetUsers struct {
	offset    int
	filter    Query
	emails    []string
	params    []interface{}
	session   *kw_rest_admin
	completed bool
}

// Admin EAPI endpoint to pull all users matching parameters.
func (s kw_rest_admin) Users(emails []string, params ...interface{}) *GetUsers {
	var T GetUsers
	T.filter = make(Query)
	T.offset = 0
	T.emails = emails

	// First extract the query from request.
	params = SetParams(params)

	var query Query

	tmp := params[0:0]
	for _, v := range params {
		switch e := v.(type) {
		case Query:
			query = e
		default:
			tmp = append(tmp, v)
		}
	}
	params = tmp

	// Next take remainder of query and reattach it to outgoing request.
	var forward_query Query
	forward_query = make(Query)
	for key, val := range query {
		switch strings.ToLower(key) {
		case "suspended":
			fallthrough
		case "active":
			fallthrough
		case "deleted":
			fallthrough
		case "verified":
			T.filter[key] = val
		default:
			forward_query[key] = val
		}
	}

	T.params = SetParams(params, forward_query)
	T.session = &s
	return &T
}

// Return a set of users to process.
func (T *GetUsers) Next() (users []KiteUser, err error) {
	if T.emails != nil && T.emails[0] != NONE {
		if !T.completed {
			T.completed = true
			return T.findEmails()
		} else {
			return []KiteUser{}, nil
		}
	}
	for {
		var raw_users []KiteUser
		err = T.session.DataCall(APIRequest{
			Method: "GET",
			Path:   "/rest/admin/users",
			Params: T.params,
			Output: &raw_users}, T.offset, 1000)
		if err != nil {
			return nil, err
		}
		if len(raw_users) == 0 {
			return
		}
		T.offset = T.offset + len(raw_users)

		users, err = T.filterUsers(raw_users)
		if err != nil {
			return nil, err
		}
		if len(users) == 0 {
			continue
		} else {
			break
		}
	}
	return
}

func (T *GetUsers) findEmails() (users []KiteUser, err error) {
	for _, u := range T.emails {
		var raw_users []KiteUser
		err = T.session.DataCall(APIRequest{
			Method: "GET",
			Path:   "/rest/admin/users",
			Params: SetParams(Query{"email": u}, T.params),
			Output: &raw_users}, -1, 1000)
		if err != nil {
			Err("%s: %s", u, err.Error())
			continue
		}
		filtered_users, err := T.filterUsers(raw_users)
		if err != nil {
			return nil, err
		}
		if len(filtered_users) > 0 {
			users = append(users, filtered_users[0:]...)
			continue
		}
		Err("%s: User not found, or did not meet specified criteria", u)
	}
	return
}

// Filter out users matching filter specified in GetUsers call.
func (T *GetUsers) filterUsers(input []KiteUser) (users []KiteUser, err error) {
	// Match bool variables
	matchBool := func(input KiteUser, key string, value bool) bool {
		var bool_var bool

		switch key {
		case "suspended":
			bool_var = input.Suspended
		case "active":
			bool_var = input.Active
		case "deleted":
			bool_var = input.Deleted
		case "verified":
			bool_var = input.Verified
		}

		if bool_var == value {
			return true
		}
		return false
	}

	for key, val := range T.filter {
		key = strings.ToLower(key)
		switch key {
		case "suspended":
			fallthrough
		case "active":
			fallthrough
		case "deleted":
			fallthrough
		case "verified":
			if v, ok := val.(bool); ok {
				tmp := input[0:0]
				for _, user := range input {
					if matchBool(user, key, v) {
						tmp = append(tmp, user)
					}
				}
				input = tmp
			} else {
				return nil, fmt.Errorf("Invalid filter for \"%s\", expected bool got %v(%v) instead.", key, reflect.TypeOf(val), val)
			}
		}
	}
	return input, nil
}

// Downloads a file to a specific path
func (s KWSession) FileDownload(file *KiteObject) (ReadSeekCloser, error) {
	if file == nil {
		return nil, fmt.Errorf("nil file object provided.")
	}

	req, err := s.NewRequest("GET", SetPath("/rest/files/%d/content", file.ID), 7)
	if err != nil {
		return nil, err
	}

	return TransferMonitor(file.Name, file.Size, RightToLeft, s.Download(req)), nil
}