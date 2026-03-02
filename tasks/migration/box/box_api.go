package box

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/cmcoffee/kitebroker/core"
	"github.com/cmcoffee/snugforge/jwcrypt"
)

// BoxAPI wraps an APIClient for Box.com REST API access.
type BoxAPI struct {
	*APIClient
}

// BoxSession holds a user ID for As-User header injection plus the BoxAPI.
type BoxSession struct {
	UserID string
	*BoxAPI
}

// BoxUserRecord represents a Box.com user.
type BoxUserRecord struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Login string `json:"login,omitempty"`
}

// BoxFile represents Box.com file metadata.
type BoxFile struct {
	Created  string `json:"created_at"`
	Modified string `json:"modified_at"`
	Parent   struct {
		ID string `json:"id"`
	} `json:"parent"`
	Description string `json:"description"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	FileVersion struct {
		ID string `json:"id"`
	} `json:"file_version"`
	TrashedAt interface{} `json:"trashed_at"`
}

// BoxFolder represents Box.com folder metadata.
type BoxFolder struct {
	BoxID       string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Created     string        `json:"created_at"`
	OwnedBy     BoxUserRecord `json:"owned_by"`
	Path        struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	} `json:"path_collection"`
	Owner       string
	FullPath    string
	Items       []BoxFolderItem
	Permissions []BoxPermission
	*BoxSession
}

// BoxFolderItem represents a child item in a Box folder.
type BoxFolderItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// BoxPermission represents a folder permission mapping.
type BoxPermission struct {
	User string `json:"username"`
	Role int    `json:"role"`
}

// BoxVersion represents a file version from Box.com.
type BoxVersion struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Created  time.Time `json:"utc_created"`
	Modified time.Time `json:"utc_modified"`
	Size     int64     `json:"size"`
	Ver      int       `json:"version_num"`
}

// BoxComment represents a file comment from Box.com.
type BoxComment struct {
	Message string
}

// BoxTask represents a file task from Box.com.
type BoxTask struct {
	ID          string    `json:"id"`
	Action      string    `json:"action"`
	Created     time.Time `json:"created_at"`
	Creator     string    `json:"creator"`
	Due         time.Time `json:"dueby"`
	AssignedTo  []string  `json:"users"`
	Completed   bool      `json:"is_complete"`
	CompletedOn time.Time `json:"completed_date"`
	Message     string    `json:"message"`
}

// boxError parses a Box.com API error response body and returns an APIError.
func boxError(body []byte) (e APIError) {
	type BoxErr struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	var box_error *BoxErr
	json.Unmarshal(body, &box_error)

	if box_error != nil && box_error.Code != NONE {
		e.Register(fmt.Sprintf("BOX_%s", strings.ToUpper(box_error.Code)), box_error.Message)
	}

	return
}

// readBoxTime parses a Box.com timestamp string into a time.Time.
func readBoxTime(input string) (time.Time, error) {
	if input == NONE {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, input)
}

// dateString formats a time.Time as YYYY-MM-DD.
func dateString(input time.Time) string {
	return input.UTC().Format("2006-01-02")
}

// getBoxLogin returns a login email for a Box user, defaulting if blank.
func getBoxLogin(user BoxUserRecord) string {
	if user.Login == NONE {
		if user.ID != NONE {
			return fmt.Sprintf("%s@box.com", user.ID)
		}
		return "unknown_box_user@box.com"
	}
	return strings.ToLower(user.Login)
}

// boxNewToken returns a NewToken callback that performs Box JWT authentication.
func boxNewToken(jsonConfigData []byte) func(string) (*Auth, error) {
	return func(username string) (*Auth, error) {
		var boxJWT struct {
			AppSettings struct {
				ClientID string `json:"clientID"`
				Secret   string `json:"clientSecret"`
				AppAuth  struct {
					PublicKeyID string `json:"publicKeyID"`
					PrivateKey  string `json:"privateKey"`
					PassPhrase  string `json:"passphrase"`
				}
			} `json:"boxAppSettings"`
			ID string `json:"enterpriseID"`
		}

		if err := json.Unmarshal(jsonConfigData, &boxJWT); err != nil {
			return nil, fmt.Errorf("Failed to parse Box JSON config: %v", err)
		}

		if boxJWT.AppSettings.AppAuth.PrivateKey == NONE {
			return nil, fmt.Errorf("Blank privateKey in Box JSON config; file was not created via 'Generate Public/Private key' button.")
		}

		key, err := jwcrypt.ParseRSAPrivateKey([]byte(boxJWT.AppSettings.AppAuth.PrivateKey), []byte(boxJWT.AppSettings.AppAuth.PassPhrase))
		if err != nil {
			return nil, fmt.Errorf("Failed to parse RSA private key: %v", err)
		}

		var claims struct {
			ClientID     string `json:"iss"`
			EnterpriseID string `json:"sub"`
			BoxSubType   string `json:"box_sub_type"`
			Audience     string `json:"aud"`
			JTI          string `json:"jti"`
			Expiry       int64  `json:"exp"`
		}

		claims.ClientID = boxJWT.AppSettings.ClientID
		claims.EnterpriseID = boxJWT.ID
		claims.BoxSubType = "enterprise"
		claims.Audience = "https://api.box.com/oauth2/token"
		claims.JTI = UUIDv4()
		claims.Expiry = time.Now().Add(45 * time.Second).Unix()

		tokenStr, err := jwcrypt.SignRS256(key, claims, map[string]string{"kid": boxJWT.AppSettings.AppAuth.PublicKeyID})
		if err != nil {
			return nil, fmt.Errorf("Failed to sign JWT: %v", err)
		}

		values := url.Values{}
		values.Add("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
		values.Add("client_id", boxJWT.AppSettings.ClientID)
		values.Add("client_secret", boxJWT.AppSettings.Secret)
		values.Add("assertion", tokenStr)

		req, err := http.NewRequest(http.MethodPost, "https://api.box.com/oauth2/token", strings.NewReader(values.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		client := http.DefaultClient
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Failed to request Box token: %v", err)
		}

		token := new(Auth)
		if err := DecodeJSON(resp, &token); err != nil {
			return nil, fmt.Errorf("Failed to decode Box token response: %v", err)
		}

		token.Expires = time.Now().Add(time.Duration(token.Expires) * time.Second).Unix()

		return token, nil
	}
}

// Session creates a BoxSession for the given user ID.
// An empty userID creates an admin session.
func (B *BoxAPI) Session(userID string) *BoxSession {
	return &BoxSession{UserID: userID, BoxAPI: B}
}

// Call performs a Box API request, injecting the As-User header when a UserID is set.
func (B *BoxSession) Call(req APIRequest) error {
	req.Username = "box_api_user"
	if B.UserID != NONE {
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		req.Header.Set("As-User", B.UserID)
	}
	// Prepend /2.0 to path for Box API v2.
	if !strings.HasPrefix(req.Path, "/2.0") {
		req.Path = "/2.0" + req.Path
	}
	return B.APIClient.Call(req)
}

// Users retrieves all Box.com users via paginated API calls.
func (B *BoxSession) Users() (users []BoxUserRecord, err error) {
	var offset int

	for {
		var result struct {
			Entries []BoxUserRecord `json:"entries"`
		}

		err = B.Call(APIRequest{
			Method: "GET",
			Path:   "/users",
			Params: SetParams(Query{"limit": 100, "offset": offset}),
			Output: &result,
		})
		if err != nil {
			return nil, err
		}

		users = append(users, result.Entries...)

		if len(result.Entries) < 100 {
			break
		}
		offset += 100
	}

	return
}

// Folder retrieves a Box folder's metadata, items, and permissions.
func (B *BoxSession) Folder(folderID string) (*BoxFolder, error) {
	var folder struct {
		Name        string        `json:"name"`
		ID          string        `json:"id"`
		Description string        `json:"description"`
		Created     string        `json:"created_at"`
		OwnedBy     BoxUserRecord `json:"owned_by"`
		Path        struct {
			Entries []struct {
				Name string `json:"name"`
			} `json:"entries"`
		} `json:"path_collection"`
	}

	if err := B.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/folders/%s", folderID),
		Output: &folder,
	}); err != nil {
		return nil, err
	}

	fr := &BoxFolder{
		BoxID:       folderID,
		Name:        folder.Name,
		Description: folder.Description,
		BoxSession:  B,
	}

	fr.Owner = strings.ToLower(getBoxLogin(folder.OwnedBy))

	var path []string
	for _, x := range folder.Path.Entries {
		if x.Name == "All Files" {
			x.Name = " "
		}
		path = append(path, x.Name)
	}
	path = append(path, fr.Name)
	fr.FullPath = strings.TrimPrefix(strings.Join(path, "/"), " ")

	if err := fr.getItems(); err != nil {
		return nil, err
	}

	if err := fr.getPermissions(); err != nil {
		return nil, err
	}

	return fr, nil
}

// getItems paginates through folder items.
func (fr *BoxFolder) getItems() error {
	var offset int

	for {
		var items struct {
			Entries []struct {
				Name string `json:"name"`
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"entries"`
			Limit int `json:"limit"`
		}

		if err := fr.BoxSession.Call(APIRequest{
			Method: "GET",
			Path:   SetPath("/folders/%s/items", fr.BoxID),
			Params: SetParams(Query{"limit": 100, "offset": offset}),
			Output: &items,
		}); err != nil {
			return err
		}

		for _, x := range items.Entries {
			fr.Items = append(fr.Items, BoxFolderItem{
				ID:   x.ID,
				Name: x.Name,
				Type: x.Type,
			})
		}

		if len(items.Entries) < 100 {
			break
		}
		offset += items.Limit
	}
	return nil
}

