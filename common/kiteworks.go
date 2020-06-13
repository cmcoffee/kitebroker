package common

import (
	. "github.com/cmcoffee/go-kwlib"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"io"
)

var ErrNotFound = errors.New("Requested item not found.")

// KiteFile/Folder/Attachment
type KiteObject struct {
	Type            string         `json:"type"`
	Status          string         `json:"status"`
	ID              int            `json:"id"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Created         string         `json:"created"`
	Modified        string         `json:"modified"`
	ClientCreated   string         `json:"clientCreated"`
	ClientModified  string         `json:"clientModified"`
	Deleted         bool           `json:"deleted"`
	PermDeleted     bool           `json:"permDeleted"`
	Expire          interface{}    `json:"expire"`
	Path            string         `json:"path"`
	ParentID        int            `json:"parentId"`
	UserID          int            `json:"userId"`
	Permalink       string         `json:"permalink"`
	Locked          int            `json:"locked"`
	Fingerprint     string         `json:"fingerprint"`
	Size            int64          `json:"size"`
	Mime            string         `json:"mime"`
	Quarantined     bool           `json:"quarantined"`
	DLPLocked       bool           `json:"dlpLocked"`
	FileLifetime    int            `json:"fileLifetime"`
	MailID          int            `json:"mail_id"`
	Links           []KiteLinks    `json:"links"`
	CurrentUserRole KitePermission `json:"currentUserRole"`
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

// Find item in folder, using folder path, if folder_id > 0, start search there.
func (s Session) Find(folder_id int, path string, params ...interface{}) (result KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}

	folder_path := SplitPath(path)

	if len(folder_path) == 0 {
		folder_path = append(folder_path, path)
	}

	var current []KiteObject

	if folder_id <= 0 {
		current, err = s.TopFolders(params)
	} else {
		current, err = s.FolderContents(folder_id, params)
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
					current, err = s.FolderContents(c.ID, params)
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

/*
// Drills down specific folder and returns all results.
func (s Session) CrawlFolder(folder_id int, params...interface{}) (results []KiteObject, err error) {
	if len(params) == 0 {

	}
}*/

// Get list of all top folders
func (s Session) TopFolders(params ...interface{}) (folders []KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}

	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   "/rest/folders/top",
		Output: &folders,
		Params: SetParams(params, Query{"with": "(path)"}),
	}, -1, 1000)
	return
}

// Returns all items with listed folder_id.
func (s Session) FolderContents(folder_id int, params ...interface{}) (children []KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%d/children", folder_id),
		Output: &children,
		Params: SetParams(params, Query{"with": "(path)"}),
	}, -1, 1000)

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
func (s Session) MyUser() (user KiteUser, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   "/rest/users/me",
		Output: &user,
	})
	return
}

// Get total count of users.
func (s Session) GetUserCount(params ...interface{}) (users int, err error) {
	var user []struct{}
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/admin/users"),
		Params: SetParams(params),
		Output: &user}, -1, 1000)
	return len(user), err
}

// Get Users
type GetUsers struct {
	offset  int
	filter  Query
	params  []interface{}
	session *Session
}

// Admin EAPI endpoint to pull all users matching parameters.
func (s Session) GetUsers(params ...interface{}) *GetUsers {
	var T GetUsers
	T.filter = make(Query)
	T.offset = 0

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
	for {
		var raw_users []KiteUser
		err = T.session.DataCall(APIRequest{
			Method: "GET",
			Path:   SetPath("/rest/admin/users"),
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
func (s Session) FileDownload(file *KiteObject) (io.ReadSeeker, error) {
	if file == nil {
		return nil, fmt.Errorf("nil file object provided.")
	}

	req, err := s.NewRequest("GET", SetPath("/rest/files/%d/content", file.ID), 7)
	if err != nil {
		return nil, err
	}

	return TransferMonitor(file.Name, file.Size, RightToLeft, s.Download(req)), nil
}
