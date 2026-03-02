package quatrix

import (
	"encoding/json"
	"fmt"
	"sync/atomic"

	. "github.com/cmcoffee/kitebroker/core"
)

// Quatrix Permissions
const (
	NO_PERMISSIONS   = 0
	QP_UPLOAD        = 1
	QP_DOWNLOAD      = 2
	QP_CREATE        = 4
	QP_DELETE        = 8
	QP_RENAME        = 16
	QP_SHARE         = 32
	QP_LINK          = 128
	QP_COMMENT       = 256
	QP_PREVIEW       = 512
	QP_MANAGE        = 1024
	QP_LIST          = 2048
	QP_FULL          = QP_UPLOAD | QP_DOWNLOAD | QP_CREATE | QP_DELETE | QP_RENAME | QP_SHARE | QP_LINK | QP_COMMENT | QP_PREVIEW | QP_MANAGE | QP_LIST
	OWNER            = QP_FULL
	QP_LIST_AND_READ = QP_LIST | QP_DOWNLOAD | QP_COMMENT | QP_PREVIEW
	READ_AND_WRITE   = QP_LIST | QP_UPLOAD | QP_DOWNLOAD | QP_CREATE | QP_DELETE | QP_RENAME | QP_COMMENT | QP_PREVIEW
)

// ProjectFolders is a method of QSession which fetches all the project folders using the Quatrix API and returns them along with any error that occurred during the API call.
func (Q *QSession) ProjectFolders() (projects []ProjectFolder, err error) {
	err = Q.Call(APIRequest{
		Path:   "/api/1.0/project-folder",
		Method: "GET",
		Output: &projects,
	})
	return
}

// AddUserToProject adds a user to the specified project(s) with given permissions and notification settings.
// It takes a user, permissions, notification flag, and a variable number of project folders as input.
func (Q *QSession) AddUserToProject(user Userdata, permissions int64, notify bool, project ...ProjectFolder) (err error) {
	type pjs struct {
		ProjectID  string `json:"project_id"`
		Notify     bool   `json:"notify"`
		Operations int64  `json:"operations"`
	}
	var params []pjs
	for _, p := range project {
		params = append(params, pjs{ProjectID: p.ID, Notify: notify, Operations: permissions})
	}

	err = Q.Call(APIRequest{
		Path:   "/api/1.0/project-folder/set-users",
		Method: "POST",
		Params: SetParams(PostJSON{"users": []PostJSON{0: {"params": params, "user_id": user.ID}}}),
	})
	return
}

// ProjectFolder represents a project folder in the Quatrix API.
type ProjectFolder struct {
	Owner struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		ID    string `json:"id"`
	} `json:"owner"`
	ID        string `json:"project_id"`
	Created   int64  `json:"ctime"`
	ModTime   int64  `json:"mtime"`
	Name      string `json:"name"`
	FileID    string `json:"file_id"`
	Expires   int64  `json:"expires,omitempty"`
	Activates any    `json:"activates,omitempty"`
}