// getPermissions retrieves folder collaborations from Box, expanding groups.
func (fr *BoxFolder) getPermissions() error {
	if fr.BoxID == "0" {
		return nil
	}

	var permission struct {
		Entries []struct {
			AccessibleBy struct {
				Login string `json:"login"`
				Name  string `json:"name"`
				Type  string `json:"type"`
				ID    string `json:"id"`
			} `json:"accessible_by"`
			Role    string `json:"role"`
			Invited string `json:"invite_email"`
			Status  string `json:"status"`
		} `json:"entries"`
	}

	if err := fr.BoxSession.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/folders/%s/collaborations", fr.BoxID),
		Output: &permission,
	}); err != nil {
		return err
	}

	for _, x := range permission.Entries {
		if x.Status != "pending" && x.Status != "accepted" {
			continue
		}
		if x.AccessibleBy.Type == NONE {
			x.AccessibleBy.Type = "user"
		}
		switch x.AccessibleBy.Type {
		case "user":
			user := x.AccessibleBy.Login
			if user == NONE {
				user = x.Invited
			}
			fr.Permissions = append(fr.Permissions, BoxPermission{
				User: strings.ToLower(user),
				Role: MapPermission(x.Role),
			})
		case "group":
			members, err := fr.BoxSession.GroupMembers(x.AccessibleBy.ID)
			if err != nil {
				return err
			}
			for _, user := range members {
				if user == fr.Owner {
					continue
				}
				fr.Permissions = append(fr.Permissions, BoxPermission{
					User: strings.ToLower(user),
					Role: MapPermission(x.Role),
				})
			}
		}
	}
	return nil
}

