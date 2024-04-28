package core

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"time"
)

var ErrNotFound = errors.New("Requested item not found.")

type FileInfo interface {
	Name() string
	Size() int64
	ModTime() time.Time
}

type KiteMember struct {
	ID     string         `json:"objectId"`
	RoleID int            `json:"roleId"`
	User   KiteUser       `json:"user"`
	Role   KitePermission `json:"role"`
}

// KiteFile/Folder/Attachment
type KiteObject struct {
	Type                  string         `json:"type,omitempty"`
	Status                string         `json:"status,omitempty"`
	ID                    string         `json:"id,omitempty"`
	Name                  string         `json:"name,omitempty"`
	Description           string         `json:"description,omitempty"`
	Created               string         `json:"created,omitempty"`
	Modified              string         `json:"modified,omitempty"`
	ClientCreated         string         `json:"clientCreated,omitempty"`
	ClientModified        string         `json:"clientModified,omitempty"`
	Deleted               bool           `json:"deleted,omitempty"`
	PermDeleted           bool           `json:"permDeleted,omitempty"`
	Expire                interface{}    `json:"expire,omitempty"`
	Path                  string         `json:"path,omitempty"`
	ParentID              string         `json:"parentId,omitempty"`
	UserID                int            `json:"userId,omitempty"`
	Permalink             string         `json:"permalink,omitempty"`
	Secure                bool           `json:"secure,omitempty"`
	Fingerprint           string         `json:"fingerprint,omitempty"`
	ProfileID             int            `json:"typeID,omitempty"`
	Size                  int64          `json:"size,omitempty"`
	Mime                  string         `json:"mime,omitempty"`
	AVStatus              string         `json:"avStatus,omitempty"`
	DLPStatus             string         `json:"dlpStatus,omitempty"`
	AdminQuarantineStatus string         `json:"adminQuarantineStatus,omitempty"`
	Quarantined           bool           `json:"quarantined,omitempty"`
	DLPLocked             bool           `json:"dlpLocked,omitempty"`
	FileLifetime          int            `json:"fileLifetime,omitempty"`
	MailID                int            `json:"mail_id,omitempty"`
	Links                 []KiteLinks    `json:"links,omitempty"`
	IsShared              bool           `json:"isShared,omitempty"`
	IsLocked              interface{}    `json:"locked,omitempty"`
	CurrentUserRole       KitePermission `json:"currentUserRole,omitempty"`
}

func (K *KiteObject) Locked() bool {
	if x, ok := K.IsLocked.(bool); ok {
		return x
	}
	if x, ok := K.IsLocked.(int); ok {
		switch x {
		case 0:
			return false
		default:
			return true
		}
	}
	return false
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
	ID           string `json:"id"`
	URL          string `json:"href"`
}

// Permission information
type KitePermission struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	//Rank       int    `json:"rank"`
	Modifiable bool `json:"modifiable"`
	Disabled   bool `json:"disabled"`
}

func (s KWSession) FolderRoles() (result []KitePermission, err error) {
	return result, s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/roles"),
		Output: &result,
	}, -1, 1000)
}

type kw_rest_folder struct {
	folder_id string
	*KWSession
}

type PubSub struct {
	Queue string `json:"queueName"`
	Token string `json:"jwtToken"`
	URIS  []string
	*KWSession
}

func (s kw_rest_folder) Sub(sub_folders bool, regex string) (result PubSub, err error) {
	params := PostJSON{"file": PostJSON{"subfolders": sub_folders, "folderId": s.folder_id}}

	if regex != NONE {
		params["folderRegex"] = regex
	}

	err = s.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/pubsub/subscribe",
		Params: SetParams(params),
		Output: &result,
	})

	if err != nil {
		return
	}

	var URI struct {
		URIS []string `json:"uri"`
	}

	err = s.Call(APIRequest{
		Method: "GET",
		Path:   "/rest/pubsub/config",
		Output: &URI,
	})

	if err != nil {
		return
	}

	result.URIS = URI.URIS
	result.KWSession = s.KWSession

	return

}