// Userdata represents a user in the Quatrix API. It contains an ID which is a unique identifier for the user.
type Userdata struct {
	ID          string `json:"id"`
	HomeID      string `json:"home_id"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	UniqueLogin any    `json:"unique_login"`
	Status      string `json:"status"`
}

// QObject is a struct that represents an object in the Quatrix API system.
type QObject struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	Type        string    `json:"type"`
	RawParentID string    `json:"parent_id"`
	Created     int64     `json:"created,omitempty"`
	ModTime     int64     `json:"modified,omitempty"`
	ModTimeMS   float64   `json:"modified_ms,omitempty"`
	GID         int64     `json:"gid"`
	UID         int64     `json:"uid"`
	Operations  int64     `json:"operations,omitempty"`
	RawMetadata any       `json:"metadata,omitempty"`
	Content     []QObject `json:"content,omitempty"`
	Owner       struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		ID    string `json:"id"`
	} `json:"owner,omitempty"`
	*QSession
}

// IsSystemFolder returns whether or not a QObject is a system folder.
func (Q QObject) IsSystemFolder() bool {
	if Q.RawMetadata != nil {
		if v, ok := Q.RawMetadata.(map[string]any)["is_system"]; ok {
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return false
}

// SubType returns the subtype of a QObject from its RawMetadata field if it exists. If not, it returns NONE (an empty string).
func (Q QObject) SubType() string {
	if Q.RawMetadata != nil {
		if v, ok := Q.RawMetadata.(map[string]any)["subtype"]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return NONE
}

// Users fetches a list of all users in the Quatrix API. It returns a slice of Userdata objects and an error if one occurs during the API call.
// Each Userdata object contains information about a user, including their ID, home ID, name, email, SFTP login status, and status.
func (Q *QSession) Users() (users []Userdata, err error) {
	err = Q.Call(APIRequest{
		Path:   "/api/1.0/user",
		Method: "GET",
		Output: &users,
	})
	return
}

// Permissions method fetches permissions for a file in the Quatrix API system.
// The function returns the retrieved permissions and any error that occurred during the request.
func (Q QObject) Permissions() (permissions QPerms, err error) {
	err = Q.Call(APIRequest{
		Path:   "/api/1.0/file/permissions/" + Q.ID,
		Method: "GET",
		Output: &permissions,
	})
	return
}

// Info fetches information about a QObject from the Quatrix API. The function returns the retrieved info and any error that occurred during the request.
// The returned type is QInfo, which includes details like ID, name, size, and other metadata for the file.
func (Q QObject) Info() (info QInfo, err error) {
	err = Q.Call(APIRequest{
		Path:   "/api/1.0/file/info/" + Q.ID,
		Method: "GET",
		Output: &info,
	})
	return
}

// QPerms is a struct that represents permissions for a file in the Quatrix API. It includes details like ID, name, and users associated with the file permissions.
type QPerms struct {
	ID    string `json:"file_id"`
	Name  string `json:"name"`
	Users []struct {
		ID                  string `json:"id"`
		Name                string `json:"name"`
		Email               string `json:"email"`
		Operations          int64  `json:"operations"`
		InheritedOperations int64  `json:"default"`
	} `json:"users"`
}

// / QInfo is a struct that represents the information of a file object in Quatrix API.
// / It includes details like ID, created time, modified time, name, size and other relevant metadata for the file.
// / The `Owner` field contains a slice of structs representing the owner's name, id, and email address.
// / The `Paths` field is a slice of structs that include path information and source type.
type QInfo struct {
	ID              string `json:"id"`
	Created         int64  `json:"created"`
	Modified        int64  `json:"modified"`
	ContentModified int64  `json:"content_modified"`
	Name            string `json:"name"`
	Size            int64  `json:"size"`
	Type            string `json:"type"`
	Owner           []struct {
		Name  string `json:"name"`
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"file_owner"`
	Paths []struct {
		Path       string `json:"path"`
		SourceType string `json:"source_type,omitempty"`
	} `json:"paths"`
}

func (Q QInfo) HomePath() string {
	var home_path string
	for _, p := range Q.Paths {
		if p.SourceType == "H" {
			home_path = p.Path
			break
		}
	}
	return home_path
}

// RawInt64 converts any type to an int64. Handles both int64 and float64
// (the default numeric type from json.Unmarshal). Returns 0 if conversion fails.
func RawInt64(a any) int64 {
	switch v := a.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}

// RawString converts the given interface to a string if it is possible, otherwise returns an empty string.
func RawString(a any) string {
	if strVal, ok := a.(string); ok {
		return strVal
	}
	return ""
}

// ParentID returns the parent ID of the QObject. The parent ID is a string value retrieved from RawParentID property.
func (M QObject) ParentID() string {
	return RawString(M.RawParentID)
}

// QAPI is a struct that embeds an APIClient and provides methods for interacting with the Quatrix API.
type QAPI struct {
	*APIClient
}