// GroupMembers expands a Box group into its member login emails.
func (B *BoxSession) GroupMembers(groupID string) (users []string, err error) {
	var offset int

	for {
		var membership struct {
			Members []struct {
				User BoxUserRecord `json:"user"`
			} `json:"entries"`
		}

		err = B.Call(APIRequest{
			Method: "GET",
			Path:   SetPath("/groups/%s/memberships", groupID),
			Params: SetParams(Query{"limit": 100, "offset": offset}),
			Output: &membership,
		})
		if err != nil {
			return nil, err
		}

		for _, v := range membership.Members {
			users = append(users, strings.ToLower(getBoxLogin(v.User)))
		}

		if len(membership.Members) < 100 {
			break
		}
		offset += len(membership.Members)
	}
	return
}

// MapPermission maps a Box.com role string to a Kiteworks role ID.
func MapPermission(role string) int {
	switch strings.ToLower(role) {
	case "editor":
		return 3 // collaborator
	case "co-owner":
		return 4 // manager
	case "uploader", "viewer uploader", "previewer uploader":
		return 7 // uploader
	case "viewer", "previewer":
		return 6 // viewer
	default:
		return 6 // viewer
	}
}

// MapPermissionName maps a Box.com role string to a human-readable permission name.
func MapPermissionName(role string) string {
	switch strings.ToLower(role) {
	case "editor":
		return "Collaborator"
	case "co-owner":
		return "Manager"
	case "uploader", "viewer uploader", "previewer uploader":
		return "Uploader"
	case "viewer", "previewer":
		return "Viewer"
	default:
		return "Viewer"
	}
}