func (s PubSub) Listen() (err error) {
	_ = url.URL{Scheme: "ws", Host: s.APIClient.Server, Path: fmt.Sprintf("/%s/%s", s.URIS[0], s.Queue)}

	return
}

func (s PubSub) UnSub() (err error) {
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/pubsub/unsubscribe",
		Params: SetParams(PostJSON{"queueName": s.Queue}),
	})

	return
}

func (s KWSession) Folder(folder_id string) kw_rest_folder {
	return kw_rest_folder{
		folder_id,
		&s,
	}
}

func (s kw_rest_folder) Files(params ...interface{}) (children []KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%s/files", s.folder_id),
		Output: &children,
		Params: SetParams(params),
	}, -1, 1000)

	return
}

func (s kw_rest_folder) Info(params ...interface{}) (output KiteObject, err error) {
	if params == nil {
		params = SetParams(Query{"deleted": false})
	}
	if IsBlank(s.folder_id) || s.folder_id == "0" {
		return
	}
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%s", s.folder_id),
		Params: SetParams(params, Query{"mode": "full", "with": "(currentUserRole, fileLifetime, path)"}),
		Output: &output,
	})
	return
}

type KiteActivity struct {
	Successful int    `json:"successful"`
	Created    string `json:"created"`
	Message    string `json:"message"`
	Event      string `json:"event"`
	ID         int    `json:"id"`
	Data       struct {
		Comment struct {
			Content string `json:"content"`
		} `json:"comment"`
		File struct {
			Uploader struct {
				Name string `json:"name"`
				ID   string `json:"guid"`
			} `json:"file_uploader"`
		} `json:"file"`
		Successful bool `json:"succcessful`
	} `json:"data"`
	User struct {
		UserID      string `json:"userId"`
		ProfileIcon string `json:"profileIcon"`
		Name        string `json:"name"`
	} `json:"user"`
}

func (s kw_rest_folder) Activities(params ...interface{}) (result []KiteActivity, err error) {
	err = s.DataCall(APIRequest{
		Version: 27,
		Method:  "GET",
		Path:    SetPath("/rest/folders/%s/activities", s.folder_id),
		Output:  &result,
		Params:  SetParams(params),
	}, -1, 1000)
	return
}

func (s kw_rest_folder) NewFolder(name string, params ...interface{}) (output KiteObject, err error) {
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/folders/%s/folders", s.folder_id),
		Params: SetParams(PostJSON{"name": name}, Query{"returnEntity": true}, params),
		Output: &output,
	})
	return
}

func (s kw_rest_folder) Delete(params ...interface{}) (err error) {
	err = s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/folders/%s", s.folder_id),
		Params: SetParams(params),
	})
	return
}

func (s kw_rest_folder) Members(params ...interface{}) (result []KiteMember, err error) {
	return result, s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%s/members", s.folder_id),
		Output: &result,
		Params: SetParams(params, Query{"with": "(user,role)"}),
	}, -1, 1000)
}

func (s kw_rest_folder) AddUsersToFolder(emails []string, role_id int, notify bool, notify_files_added bool, params ...interface{}) (err error) {
	params = SetParams(PostJSON{"notify": notify, "notifyFileAdded": notify_files_added, "emails": emails, "roleId": role_id}, Query{"updateIfExists": true, "partialSuccess": true}, params)
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/folders/%s/members", s.folder_id),
		Params: params,
	})
	return
}

func (s kw_rest_folder) ResolvePath(path string) (result KiteObject, err error) {
	folder_path := SplitPath(path)

	current_id := s.folder_id

	var current KiteObject

	for _, f := range folder_path {
		current, err = s.Folder(current_id).Find(f)
		if err != nil {
			if err == ErrNotFound {
				current, err = s.Folder(current_id).NewFolder(f)
				if err != nil {
					return
				}
				current_id = current.ID
			}
		}
		current_id = current.ID
	}
	result = current
	return
}