// QSession represents a session with the Quatrix API. It holds information about the username and their profile data.
type QSession struct {
	Username string
	Profile  Userdata
	*QAPI
}

// qautrixError unmarshals a JSON-encoded Quatrix API error response body into an APIError struct.
// It extracts the error code and message from the provided byte slice (body),
// and if these are not empty, it registers them in the returned APIError struct.
func qautrixError(body []byte) (e APIError) {
	// Quatrix API Error
	type QuatrixErr struct {
		Code    int64  `json:"code"`
		Message string `json:"msg"`
		Details string `json:"details"`
	}

	var quatrix_error *QuatrixErr
	json.Unmarshal(body, &quatrix_error)

	if quatrix_error != nil && quatrix_error.Code != 0 {
		e.Register(fmt.Sprintf("QUATRIX_CODE_%d", quatrix_error.Code), quatrix_error.Message)
	}

	return
}

// Session retrieves the current user's session data from the API.
// It returns a QSession object containing the username and profile
// information, or an error if the API call fails.
func (Q *QAPI) Session() (*QSession, error) {
	var user_info Userdata

	err := Q.Call(APIRequest{
		Username: "quatrix_user",
		Path:     "/api/1.0/profile",
		Method:   "GET",
		Output:   &user_info,
	})
	if err != nil {
		return nil, err
	}
	Q.ErrorScanner = qautrixError
	return &QSession{Username: user_info.Email, Profile: user_info, QAPI: Q}, nil
}

// Call makes an API request with the given parameters, automatically
// setting the username and forwarding the request to the API client.
// It returns an error if the API call fails.
func (Q *QSession) Call(params APIRequest) error {
	params.Username = Q.Username
	return Q.APIClient.Call(params)
}

// File method is a part of QSession struct that takes a file_id string as an input parameter and returns a QObject named 'file' and an error if any occurred.
// This function fetches the metadata for a specific file using its ID from the Quatrix API.
func (Q QSession) File(file_id string) (file QObject, err error) {
	err = Q.Call(APIRequest{
		Path:   "/api/1.0/file/metadata/" + file_id,
		Method: "GET",
		Output: &file,
	})
	file.QSession = &Q
	return
}

// Download fetches a file's download link from the Quatrix API and downloads the file using the APIClient.WebDownload function.
// The method returns an object satisfying the ReadSeekCloser interface, which can be used to read and seek within the downloaded file.
// If any error occurs during this process, it is returned by the method.
func (Q QObject) Download() (ReadSeekCloser, error) {
	var link struct {
		ID string `json:"id"`
	}
	err := Q.QSession.Call(APIRequest{
		Path:   "/api/1.0/file/download-link",
		Method: "POST",
		Params: SetParams(PostJSON{"ids": []string{Q.ID}}),
		Output: &link,
	})
	if err != nil {
		return nil, err
	}
	req, err := Q.APIClient.NewRequest("GET", "/api/1.0/file/download/"+link.ID)
	if err != nil {
		return nil, err
	}
	err = Q.APIClient.SetToken(Q.QSession.Username, req)
	if err != nil {
		return nil, err
	}
	return Q.APIClient.WebDownload(req), nil
}

// folderCrawler represents a crawler that traverses through provided 'folders' and processes their contents.
// It maintains concurrency by limiting the number of operations to 50 using the LimitGroup package.
// If it encounters an error or needs to stop, it signals this via the all_stop variable.
type folderCrawler struct {
	processor      func(*QSession, *QObject) error
	folder_limiter LimitGroup
	file_chan      chan *QObject
	all_stop       int32
}

// abortError represents a custom error type used to convey the occurrence of an abort signal during a certain process or operation.
// It holds an underlying error for more detailed information if required.
type abortError struct {
	err error
}

// Error returns the error message if an underlying error exists.
func (e abortError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return ""
}

// abortCheck checks if an error is of type `abortError` and returns a boolean value indicating whether it was or not.
// If the error is not an `abortError`, the function simply returns the original error.
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