// FileVersions retrieves all versions of a file from Box.com.
// Returns versions in oldest-first order, with the current version appended.
func (B *BoxSession) FileVersions(fileID string) (versions []BoxVersion, err error) {
	// Get current file info.
	var current BoxFile
	if err = B.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/files/%s", fileID),
		Output: &current,
	}); err != nil {
		return nil, err
	}

	// Get prior versions.
	var versionResp struct {
		Entries *[]BoxFile `json:"entries"`
	}
	if err = B.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/files/%s/versions", fileID),
		Output: &versionResp,
	}); err != nil {
		return nil, err
	}

	ver := 1

	if versionResp.Entries != nil {
		for i := len(*versionResp.Entries) - 1; i >= 0; i-- {
			x := (*versionResp.Entries)[i]
			if x.TrashedAt != nil {
				ver++
				continue
			}
			createdTime, err := readBoxTime(x.Created)
			if err != nil {
				return nil, err
			}
			modifiedTime, err := readBoxTime(x.Modified)
			if err != nil {
				return nil, err
			}

			versions = append(versions, BoxVersion{
				ID:       x.ID,
				Created:  createdTime.UTC(),
				Modified: modifiedTime.UTC(),
				Name:     x.Name,
				Size:     x.Size,
				Ver:      ver,
			})
			ver++
		}
	}

	createdTime, err := readBoxTime(current.Created)
	if err != nil {
		return nil, err
	}
	modifiedTime, err := readBoxTime(current.Modified)
	if err != nil {
		return nil, err
	}

	versions = append(versions, BoxVersion{
		ID:       current.FileVersion.ID,
		Created:  createdTime.UTC(),
		Modified: modifiedTime.UTC(),
		Name:     current.Name,
		Size:     current.Size,
		Ver:      ver,
	})

	return
}

// FileComments retrieves paginated file comments from Box.com.
func (B *BoxSession) FileComments(fileID string) (comments []BoxComment, err error) {
	var offset int

	for {
		var result struct {
			Entries []struct {
				ID      string        `json:"id"`
				Creator BoxUserRecord `json:"created_by"`
				Created string        `json:"created_at"`
				Message string        `json:"message"`
			} `json:"entries"`
			TotalCount int `json:"total_count"`
			Limit      int `json:"limit"`
		}

		err = B.Call(APIRequest{
			Method: "GET",
			Path:   SetPath("/files/%s/comments", fileID),
			Params: SetParams(Query{"limit": 100, "offset": offset}),
			Output: &result,
		})
		if err != nil {
			return nil, err
		}

		if len(result.Entries) == 0 {
			break
		}

		for _, c := range result.Entries {
			createdTime, err := readBoxTime(c.Created)
			if err != nil {
				return nil, err
			}
			comments = append(comments, BoxComment{
				Message: fmt.Sprintf("[%s] box.com comment created by (%s): %s", dateString(createdTime), getBoxLogin(c.Creator), c.Message),
			})
		}

		if len(result.Entries) < 100 {
			break
		}
		offset += result.Limit
	}
	return
}