// Find item in folder, using folder path, if folder_id > 0, start search there.
func (s kw_rest_folder) Find(path string, params ...interface{}) (result KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}

	folder_path := SplitPath(path)

	var current []KiteObject

	if !IsBlank(s.folder_id) && s.folder_id != "0" {
		current, err = s.Folder(s.folder_id).Contents(params)
	} else {
		current, err = s.TopFolders(params)
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
				result = c
				if i < folder_len && c.Type == "d" {
					current, err = s.Folder(c.ID).Contents(params)
					if err != nil {
						return
					}
					found = true
					break
				} else if i == folder_len {
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

func (s kw_rest_admin) Register(email string) (err error) {
	return s.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/users/register",
		Params: SetParams(PostJSON{"email": email, "password": "NewAccount#123"}),
	})
}

func (s kw_rest_admin) ActivateUser(userid int) (err error) {
	err = s.Call(APIRequest{
		Method: "PUT",
		Path:   SetPath("/rest/admin/users/%d", userid),
		Params: SetParams(PostJSON{"suspended": false, "verified": true, "deactivated": false}),
	})
	return
}

func (s kw_rest_admin) DeactivateUser(userid int) (err error) {
	err = s.Call(APIRequest{
		Method: "PUT",
		Path:   SetPath("/rest/admin/users/%d", userid),
		Params: SetParams(PostJSON{"suspended": true, "verified": true, "deactivated": false}),
	})
	return
}

// Creates a new user on the system.
func (s kw_rest_admin) NewUser(user_email string, type_id int, verified, notify bool) (user *KiteUser, err error) {
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/users",
		Params: SetParams(PostJSON{"email": user_email, "userTypeId": type_id, "verified": verified, "sendNotification": notify}, Query{"returnEntity": true}),
		Output: &user,
	})

	return user, err
}

func (s kw_rest_admin) FindUser(user_email string, params ...interface{}) (user *KiteUser, err error) {
	user_getter, err := s.Admin().Users([]string{user_email}, 0)
	if err != nil {
		return nil, err
	}
	users, err := user_getter.Next()
	if err != nil {
		return nil, err
	}
	if len(users) > 0 {
		return &users[0], nil
	}
	return nil, fmt.Errorf("Unable to find user %s.", user_email)
}

func (s kw_rest_admin) GetAllUsers(params ...interface{}) (emails []string, err error) {
	var users []struct {
		Email string `json:"email"`
	}
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   "/rest/admin/users",
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

func (s kw_rest_admin) DeleteUser(user KiteUser, params ...interface{}) (err error) {
	return s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/admin/users/%v", user.ID),
		Params: SetParams(params),
	})
}

func (s kw_rest_admin) FindProfile(name string) (profile KWProfile, err error) {
	profiles, err := s.Profiles()
	if err != nil {
		return profile, err
	}
	for _, v := range profiles {
		if strings.ToLower(name) == strings.ToLower(v.Name) {
			return v, nil
		}
	}
	err = fmt.Errorf("Requested profile not found: %s", name)
	return
}

func (s kw_rest_admin) Profiles(params ...interface{}) (profiles []KWProfile, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   "/rest/admin/profiles",
		Params: SetParams(params),
		Output: &profiles,
	}, -1, 1000)
	return
}

type kw_rest_file struct {
	file_id string
	*KWSession
}

func (s kw_rest_file) Unlock() (err error) {
	err = s.Call(APIRequest{
		Version: 27,
		Method:  "PATCH",
		Path:    SetPath("/rest/files/%s/actions/unlock", s.file_id),
		Output:  nil,
		Params:  nil,
	})
	return
}

func (s KWSession) File(file_id string) kw_rest_file {
	return kw_rest_file{file_id, &s}
}

func (s kw_rest_file) Activities(params ...interface{}) (result []KiteActivity, err error) {
	err = s.DataCall(APIRequest{
		Version: 27,
		Method:  "GET",
		Path:    SetPath("/rest/files/%s/activities", s.file_id),
		Output:  &result,
		Params:  SetParams(params),
	}, -1, 1000)
	return
}

func (s kw_rest_file) Info(params ...interface{}) (result KiteObject, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/files/%s", s.file_id),
		Params: SetParams(params),
		Output: &result,
	})
	return
}

func (s kw_rest_file) Delete(params ...interface{}) (err error) {
	err = s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/files/%s", s.file_id),
		Params: SetParams(params),
	})
	return
}