// FolderCrawler starts a new folder crawler to process files in the provided folders using the provided processor function.
// The processor function is called for each file that is found and is responsible for processing it.
// It also takes care of limiting the number of simultaneous file and folder operations to avoid overwhelming the system with too many requests at once.
func (K *QSession) FolderCrawler(processor func(*QSession, *QObject) error, folders ...*QObject) {
	crawler := new(folderCrawler)
	crawler.folder_limiter = NewLimitGroup(50)
	file_limiter := NewLimitGroup(50)
	crawler.processor = processor
	crawler.file_chan = make(chan *QObject, 1000)

	file_limiter.Add(1)
	go func(user *QSession) {
		defer file_limiter.Done()
		for m := range crawler.file_chan {
			if m.Type != "F" {
				continue
			}
			if atomic.LoadInt32(&crawler.all_stop) > 0 || crawler.processor == nil {
				continue
			}
			if file_limiter.Try() {
				go func(user *QSession, object *QObject) {
					defer file_limiter.Done()
					err, abort := abortCheck(processor(user, object))
					if err != nil {
						Err("%s - %s: %v", user.Username, object.Name, err)
					}
					if abort {
						atomic.StoreInt32(&crawler.all_stop, 1)
					}
				}(K, m)
			} else {
				err, abort := abortCheck(processor(user, m))
				if err != nil {
					Err("%s - %s: %v", user.Username, m.Name, err)
				}
				if abort {
					atomic.StoreInt32(&crawler.all_stop, 1)
				}
			}
		}
	}(K)

	for _, f := range folders {
		crawler.folder_limiter.Add(1)
		go func(user *QSession, folder *QObject) {
			defer crawler.folder_limiter.Done()
			crawler.process(K, folder)
		}(K, f)
	}
	crawler.folder_limiter.Wait()
	close(crawler.file_chan)
	file_limiter.Wait()
	return
}

// process is a method of the folderCrawler struct that processes the content of a given folder.
// It fetches all files and subfolders from a specified directory, processes them using a provided function, and recursively traverses any subdirectories found.
func (F *folderCrawler) process(user *QSession, folder *QObject) {
	// Folder is already complete, return to caller.
	var folders []*QObject

	folders = append(folders, folder)

	var n int
	var next []*QObject

	if folder == nil {
		Err("Cannot process 'nil' folder.")
		return
	}

	if folder.Type != "D" && folder.Type != "S" {
		Err("%s is not a folder.", folder.Name)
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
					if o.IsSystemFolder() {
						continue
					}
					if F.folder_limiter.Try() {
						go func(user *QSession, folder *QObject) {
							defer F.folder_limiter.Done()
							f, err := user.File(folder.ID)
							if err != nil {
								Err("%s: %v", folder.Name, err)
								return
							}
							F.process(user, &f)
						}(user, o)
					} else {
						for _, o := range next[i:] {
							f, err := user.File(o.ID)
							if err != nil {
								Err("%s:  %v", o.Name, err)
								continue
							}
							folders = append(folders, &f)
						}
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
		if folders[n].Type == "D" || folders[n].Type == "S" {
			if F.processor != nil {
				if atomic.LoadInt32(&F.all_stop) > 0 {
					return
				}
				if err := F.processor(user, folders[n]); err != nil {
					err, abort := abortCheck(err)
					if err != nil {
						Err("%s - %s: %v", user.Username, folders[n].Name, err)
					}
					if abort {
						atomic.StoreInt32(&F.all_stop, 1)
						return
					}
				}
			}
			childs := folders[n].Content
			for i := 0; i < len(childs); i++ {
				childs[i].QSession = folder.QSession
				switch childs[i].Type {
				case "S":
					next = append(next, &childs[i])
				case "D":
					next = append(next, &childs[i])
				default:
					if atomic.LoadInt32(&F.all_stop) > 0 {
						return
					}
					F.file_chan <- &childs[i]
				}
			}
		}
		n++
	}
	return
}