// FileTasks retrieves tasks associated with a file from Box.com.
func (B *BoxSession) FileTasks(fileID string) (tasks []BoxTask, err error) {
	var result struct {
		Entries []struct {
			ID             string        `json:"id"`
			Action         string        `json:"action"`
			Created        string        `json:"created_at"`
			CreatedBy      BoxUserRecord `json:"created_by"`
			DueBy          string        `json:"due_at"`
			Completed      bool          `json:"is_completed"`
			Message        string        `json:"message"`
			TaskAssignment struct {
				Entries []struct {
					AssignedTo    BoxUserRecord `json:"assigned_to"`
					CompletedDate string        `json:"completed_at"`
				} `json:"entries"`
			} `json:"task_assignment_collection"`
		} `json:"entries"`
	}

	err = B.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/files/%s/tasks", fileID),
		Output: &result,
	})
	if err != nil {
		return nil, err
	}

	for i := len(result.Entries) - 1; i >= 0; i-- {
		entry := result.Entries[i]

		createdTime, err := readBoxTime(entry.Created)
		if err != nil {
			return nil, err
		}

		var dueDate time.Time
		if entry.DueBy != NONE {
			dueDate, err = readBoxTime(entry.DueBy)
			if err != nil {
				return nil, err
			}
		}

		newTask := BoxTask{
			ID:        entry.ID,
			Action:    entry.Action,
			Created:   createdTime,
			Creator:   getBoxLogin(entry.CreatedBy),
			Due:       dueDate,
			Message:   entry.Message,
			Completed: entry.Completed,
		}

		for _, u := range entry.TaskAssignment.Entries {
			login := getBoxLogin(u.AssignedTo)
			if login == NONE || login == "unknown_box_user@box.com" {
				continue
			}
			if u.CompletedDate != NONE {
				newTask.CompletedOn, err = readBoxTime(u.CompletedDate)
				if err != nil {
					return nil, err
				}
			}
			newTask.AssignedTo = append(newTask.AssignedTo, login)
		}
		if len(newTask.AssignedTo) == 0 {
			continue
		}
		tasks = append(tasks, newTask)
	}

	return
}

// Download returns a ReadSeekCloser for downloading a file from Box.com.
func (B *BoxSession) Download(fileID string) (ReadSeekCloser, error) {
	req, err := B.APIClient.NewRequest("GET", fmt.Sprintf("/2.0/files/%s/content", fileID))
	if err != nil {
		return nil, err
	}
	if B.UserID != NONE {
		req.Header.Set("As-User", B.UserID)
	}
	err = B.APIClient.SetToken("box_api_user", req)
	if err != nil {
		return nil, err
	}
	return B.APIClient.WebDownload(req), nil
}

// Files returns count of file items in a folder.
func (f *BoxFolder) Files() int {
	var n int
	for _, v := range f.Items {
		if v.Type == "file" {
			n++
		}
	}
	return n
}

// Folders returns count of folder items in a folder.
func (f *BoxFolder) Folders() int {
	var n int
	for _, v := range f.Items {
		if v.Type == "folder" {
			n++
		}
	}
	return n
}

// boxFolderCrawler traverses Box.com folder hierarchies concurrently.
// It processes folders and files via a processor callback, using LimitGroups
// to manage concurrency for both folder traversal and file processing.
type boxFolderCrawler struct {
	processor      func(*BoxSession, *BoxFolder, *BoxFolderItem) error
	folder_limiter LimitGroup
	file_chan      chan boxFileMsg
	all_stop       int32
}

// boxFileMsg carries a file item along with the parent folder context.
type boxFileMsg struct {
	folder *BoxFolder
	item   *BoxFolderItem
}

// abortError represents a custom error type to signal abort of the crawler.
type abortError struct {
	err error
}

// Error returns the error message.
func (e abortError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return ""
}

// abortCheck checks if an error is of type abortError.
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