func (s kw_rest_file) PermDelete() (err error) {
	err = s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/files/%s/actions/permanent", s.file_id),
	})
	return
}

type KiteSource struct {
	URL          string `json:"sourceURL"`
	Description  string `json:"description"`
	ID           string `json:"id"`
	SourceTypeID int    `json"sourceTypeId"`
	PinnedTime   string `json:"pinnedTime"`
	Name         string `json:"name"`
	SourceByUser bool   `json:"sourceByUser"`
	Pinned       bool   `json:"pinned"`
}

type kw_source struct {
	source_id string
	*KWSession
}

func (s KWSession) Source(source_id string) kw_source {
	return kw_source{
		source_id,
		&s,
	}
}

func (s kw_source) Folders(params ...interface{}) (folders []KiteObject, err error) {
	err = s.DataCall(APIRequest{
		Version: 26,
		Method:  "GET",
		Path:    SetPath("/rest/sources/%s/folders", s.source_id),
		Output:  &folders,
		Params:  SetParams(params),
	}, -1, 1000)
	return
}

func (s KWSession) Sources(params ...interface{}) (sources []KiteSource, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   "/rest/sources",
		Output: &sources,
		Params: SetParams(params),
	}, -1, 1000)
	return
}

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

// Get list of all top owned folders
func (s KWSession) OwnedFolders(params ...interface{}) (folders []KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}

	var top_folders []KiteObject

	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   "/rest/folders/top",
		Output: &top_folders,
		Params: SetParams(params, Query{"with": "(path,currentUserRole)"}),
	}, -1, 1000)

	for _, folder := range top_folders {
		if folder.CurrentUserRole.ID != 5 {
			continue
		}
		folders = append(folders, folder)
	}

	return
}

// Returns all items with listed folder_id.
func (s kw_rest_folder) Contents(params ...interface{}) (children []KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}

	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%s/children", s.folder_id),
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
		Path:   SetPath("/rest/folders/%s/folders", s.folder_id),
		Output: &children,
		Params: SetParams(params, Query{"with": "(path,currentUserRole)"}),
	}, -1, 1000)

	return
}

func (s kw_rest_folder) Recover(params ...interface{}) (err error) {
	return s.Call(APIRequest{
		Method: "PATCH",
		Path:   SetPath("/rest/folders/%s/actions/recover", s.folder_id),
	})
}

func (s kw_rest_file) Recover(params ...interface{}) (err error) {
	return s.Call(APIRequest{
		Method: "PATCH",
		Path:   SetPath("/rest/files/%s/actions/recover", s.file_id),
	})
}

func (s kw_rest_folder) MoveToFolder(folder_id string) (err error) {
	return s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/folders/%s/actions/move", s.folder_id),
		Params: SetParams(PostJSON{"destinationFolderId": folder_id}),
	})
}

// Kiteworks User Data
type KiteUser struct {
	ID          int    `json:"id"`
	Active      bool   `json:"active"`
	Deactivated bool   `json:"deactivated"`
	Suspended   bool   `json:"suspended"`
	Deleted     bool   `json:"deleted"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	MyDirID     string `json:"mydirId"`
	BaseDirID   string `json:"basedirId"`
	SyncDirID   string `json:"syncdirId"`
	UserTypeID  int    `json:"userTypeId"`
	Verified    bool   `json:"verified"`
	Internal    bool   `json:"internal"`
	ProfileID   int    `json:"userTypeID"`
	*KWSession
}

// Returns if account can be used or not for API calls.
func (K KiteUser) IsActive() bool {
	if K.Active && !K.Deactivated && !K.Suspended && !K.Deleted {
		return true
	}
	return false
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

type KiteQuota struct {
	SendUsed      int `json:"send_quota_used"`
	FolderUsed    int `json:"folder_quota_users"`
	FolderAllowed int `json:"folder_quota_allowed"`
	SendAllowed   int `json:"send_quota_allowed"`
}

func (s KWSession) MyQuota() (quota KiteQuota, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   "/rest/users/me/quota",
		Output: &quota,
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
	offset       int
	filter       Query
	profile_id   int
	emails       []string
	email_map    map[string]interface{}
	params       []interface{}
	orig_params  []interface{}
	session      *kw_rest_admin
	user_total   int
	failed_users int
	show_errors  bool
	completed    bool
}

// Returns total failed users.
func (T *GetUsers) Failed() int {
	return T.failed_users
}

// Returns total users estimate.
func (T *GetUsers) Total() int {
	return T.user_total
}

// Admin EAPI endpoint to pull all users matching parameters.
func (s kw_rest_admin) Users(emails []string, profile_id int, params ...interface{}) (*GetUsers, error) {
	var T GetUsers
	T.filter = make(Query)
	T.offset = 0
	T.profile_id = profile_id
	T.orig_params = SetParams(params)

	if len(emails) > 0 {
		T.email_map = make(map[string]interface{})
		for _, v := range emails {
			T.email_map[strings.ToLower(v)] = struct{}{}
		}
		// If emails are under 100, we'll search directly for the emails instead of filtering them out of the global user list.
		if len(emails) <= 100 || profile_id > 0 {
			T.emails = emails
		}
		T.show_errors = true
		T.user_total = len(T.email_map)
	}

	// First extract the query from request.
	params = SetParams(params)

	var query Query

	var tmp []interface{}
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

	// Perform profile filtering.
	if T.profile_id > 0 {
		profile_emails, err := T.session.FindProfileUsers(T.profile_id, T.orig_params)
		if err != nil {
			if IsAPIError(err, "ERR_ACCESS_USER") {
				return nil, fmt.Errorf("User list failure for profile id %d: please verify profile id exists.", T.profile_id)
			} else {
				return nil, fmt.Errorf("User list failure for profile id %d: %v", T.profile_id, err)
			}
		}
		if len(T.email_map) > 0 {
			upmap := make(map[string]struct{})
			for _, email := range profile_emails {
				upmap[strings.ToLower(email)] = struct{}{}
			}
			for e := range T.email_map {
				if _, ok := upmap[e]; !ok {
					if len(T.emails) > 0 {
						Err("%s does not meet required profile id [%d], skipping user..", e, T.profile_id)
					}
					delete(T.email_map, e)
				}
			}
		} else {
			T.email_map = make(map[string]interface{})
			for _, email := range profile_emails {
				T.email_map[strings.ToLower(email)] = struct{}{}
			}
		}
		T.emails = nil
		if len(T.email_map) <= 100 {
			for e := range T.email_map {
				T.emails = append(T.emails, e)
			}
		}
		T.profile_id = 0
		if len(T.email_map) == 0 {
			T.completed = true
			return &T, nil
		}
		T.user_total = len(T.email_map)
	}

	if T.user_total == 0 {
		T.user_total, _ = T.session.UserCount(nil, T.orig_params)
	}

	return &T, nil
}

// Return a set of users to process.
func (T *GetUsers) Next() (users []KiteUser, err error) {
	if T.completed {
		return
	}

	if len(T.emails) > 0 {
		T.completed = true
		return T.findEmails()
	}

	for {
		var raw_users []KiteUser
		err = T.session.DataCall(APIRequest{
			Method: "GET",
			Path:   "/rest/admin/users",
			Params: T.params,
			Output: &raw_users}, T.offset, 1000)
		if err != nil {
			return nil, fmt.Errorf("User list failure: %v", err)
		}
		if len(raw_users) == 0 {
			T.completed = true
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

// Individual Email Lookup
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
		if T.show_errors {
			Err("%s: User not found, or did not meet specified criteria.", u)
		} else {
			Debug("%s: User not found, or did not meet specified criteria.", u)
		}
		T.failed_users++
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
					if T.email_map != nil {
						if _, ok := T.email_map[strings.ToLower(user.Email)]; !ok {
							continue
						}
					}
					if matchBool(user, key, v) {
						tmp = append(tmp, user)
					}
				}
				input = tmp
			} else {
				return nil, fmt.Errorf("User list failure: invalid filter for \"%s\", expected bool got %v(%v) instead.", key, reflect.TypeOf(val), val)
			}
		}
	}
	return input, nil
}

type KiteRoles struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Rank  int    `json:"rank"`
	Type  string `json:"type"`
	Links string `json:"links"`
}

func (s KWSession) Roles() (output []KiteRoles, err error) {
	var Roles struct {
		Out []KiteRoles `json:"data"`
	}

	err = s.Call(APIRequest{
		Method: "GET",
		Path:   "/rest/roles",
		Output: &Roles,
	})
	output = Roles.Out
	return
}

/*
type kw_profile struct {
	profile_id int
	*KWSession
}

func (K KWSession) Profiles(profile_id int) kw_profile {
	return kw_profile{
		profile_id,
		&K,
	}

}

func (K kw_profile) Get() (profile KWProfile, err error) {
	err = K.Call(APIRequest{
		Version: 20,
		Path:    SetPath("/rest/profiles/%d", K.profile_id),
		Output:  &profile,
	})
	return
}*/

type folderCrawler struct {
	processor      func(*KWSession, *KiteObject) error
	folder_limiter LimitGroup
	file_chan      chan *KiteObject
	all_stop       int32
}

type abortError struct {
	err error
}

func AbortError(err error) abortError {
	return abortError{
		err: err,
	}
}

func (e abortError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return ""
}

func abortCheck(err error) (error, bool) {
	if err == nil {
		return nil, false
	}
	if e, ok := err.(abortError); !ok {
		return err, false
	} else {
		return e.err, true
	}
}

func (s *KWSession) FolderCrawler(processor func(*KWSession, *KiteObject) error, folders ...KiteObject) {
	crawler := new(folderCrawler)
	crawler.folder_limiter = NewLimitGroup(50)
	file_limiter := NewLimitGroup(50)
	crawler.processor = processor
	crawler.file_chan = make(chan *KiteObject, 1000)

	file_limiter.Add(1)
	go func(user *KWSession) {
		defer file_limiter.Done()
		for {
			m := <-crawler.file_chan
			if m == nil {
				return
			}
			if atomic.LoadInt32(&crawler.all_stop) > 0 || crawler.processor == nil {
				continue
			}
			if file_limiter.Try() {
				go func(user *KWSession, object *KiteObject) {
					defer file_limiter.Done()
					err, abort := abortCheck(processor(user, object))
					if err != nil {
						Err("%s - %s: %v", user.Username, object.Name, err)
					}
					if abort {
						atomic.AddInt32(&crawler.all_stop, 1)
					}
				}(s, m)
			} else {
				err, abort := abortCheck(processor(user, m))
				if err != nil {
					Err("%s - %s: %v", user.Username, m.Name, err)
				}
				if abort {
					atomic.AddInt32(&crawler.all_stop, 1)
				}
			}

		}
	}(s)

	for _, f := range folders {
		crawler.folder_limiter.Add(1)
		go func(user *KWSession, folder KiteObject) {
			defer crawler.folder_limiter.Done()
			crawler.process(s, &folder)
		}(s, f)
	}
	crawler.folder_limiter.Wait()
	crawler.file_chan <- nil
	file_limiter.Wait()
	return
}

func (F *folderCrawler) process(user *KWSession, folder *KiteObject) {
	// Folder is already complete, return to caller.
	var folders []*KiteObject

	folders = append(folders, folder)

	var n int
	var next []*KiteObject

	if folder != nil && folder.Type != "d" {
		Err("%s is not a folder.", folder.Name)
		return
	}

	if folder == nil {
		Err("Cannot process 'nil' folder.")
		return
	}

	for {
		if atomic.LoadInt32(&F.all_stop) > 0 {
			return
		}
		if len(folders) < n+1 {
			folders = folders[0:0]
			if len(next) > 0 {
				for i, o := range next {
					if F.folder_limiter.Try() {
						go func(user *KWSession, folder *KiteObject) {
							defer F.folder_limiter.Done()
							F.process(user, folder)
						}(user, o)
					} else {
						folders = append(folders, next[i])
					}
				}
				next = next[0:0]
				n = 0
				if len(folders) == 0 {
					break
				}
			} else {
				break
			}
		}
		if folders[n].Type == "d" {
			if F.processor != nil {
				if atomic.LoadInt32(&F.all_stop) > 0 {
					return
				}
				if err := F.processor(user, folders[n]); err != nil {
					err, abort := abortCheck(err)
					if err != nil {
						Err("%s - %s: %v", user.Username, folders[n].Path, err)
					}
					if abort {
						atomic.AddInt32(&F.all_stop, 1)
						return
					}
				}
			}
			childs, err := user.Folder(folders[n].ID).Contents()
			if err == nil {
				for i := 0; i < len(childs); i++ {
					switch childs[i].Type {
					case "d":
						next = append(next, &childs[i])
					default:
						if atomic.LoadInt32(&F.all_stop) > 0 {
							return
						}
						F.file_chan <- &childs[i]
					}
				}
			} else {
				Err("%s - %s: %v", user.Username, folders[n].Path, err)
			}
		}
		n++
	}
	return
}

type kw_profile struct {
	profile_map map[int]KWProfile
}

func (K kw_profile) Find(name string) (profile *KWProfile, err error) {
	for _, v := range K.profile_map {
		if strings.ToLower(name) == strings.ToLower(v.Name) {
			return &v, nil
		}
	}

	return nil, fmt.Errorf("No profile with name '%s' found.", name)
}

func (K kw_profile) Get(id int) (profile *KWProfile, err error) {
	if v, ok := K.profile_map[id]; ok {
		return &v, nil
	} else {
		return nil, fmt.Errorf("No profile with id '%d' found.", id)
	}
}

type KWProfile struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Features struct {
		AllowSFTP    bool  `json:"allowSftp"`
		MaxStorage   int64 `json:"maxStorage"`
		SendExternal bool  `json:"sendExternal`
		FolderCreate int   `json:"folderCreate"`
		FileTime     int   `json:"fileLifetime"`
		FolderTime   int   `json:"folderExpirationLimit"`
	} `json:"features"`
}