// FolderCrawler starts a concurrent folder crawler that traverses the provided
// BoxFolder trees and calls the processor for each folder and file encountered.
// For folders, the item argument is nil. For files, the folder argument is the parent.
func (B *BoxSession) FolderCrawler(processor func(*BoxSession, *BoxFolder, *BoxFolderItem) error, folders ...*BoxFolder) {
	crawler := new(boxFolderCrawler)
	crawler.folder_limiter = NewLimitGroup(50)
	file_limiter := NewLimitGroup(50)
	crawler.processor = processor
	crawler.file_chan = make(chan boxFileMsg, 1000)

	file_limiter.Add(1)
	go func(sess *BoxSession) {
		defer file_limiter.Done()
		for m := range crawler.file_chan {
			if atomic.LoadInt32(&crawler.all_stop) > 0 || crawler.processor == nil {
				continue
			}
			if file_limiter.Try() {
				go func(sess *BoxSession, msg boxFileMsg) {
					defer file_limiter.Done()
					err, abort := abortCheck(processor(sess, msg.folder, msg.item))
					if err != nil {
						Err("%s - %s: %v", sess.UserID, msg.item.Name, err)
					}
					if abort {
						atomic.StoreInt32(&crawler.all_stop, 1)
					}
				}(B, m)
			} else {
				err, abort := abortCheck(processor(sess, m.folder, m.item))
				if err != nil {
					Err("%s - %s: %v", sess.UserID, m.item.Name, err)
				}
				if abort {
					atomic.StoreInt32(&crawler.all_stop, 1)
				}
			}
		}
	}(B)

	for _, f := range folders {
		crawler.folder_limiter.Add(1)
		go func(sess *BoxSession, folder *BoxFolder) {
			defer crawler.folder_limiter.Done()
			crawler.process(sess, folder)
		}(B, f)
	}
	crawler.folder_limiter.Wait()
	close(crawler.file_chan)
	file_limiter.Wait()
}

// process recursively traverses a BoxFolder and its subfolders.
// It calls the processor for each folder, sends files to the file channel,
// and spawns goroutines for subfolder traversal when concurrency permits.
func (F *boxFolderCrawler) process(sess *BoxSession, folder *BoxFolder) {
	var folders []*BoxFolder
	folders = append(folders, folder)

	var n int
	var next []*BoxFolder

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
						go func(sess *BoxSession, folder *BoxFolder) {
							defer F.folder_limiter.Done()
							F.process(sess, folder)
						}(sess, o)
					} else {
						folders = append(folders, next[i:]...)
						break
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

		current := folders[n]

		// Call processor for the folder itself (item=nil).
		if F.processor != nil {
			if atomic.LoadInt32(&F.all_stop) > 0 {
				return
			}
			if err := F.processor(sess, current, nil); err != nil {
				err, abort := abortCheck(err)
				if err != nil {
					Err("%s - %s: %v", sess.UserID, current.Name, err)
				}
				if abort {
					atomic.StoreInt32(&F.all_stop, 1)
					return
				}
			}
		}

		// Dispatch items: files to channel, folders to next queue.
		for i := range current.Items {
			item := &current.Items[i]
			switch item.Type {
			case "folder":
				subFolder, err := sess.Folder(item.ID)
				if err != nil {
					Err("%s: %v", item.Name, err)
					continue
				}
				next = append(next, subFolder)
			case "file":
				if atomic.LoadInt32(&F.all_stop) > 0 {
					return
				}
				F.file_chan <- boxFileMsg{folder: current, item: item}
			}
		}
		n++
	}
}

// TaskString generates a message string for a Box task.
func TaskString(task *BoxTask, forImport bool) string {
	var insert string
	if !forImport {
		insert = fmt.Sprintf(" assigned to %s,", strings.Join(task.AssignedTo, ", "))
	}

	dueDate := task.Due.UTC()
	nowDate := time.Now().UTC()

	switch {
	case task.Completed:
		return fmt.Sprintf("[%s] box.com task: \"%s\", created by %s,%s and completed on %s.", dateString(task.Created), task.Message, task.Creator, insert, dateString(task.CompletedOn))
	case dueDate.IsZero():
		return fmt.Sprintf("[%s] box.com task: \"%s\", created by %s,%s and had no due date.", dateString(task.Created), task.Message, task.Creator, insert)
	case dueDate.Unix() < nowDate.Unix() || dateString(dueDate) == dateString(nowDate):
		return fmt.Sprintf("[%s] box.com task: \"%s\", created by %s,%s and was due on %s.", dateString(task.Created), task.Message, task.Creator, insert, dateString(task.Due))
	default:
		if forImport {
			return fmt.Sprintf("[%s] box.com task: \"%s\".", dateString(task.Created), task.Message)
		}
		return fmt.Sprintf("[%s] box.com task: \"%s\", created by %s, assigned to %s, and is due on %s.", dateString(task.Created), task.Message, task.Creator, strings.Join(task.AssignedTo, ", "), dateString(task.Due.UTC()))
	}
}