func (K KWSession) Profiles() (output map[int]KWProfile, err error) {
	var profiles []KWProfile
	err = K.DataCall(APIRequest{
		Version: 20,
		Method:  "GET",
		Path:    SetPath("/rest/profiles"),
		Output:  &profiles,
	}, -1, 1000)
	if err != nil {
		return nil, err
	}
	output = make(map[int]KWProfile)
	for _, v := range profiles {
		output[v.ID] = v
	}
	return
}

type dli_admin struct {
	*KWSession
}

func (K KWSession) DLIAdmin() dli_admin {
	return dli_admin{
		&K,
	}
}

func (K dli_admin) ActivityCount(input KiteUser, number_of_days_ago int) (activities int, err error) {
	var activity []struct {
		Created string `json:"created"`
	}

	err = K.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/dli/users/%v/activities", input.ID),
		Params: SetParams(Query{"noDayBack": number_of_days_ago}),
		Output: &activity,
	}, -1, 1000)
	if err != nil {
		return 1, err
	}

	return len(activity), err
}

func (K dli_admin) CheckForActivity(user KiteUser, number_of_days int) (found bool, err error) {
	end_date := time.Now().UTC()
	start_date := time.Now().UTC().Add((time.Hour * 24) * time.Duration(1) * -1)

	var report []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}

	err = K.DataCall(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/dli/exports/users/%v", user.ID),
		Params: SetParams(PostJSON{"startDate": WriteKWTime(start_date), "endDate": WriteKWTime(end_date), "types": []string{"activities"}}, Query{"returnEntity": true}),
		Output: &report,
	}, -1, 1000)

	if err != nil {
		return false, err
	}

	if len(report) == 0 {
		return false, fmt.Errorf("No report found.")
	}

	path := SetPath("/rest/dli/exports/%s", report[0].ID)

	defer K.Call(APIRequest{
		Method: "DELETE",
		Path:   path,
	})

	status := report[0].Status

	for status == "inprocess" {
		time.Sleep(time.Second)
		var r_status struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}

		err = K.Call(APIRequest{
			Method: "GET",
			Path:   path,
			Output: &r_status,
		})

		if err != nil {
			return false, err
		}

		status = r_status.Status
	}

	if status == "nodata" {
		return false, nil
	} else {
		return true, nil
	}

	return
}
