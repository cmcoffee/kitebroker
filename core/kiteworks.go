package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

var ErrNotFound = errors.New("Requested item not found.")

// SkipExistingPerms removes entries from perm_map that already exist in kw_perms,
// and returns the number of skipped permissions.
func SkipExistingPerms(perm_map map[int][]string, kw_perms []KiteMember) (skipped int) {
	for _, p := range kw_perms {
		for i := 0; i < len(perm_map[p.RoleID]); i++ {
			if p.User.Email == perm_map[p.RoleID][i] {
				skipped++
				perm_map[p.RoleID] = slices.Delete(perm_map[p.RoleID], i, i+1)
				i--
			}
		}
		if len(perm_map[p.RoleID]) == 0 {
			delete(perm_map, p.RoleID)
		}
	}
	return
}

// FileInfo represents information about a file.
// It provides access to the file's name, size, and modification time.
type FileInfo interface {
	Name() string
	Size() int64
	ModTime() time.Time
}

// KiteMember Folder Membership Object
type KiteMember struct {
	ID     string         `json:"objectId"`
	RoleID int            `json:"roleId"`
	User   KiteUser       `json:"user"`
	Role   KitePermission `json:"role"`
}

// KiteObject KiteFile/Folder/Attachment
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
	UserID                string         `json:"userId,omitempty"`
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
	Pushed                bool           `json:"pushed,omitempty"`
}

// Locked Returns true if the object is locked, false otherwise.
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

// Expiry Returns the expiry time of the KiteObject, if available.
func (K *KiteObject) Expiry() time.Time {
	var exp_time time.Time

	if exp_string, ok := K.Expire.(string); ok {
		exp_time, _ = ReadKWTime(exp_string)
	}

	return exp_time
}

// KiteLinks Kiteworks Links Data
type KiteLinks struct {
	Relationship string `json:"rel"`
	Entity       string `json:"entity"`
	ID           string `json:"id"`
	URL          string `json:"href"`
}

// KitePermission Permission information
type KitePermission struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	//Rank       int    `json:"rank"`
	Allowed    bool `json:"allowed"`
	Modifiable bool `json:"modifiable"`
	Disabled   bool `json:"disabled"`
}

// FolderRoles Returns all available folder roles.
func (K KWSession) FolderRoles() (result []KitePermission, err error) {
	return result, K.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/roles"),
		Output: &result,
	}, -1, 1000)
}

// FindPermission Find ID for specific permission.
func (K KWSession) FindPermission(perm string) (id int, err error) {
	perms, err := K.FolderRoles()
	if err != nil {
		return 0, err
	}
	l_perm := strings.ToLower(perm)
	for _, v := range perms {
		if l_perm == strings.ToLower(v.Name) {
			return v.ID, nil
		}
	}
	return -1, fmt.Errorf("No permission found for %s.", perm)
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

// PubSub webhook status values, as reported by KiteWebhookStatus.Status.
const (
	WEBHOOK_STATUS_UNKNOWN = "UNKNOWN"
	WEBHOOK_STATUS_SUCCESS = "SUCCESS"
	WEBHOOK_STATUS_ERROR   = "ERROR"
)

// KiteWebhookStatus represents the operational status of a PubSub webhook.
type KiteWebhookStatus struct {
	Status      string `json:"status"`
	Description string `json:"description"`
}

// KiteWebhook represents a PubSub consumer webhook registration.
type KiteWebhook struct {
	ID            string            `json:"id"`
	URL           string            `json:"url"`
	Enabled       bool              `json:"enabled"`
	Created       string            `json:"createdAt"`
	Modified      string            `json:"updatedAt"`
	Subscriptions []string          `json:"subscriptions"`
	Status        KiteWebhookStatus `json:"status"`
}

// Webhooks retrieves all PubSub webhooks, paginating through the result set.
// It accepts optional query parameters to filter the results.
func (K KWSession) Webhooks(params ...interface{}) (webhooks []KiteWebhook, err error) {
	err = K.ItemsCall(APIRequest{
		Method: "GET",
		Path:   "/pubsub-ext/webhooks",
		Output: &webhooks,
		Params: SetParams(params),
	}, -1, 1000)
	return
}

// CreateWebhook creates a new PubSub webhook and returns the created webhook.
// webhook_url and subscriptions are required; optional fields such as secret,
// token, and enabled may be supplied via PostJSON.
func (K KWSession) CreateWebhook(webhook_url string, subscriptions []string, params ...interface{}) (webhook KiteWebhook, err error) {
	err = K.Call(APIRequest{
		Method: "POST",
		Path:   "/pubsub-ext/webhooks",
		Params: SetParams(PostJSON{"url": webhook_url, "subscriptions": subscriptions}, params),
		Output: &webhook,
	})
	return
}

// kw_webhook represents a single PubSub webhook accessor in the consumer API.
type kw_webhook struct {
	webhook_id string
	*KWSession
}

// Webhook returns a kw_webhook accessor for the given webhook ID.
func (K KWSession) Webhook(webhook_id string) kw_webhook {
	return kw_webhook{webhook_id, &K}
}

// Info retrieves the webhook by its ID.
func (s kw_webhook) Info() (webhook KiteWebhook, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/pubsub-ext/webhooks/%s", s.webhook_id),
		Output: &webhook,
	})
	return
}

// Update replaces the webhook with the provided values and returns the updated webhook.
// webhook_url and subscriptions are required; optional fields such as secret,
// token, and enabled may be supplied via PostJSON.
func (s kw_webhook) Update(webhook_url string, subscriptions []string, params ...interface{}) (webhook KiteWebhook, err error) {
	err = s.Call(APIRequest{
		Method: "PUT",
		Path:   SetPath("/pubsub-ext/webhooks/%s", s.webhook_id),
		Params: SetParams(PostJSON{"url": webhook_url, "subscriptions": subscriptions}, params),
		Output: &webhook,
	})
	return
}

// Patch partially updates the webhook, sending only the supplied fields
// (the WebhookPatchRequest form). Any subset of url, token, secret, enabled,
// and subscriptions may be provided via PostJSON, e.g. PostJSON{"enabled": false}.
func (s kw_webhook) Patch(params ...interface{}) (webhook KiteWebhook, err error) {
	err = s.Call(APIRequest{
		Method: "PATCH",
		Path:   SetPath("/pubsub-ext/webhooks/%s", s.webhook_id),
		Params: SetParams(params),
		Output: &webhook,
	})
	return
}

// Delete removes the webhook by its ID.
func (s kw_webhook) Delete() (err error) {
	return s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/pubsub-ext/webhooks/%s", s.webhook_id),
	})
}

type kw_rest_folder struct {
	folder_id string
	*KWSession
}

// Folder returns a folder object for the given folder ID.
func (K KWSession) Folder(folder_id string) kw_rest_folder {
	return kw_rest_folder{
		folder_id,
		&K,
	}
}

// Files retrieves a list of files within the current folder.
// It accepts optional parameters to filter the results.
// Returns a slice of KiteObjects representing the files and an error.
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

// Info returns folder information.
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

// KiteActivity is the activities object for Kiteworks.
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
		Successful bool `json:"succcessful"`
	} `json:"data"`
	User struct {
		UserID      string `json:"userId"`
		ProfileIcon string `json:"profileIcon"`
		Name        string `json:"name"`
	} `json:"user"`
}

// Activities retrieves the activity list for the folder.
// It takes optional parameters to filter the results.
func (s kw_rest_folder) Activities(params ...interface{}) (result []KiteActivity, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%s/activities", s.folder_id),
		Output: &result,
		Params: SetParams(params),
	}, -1, 1000)
	return
}

// NewFolder creates a new folder within the current folder.
// It takes the folder name and optional parameters as input.
// Returns the created KiteObject and any error encountered.
func (s kw_rest_folder) NewFolder(name string, params ...interface{}) (output KiteObject, err error) {
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/folders/%s/folders", s.folder_id),
		Params: SetParams(PostJSON{"name": name}, Query{"returnEntity": true}, params),
		Output: &output,
	})
	return
}

// Delete deletes the folder.
func (s kw_rest_folder) Delete(params ...interface{}) (err error) {
	err = s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/folders/%s", s.folder_id),
		Params: SetParams(params),
	})
	return
}

// Members returns the members of the folder.
func (s kw_rest_folder) Members(params ...interface{}) (result []KiteMember, err error) {
	return result, s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%s/members", s.folder_id),
		Output: &result,
		Params: SetParams(params, Query{"with": "(user,role)"}),
	}, -1, 1000)
}

// ChangeMember
func (s kw_rest_folder) ChangeMember(user_id string, permission int, downgrade_nested bool, params ...interface{}) (err error) {
	params = SetParams(PostJSON{"roleId": permission}, params)
	err = s.Call(APIRequest{
		Method: "PUT",
		Path:   SetPath("/rest/folders/%s/members/%s", s.folder_id, user_id),
		Params: SetParams(params, Query{"downgradeNested": downgrade_nested}),
	})
	return
}

// AddUsersToFolder adds multiple users to a folder with specified role and notification settings.
func (s kw_rest_folder) AddUsersToFolder(emails []string, role_id int, notify bool, notify_files_added bool, params ...interface{}) (err error) {
	params = SetParams(PostJSON{"notify": notify, "notifyFileAdded": notify_files_added, "emails": emails, "roleId": role_id}, Query{"updateIfExists": true, "partialSuccess": true}, params)
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/folders/%s/members", s.folder_id),
		Params: params,
	})
	return
}

// RemoveUsersFromFolder
func (s kw_rest_folder) RemoveUserFromFolder(user_id string, params ...interface{}) (err error) {
	return s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/folders/%s/members/%s", s.folder_id, user_id),
		Params: params,
	})
}

// ResolvePath resolves a path to a KiteObject, creating folders as needed.
// It returns the resolved KiteObject and any error encountered.
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

// Find recursively searches for a folder or file by path.
// It returns the KiteObject if found, otherwise ErrNotFound.
// Accepts optional query parameters to filter the search.
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

type KWCompose struct {
	To                 []string      `json:"to,omitempty"`
	CC                 []string      `json:"cc,omitempty"`
	BCC                []string      `json:"bcc,omitempty"`
	Files              []string      `json:"files,omitempty"`
	ACL                string        `json:"acl,omitempty"`
	Expire             string        `json:"expire,omitempty"`
	Draft              string        `json:"draft,omitempty"`
	Preview            string        `json:"preview,omitempty"`
	Watermark          string        `json:"watermark,omitempty"`
	SecureBody         bool          `json:"secureBody,omitempty"`
	SelfCopy           bool          `json:"selfCopy,omitempty"`
	IncludeFingerpting bool          `json:"includeFingerprint,omitempty"`
	ParentEmailID      int           `json:"parentEmailId,omitempty"`
	SelfReturnReceipt  bool          `json:"isSelfReturnReceipt,omitempty"`
	ReturnReceipts     []string      `json:"returnReceipts,omitempty"`
	Type               string        `json:"type,omitempty"`
	Uploading          bool          `json:"uploading,omitempty"`
	Body               string        `json:"body,omitempty"`
	Subject            string        `json:"subject,omitempty"`
	WebFormID          int           `json:"webFormId,omitempty"`
	WebFormFields      []interface{} `json:"webFormFields,omitempty"`
	SharedMailboxID    string        `json:"sharedMailboxId,omitempty"`
	TrackingAccess     []string      `json:"trackingAccess,omitempty"`
	*KWSession
}

func (K KWSession) NewMail() KWCompose {
	return KWCompose{KWSession: &K}
}

// Send sends a Kiteworks email composed from the KWCompose fields. It posts to
// /rest/mail/actions/sendFile (the /rest/mail collection itself is read-only)
// and, when returnEntity is requested, decodes the created mail. Recipients
// (To) and a Subject are required; Files may be empty for a body-only message.
func (s KWCompose) Send(params ...interface{}) (mail KiteMail, err error) {
	if len(s.To) == 0 {
		return mail, fmt.Errorf("Send: at least one recipient (To) is required.")
	}

	// files must be present in the payload; the appliance expects the key even
	// for a body-only message (an empty list).
	files := s.Files
	if files == nil {
		files = []string{}
	}
	body := PostJSON{
		"to":      s.To,
		"subject": s.Subject,
		"body":    s.Body,
		"files":   files,
	}
	if len(s.CC) > 0 {
		body["cc"] = s.CC
	}
	if len(s.BCC) > 0 {
		body["bcc"] = s.BCC
	}

	err = s.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/mail/actions/sendFile",
		Params: SetParams(body, Query{"returnEntity": true}, params),
		Output: &mail,
	})
	return
}

// kw_rest_admin encapsulates admin-level REST API functionality.
// It provides methods for managing users, profiles, and activities.
type kw_rest_admin struct {
	*KWSession
}

// Admin returns a new kw_rest_admin instance associated with the session.
func (K KWSession) Admin() kw_rest_admin {
	return kw_rest_admin{&K}
}

// KiteAdminActivity represents an admin activity entry from the Kiteworks admin activity log.
type KiteAdminActivity struct {
	ID            string                 `json:"id"`
	Created       string                 `json:"created"`
	EventName     string                 `json:"eventName"`
	Description   string                 `json:"description"`
	Successful    bool                   `json:"successful"`
	UserName      string                 `json:"userName"`
	IPAddress     string                 `json:"ipAddress"`
	ClientName    string                 `json:"clientName"`
	GeolocationID int                    `json:"geolocationId"`
	Data          map[string]interface{} `json:"data"`
}

// Activities retrieves the admin activities list with pagination via EventsCall.
// Required params: Query{"startDateTime": "...", "endDateTime": "..."}.
// Optional params: eventFilters:in, objectIds:in, userId, maxPages, orderBy, compact.
func (s kw_rest_admin) Activities(output interface{}, offset int, limit int, params ...interface{}) (err error) {
	return s.EventsCall(APIRequest{
		Method: "GET",
		Path:   "/rest/admin/activities",
		Params: SetParams(params[0:]...),
		Output: output,
	}, offset, limit)
}

// Activity retrieves detailed information about a specific admin activity by its UUID.
func (s kw_rest_admin) Activity(id string, output interface{}, params ...interface{}) (err error) {
	return s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/admin/activities/%s", id),
		Params: SetParams(params[0:]...),
		Output: output,
	})
}

// Register registers a new user with the given email and password.
// It returns an error if registration fails.
func (s kw_rest_admin) Register(email string, password string) (err error) {
	return s.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/users/register",
		Params: SetParams(PostJSON{"email": email, "password": password}),
	})
}

// ActivateUser activates the specified user.
// It sets the user's suspended, verified, and deactivated flags.
func (s kw_rest_admin) ActivateUser(userid string) (err error) {
	err = s.Call(APIRequest{
		Method: "PUT",
		Path:   SetPath("/rest/admin/users/%s", userid),
		Params: SetParams(PostJSON{"suspended": false, "verified": true, "deactivated": false}),
	})
	return
}

// DeactivateUser deactivates a user by setting the "suspended" flag.
// It also sets "verified" to true and "deactivated" to false.
func (s kw_rest_admin) DeactivateUser(userid string) (err error) {
	err = s.Call(APIRequest{
		Method: "PUT",
		Path:   SetPath("/rest/admin/users/%s", userid),
		Params: SetParams(PostJSON{"suspended": true, "verified": true, "deactivated": false}),
	})
	return
}

// UpdateUser updates an existing user with the given ID and parameters.
// It returns an error if the update fails.
func (s kw_rest_admin) UpdateUser(userid string, params ...interface{}) (err error) {
	return s.Call(APIRequest{
		Method: "PUT",
		Path:   SetPath("/rest/admin/users/%s", userid),
		Params: SetParams(params),
	})
}

// UpdateUserProfile updates the profiles for specified users.
// It takes a profile ID and a slice of user IDs as input.
func (s kw_rest_admin) UpdateUserProfile(profile_id int, user_ids []string, params ...interface{}) (err error) {
	return s.Call(APIRequest{
		Method: "PUT",
		Path:   SetPath("/rest/admin/profiles/%d/users", profile_id),
		Params: SetParams(Query{"id:in": user_ids}),
	})
}

// NewUser creates a new user with the specified parameters.
// It returns the created KiteUser and any error encountered.
//
// If type_id is <= 0, userTypeId is omitted from the request so the appliance
// assigns the profile via its own login/LDAP/domain mapping rules. Passing an
// explicit type_id pins the profile as a manual override, so callers should only
// do so when they intend to override auto-mapping.
func (s kw_rest_admin) NewUser(user_email string, type_id int, verified, notify bool) (user *KiteUser, err error) {
	body := PostJSON{"email": user_email, "verified": verified, "sendNotification": notify}
	if type_id > 0 {
		body["userTypeId"] = type_id
	}
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/users",
		Params: SetParams(body, Query{"returnEntity": true}),
		Output: &user,
	})

	return user, err
}

// ERR_NO_USER_FOUND indicates that a user with the specified criteria was not found.
var ERR_NO_USER_FOUND = fmt.Errorf("User not found, or did not meet specified criteria.")

// FindUser finds a user by their email address.
// It returns the KiteUser object if found, otherwise nil and an error.
func (s kw_rest_admin) FindUser(user_email string, params ...interface{}) (user *KiteUser, err error) {
	user_getter, err := s.Admin().Users([]string{user_email}, 0)
	if err != nil {
		return nil, err
	}
	user_getter.show_errors = false
	users, err := user_getter.Next()
	if err != nil {
		return nil, err
	}

	if len(users) > 0 {
		return &users[0], nil
	}
	return nil, ERR_NO_USER_FOUND
}

// UserByID retrieves a single user by their numeric user ID via the admin API.
// It returns the KiteUser and any error encountered.
func (s kw_rest_admin) UserByID(user_id string, params ...interface{}) (user KiteUser, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/admin/users/%s", user_id),
		Params: SetParams(params),
		Output: &user,
	})
	return
}

// GetAllUsers retrieves a list of all user emails.
// It returns a slice of email strings and any error encountered.
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

// ImportUserMetadata imports user metadata from a CSV file.
// It allows updating existing users, sending notifications, and
// handling partial success.
func (s kw_rest_admin) ImportUserMetadata(csv io.ReadCloser, update_if_exists, send_notification, partial_success bool) (err error) {
	uexists := fmt.Sprintf("%v", update_if_exists)
	snotify := fmt.Sprintf("%v", send_notification)
	psuccess := fmt.Sprintf("%v", partial_success)

	err = s.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/admin/users/actions/import",
		Params: SetParams(MimeBody{"content", "import_data.csv", csv, map[string]string{"updateIfExists": uexists, "sendNotification": snotify, "partialSuccess": psuccess}, -1}),
	})
	return
}

// MigrateEmails Migrates email addresses for a list of users.
// It updates user email addresses and optionally deletes existing ones.
func (s kw_rest_admin) MigrateEmails(users []map[string]string, delete_if_exists bool) (err error) {
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/admin/users/migrateEmails",
		Params: SetParams(PostJSON{"users": users, "deleteIfExists": delete_if_exists}),
	})
	return
}

// FindProfileUsers returns a list of emails for users associated with a profile.
// It retrieves user emails based on the given profile ID and optional parameters.
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

// DeleteUser deletes a user by ID.
func (s kw_rest_admin) DeleteUser(user KiteUser, params ...interface{}) (err error) {
	return s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/admin/users/%v", user.ID),
		Params: SetParams(params),
	})
}

// FindProfile finds a profile by its name (case-insensitive).
// It returns the KWProfile if found, otherwise an error.
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

// Profiles retrieves a list of KWProfiles. It accepts optional parameters.
func (s kw_rest_admin) Profiles(params ...interface{}) (profiles []KWProfile, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   "/rest/admin/profiles",
		Params: SetParams(params),
		Output: &profiles,
	}, -1, 1000)
	return
}

// kw_rest_file represents a file command within the Kite REST API.
type kw_rest_file struct {
	file_id string
	*KWSession
}

// Unlock unlocks the file.
func (s kw_rest_file) Unlock() (err error) {
	err = s.Call(APIRequest{
		Method: "PATCH",
		Path:   SetPath("/rest/files/%s/actions/unlock", s.file_id),
		Output: nil,
		Params: nil,
	})
	return
}

// Push pushes the file.
func (s kw_rest_file) Push() (err error) {
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/files/%s/actions/push", s.file_id),
		Output: nil,
		Params: nil,
	})
	return
}

// File returns a kw_rest_file object associated with the given file ID.
func (K KWSession) File(file_id string) kw_rest_file {
	return kw_rest_file{file_id, &K}
}

// Activities retrieves the activity list for the file.
// It takes optional parameters to filter the results.
func (s kw_rest_file) Activities(params ...interface{}) (result []KiteActivity, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/files/%s/activities", s.file_id),
		Output: &result,
		Params: SetParams(params),
	}, -1, 1000)
	return
}

// Info retrieves information about the file.
func (s kw_rest_file) Info(params ...interface{}) (result KiteObject, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/files/%s", s.file_id),
		Params: SetParams(params),
		Output: &result,
	})
	return
}

// Delete deletes the file.
func (s kw_rest_file) Delete(params ...interface{}) (err error) {
	err = s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/files/%s", s.file_id),
		Params: SetParams(params),
	})
	return
}

// AddComment adds a comment to the file.
func (s kw_rest_file) AddComment(contents string) (err error) {
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/files/%s/comments", s.file_id),
		Params: SetParams(PostJSON{"contents": contents}),
	})
	return
}

// AddTask adds a task to the file.
func (s kw_rest_file) AddTask(assignee_id, due, contents string) (err error) {
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/files/%s/tasks", s.file_id),
		Params: SetParams(PostJSON{"assigneeId": assignee_id, "due": due, "contents": contents}),
	})
	return
}

// PermDelete permanently deletes the file.
func (s kw_rest_file) PermDelete() (err error) {
	err = s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/files/%s/actions/permanent", s.file_id),
	})
	return
}

// KiteSource represents a data source.
type KiteSource struct {
	URL          string `json:"sourceURL"`
	Description  string `json:"description"`
	ID           string `json:"id"`
	SourceTypeID int    `json:"sourceTypeId"`
	PinnedTime   string `json:"pinnedTime"`
	Name         string `json:"name"`
	SourceByUser bool   `json:"sourceByUser"`
	Pinned       bool   `json:"pinned"`
}

type kw_source struct {
	source_id string
	*KWSession
}

// Source returns a kw_source associated with the given source_id.
func (K KWSession) Source(source_id string) kw_source {
	return kw_source{
		source_id,
		&K,
	}
}

// Folders returns a list of KiteObjects representing folders.
func (s kw_source) Folders(params ...interface{}) (folders []KiteObject, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/sources/%s/folders", s.source_id),
		Output: &folders,
		Params: SetParams(params),
	}, -1, 1000)
	return
}

// Sources retrieves a list of kite sources.
// It takes optional parameters to filter the sources.
// Returns a slice of KiteSource objects and an error.
func (K KWSession) Sources(params ...interface{}) (sources []KiteSource, err error) {
	err = K.DataCall(APIRequest{
		Method: "GET",
		Path:   "/rest/sources",
		Output: &sources,
		Params: SetParams(params),
	}, -1, 1000)
	return
}

// TopFolders returns the top-level folders for the current user.
// It accepts optional query parameters to filter the results.
func (K KWSession) TopFolders(params ...interface{}) (folders []KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}

	err = K.DataCall(APIRequest{
		Method: "GET",
		Path:   "/rest/folders/top",
		Output: &folders,
		Params: SetParams(params, Query{"with": "(path,currentUserRole)"}),
	}, -1, 1000)
	return
}

// OwnedFolders returns the top level folders owned by the current user.
// It accepts optional query parameters to filter the results.
func (K KWSession) OwnedFolders(params ...interface{}) (folders []KiteObject, err error) {
	if len(params) == 0 {
		params = SetParams(Query{"deleted": false})
	}

	var top_folders []KiteObject

	err = K.DataCall(APIRequest{
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

// Contents returns the children of the folder.
// Accepts optional parameters for filtering and with fields.
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

// Folders Returns a list of subfolders within the current folder.
// Accepts optional parameters for filtering and with fields.
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

// UploadBase64 uploads a base64-encoded file to the folder via JSON.
// This is a simple single-request upload without chunking, useful for testing.
func (s kw_rest_folder) UploadBase64(name string, content_base64 string, params ...interface{}) (result KiteObject, err error) {
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/folders/%s/actions/fileBase64Encoded", s.folder_id),
		Params: SetParams(PostJSON{"name": name, "content": content_base64}, Query{"returnEntity": true}, params),
		Output: &result,
	})
	return
}

// Recover recovers a deleted folder.
func (s kw_rest_folder) Recover(params ...interface{}) (err error) {
	return s.Call(APIRequest{
		Method: "PATCH",
		Path:   SetPath("/rest/folders/%s/actions/recover", s.folder_id),
	})
}

// Recover attempts to recover the file.
func (s kw_rest_file) Recover(params ...interface{}) (err error) {
	return s.Call(APIRequest{
		Method: "PATCH",
		Path:   SetPath("/rest/files/%s/actions/recover", s.file_id),
	})
}

// MoveToFolder moves the folder to the specified folder ID.
func (s kw_rest_folder) MoveToFolder(folder_id string) (err error) {
	return s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/folders/%s/actions/move", s.folder_id),
		Params: SetParams(PostJSON{"destinationFolderId": folder_id}),
	})
}

// KiteUser represents a user within the Kiteworks system.
type KiteUser struct {
	ID                   string `json:"id"`
	Flags                int    `json:"flags"`
	Active               bool   `json:"active"`
	Created              string `json:"created"`
	Deactivated          bool   `json:"deactivated"`
	Suspended            bool   `json:"suspended"`
	Deleted              bool   `json:"deleted"`
	Email                string `json:"email"`
	Name                 string `json:"name"`
	MyDirID              string `json:"mydirId"`
	BaseDirID            string `json:"basedirId"`
	SyncDirID            string `json:"syncdirId"`
	UserTypeID           int    `json:"userTypeId"`
	Verified             bool   `json:"verified"`
	Internal             bool   `json:"internal"`
	AdminRoleID          int    `json:"adminRoleId"`
	LastActivityDateTime string `json:"lastActivityDateTime"`
}

func (K KiteUser) LastActivity() (time.Time, error) {
	if IsBlank(K.LastActivityDateTime) {
		return time.Now().UTC(), fmt.Errorf("LastActivityDateTime was not provided by system. (Likely due to Kiteworks being lower than 9.1.0, must use User export csv.)")
	}
	return ReadKWTime(K.LastActivityDateTime)
}

// IsActive returns true if the user is active, and not deactivated, suspended, or deleted.
func (K KiteUser) IsActive() bool {
	if K.Active && !K.Deactivated && !K.Suspended && !K.Deleted {
		return true
	}
	return false
}

// MyUser retrieves the currently authenticated user.
// It returns the KiteUser object and an error if any.
func (K KWSession) MyUser() (user KiteUser, err error) {
	err = K.Call(APIRequest{
		Method: "GET",
		Path:   "/rest/users/me",
		Output: &user,
	})
	return
}

// KiteQuota represents the quota information for a Kite user.
// It includes send and folder usage and allowance details.
type KiteQuota struct {
	SendUsed      int `json:"send_quota_used"`
	FolderUsed    int `json:"folder_quota_users"`
	FolderAllowed int `json:"folder_quota_allowed"`
	SendAllowed   int `json:"send_quota_allowed"`
}

// MyQuota retrieves the current user's quota.
// It returns the KiteQuota and an error, if any.
func (K KWSession) MyQuota() (quota KiteQuota, err error) {
	err = K.Call(APIRequest{
		Method: "GET",
		Path:   "/rest/users/me/quota",
		Output: &quota,
	})
	return
}

// UpdateMobile updates the user's mobile phone number.
// It takes the new phone number as input and returns an error if the
// update fails.
func (K KWSession) UpdateMobile(phone string) (err error) {
	return K.Call(APIRequest{
		Method: "PUT",
		Path:   "/rest/users/me/mobileNumber",
		Params: SetParams(PostJSON{"mobileNumber": phone}),
	})
}

// KiteSshPublicKey represents a single SSH public key record belonging to the
// current login user. Returned by MySshPublicKeys and CreateMySshPublicKey.
type KiteSshPublicKey struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	PublicKey   string `json:"publicKey"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Created     string `json:"created,omitempty"`
}

// KiteSshKeyPair represents a freshly generated SSH key pair returned by
// GenerateMySshPublicKey. PrivateKey is only ever returned at generation time.
type KiteSshKeyPair struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
	Passphrase string `json:"passphrase,omitempty"`
}

// MySshPublicKeys lists the current user's SSH public keys.
func (K KWSession) MySshPublicKeys() (result []KiteSshPublicKey, err error) {
	return result, K.DataCall(APIRequest{
		Method: "GET",
		Path:   "/rest/userSshPublicKeys",
		Output: &result,
	}, -1, 1000)
}

// CreateMySshPublicKey registers an existing SSH public key for the current user.
// name is capped to 50 characters by the server. name and publicKey are sent in
// the JSON body (the endpoint reads them from the body, not the query string).
//
// The create response may not carry the new key's id, so when it comes back
// without one we re-list the user's keys and resolve the id by name — otherwise
// callers would record a key with id 0 and later be unable to delete it.
func (K KWSession) CreateMySshPublicKey(name, public_key string) (key KiteSshPublicKey, err error) {
	err = K.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/userSshPublicKeys/create",
		Params: SetParams(PostJSON{"name": name, "publicKey": public_key}, Query{"returnEntity": true}),
		Output: &key,
	})
	if err != nil {
		return
	}
	if key.ID <= 0 {
		if resolved, ok := K.findMySshKeyByName(name); ok {
			key = resolved
		}
	}
	return
}

// findMySshKeyByName re-lists the current user's SSH keys and returns the one
// matching name (case-sensitive, as stored), if any.
func (K KWSession) findMySshKeyByName(name string) (KiteSshPublicKey, bool) {
	keys, err := K.MySshPublicKeys()
	if err != nil {
		return KiteSshPublicKey{}, false
	}
	for _, k := range keys {
		if k.Name == name {
			return k, true
		}
	}
	return KiteSshPublicKey{}, false
}

// GenerateMySshPublicKey asks the server to generate a new SSH key pair for
// the current user. The returned PrivateKey is only available here — store it
// before discarding the response. passphrase may be empty.
func (K KWSession) GenerateMySshPublicKey(name, passphrase string) (pair KiteSshKeyPair, err error) {
	body := PostJSON{"name": name}
	if !IsBlank(passphrase) {
		body["passphrase"] = passphrase
	}
	err = K.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/userSshPublicKeys/generate",
		Params: SetParams(body),
		Output: &pair,
	})
	return
}

// DeleteMySshPublicKey deletes the SSH public key with the given id from the
// current user's account.
func (K KWSession) DeleteMySshPublicKey(id int) (err error) {
	return K.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/userSshPublicKeys/%d", id),
	})
}

// UserCount returns the number of users matching the provided email addresses or parameters.
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

// GetUsers represents a process for retrieving user data.
// It handles pagination, filtering, and error tracking.
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

// Failed returns the number of users that failed to be retrieved.
func (T *GetUsers) Failed() int {
	return T.failed_users
}

// Total returns the total number of users.
func (T *GetUsers) Total() int {
	return T.user_total
}

// Users returns a list of users based on provided emails and filters.
// It retrieves user data, applies filters based on profile ID,
// and returns a GetUsers struct containing the results.
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
		//if len(emails) <= 100 || profile_id > 0 {
		T.emails = emails
		//}
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

// Next retrieves the next batch of users.
// Returns an empty slice and nil error when no more users are available.
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
			if len(T.email_map) > 0 {
				var user_list []string
				for v, _ := range T.email_map {
					user_list = append(user_list, v)
				}
				slices.Sort(user_list)
				for _, v := range user_list {
					Err("%s: User not found, or did not meet specified criteria.", v)
				}
			}
			break
		}
	}
	return
}

// findEmails retrieves users based on a list of email addresses.
// It iterates through the email list, queries the API for each email,
// filters the results, and appends the filtered users to the result.
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

// Filters a list of KiteUser based on the provided filter criteria.
// It iterates through the filter map and applies boolean filters to the user list.
// Returns the filtered list of users and a potential error.
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
	for _, v := range input {
		if _, ok := T.email_map[strings.ToLower(v.Email)]; ok {
			delete(T.email_map, strings.ToLower(v.Email))
		}
	}
	return input, nil
}

// KiteRoles represents a role within the Kite system.
// It encapsulates information such as ID, name, rank, type, and links.
type KiteRoles struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Rank  int    `json:"rank"`
	Type  string `json:"type"`
	Links string `json:"links"`
}

// Roles retrieves a list of Kite roles.
// Returns a slice of KiteRoles and an error, if any.
func (K KWSession) Roles() (output []KiteRoles, err error) {
	var Roles struct {
		Out []KiteRoles `json:"data"`
	}

	err = K.Call(APIRequest{
		Method: "GET",
		Path:   "/rest/roles",
		Output: &Roles,
	})
	output = Roles.Out
	return
}

// folderCrawler recursively crawls a folder and processes files.
// It uses a processor function to handle each KiteObject.
// A folder_limiter controls concurrent folder access.
// file_chan sends KiteObjects to be processed.
// all_stop signals the crawler to halt.
type folderCrawler struct {
	processor      func(*KWSession, *KiteObject) error
	folder_limiter LimitGroup
	file_chan      chan *KiteObject
	all_stop       int32
}

// abortError represents an error that signals an operation should abort.
// It wraps another error to provide context.
type abortError struct {
	err error
}

// AbortError returns an abortError for the given error.
func AbortError(err error) abortError {
	return abortError{
		err: err,
	}
}

// Error returns the error message if an underlying error exists.
func (e abortError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return ""
}

// abortCheck returns the error and a boolean indicating if the
// processing should be aborted.
// It checks if the given error is an abortError and returns the
// underlying error and a true value if it is. Otherwise, it returns
// the original error and false.
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

// FolderCrawler recursively crawls through the given folders, processing
// each KiteObject with the provided processor function. It limits
// concurrency for both folder and file processing to prevent resource
// exhaustion. The crawling stops if the processor returns an error
// or signals an abort condition.
func (K *KWSession) FolderCrawler(processor func(*KWSession, *KiteObject) error, folders ...KiteObject) {
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
				}(K, m)
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
	}(K)

	for _, f := range folders {
		crawler.folder_limiter.Add(1)
		go func(user *KWSession, folder KiteObject) {
			defer crawler.folder_limiter.Done()
			crawler.process(K, &folder)
		}(K, f)
	}
	crawler.folder_limiter.Wait()
	crawler.file_chan <- nil
	file_limiter.Wait()
	return
}

// Recursively crawls a folder and processes its contents.
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
			/*
				folders, err := user.Folder(folders[n].ID).Folders()
				if err == nil {
					for _, f := range folders {
						next = append(next, &f)
					}
				}
				files, err := user.Folder(folders[n].ID).Files()
				if err == nil {
					for _, f := range files {
						F.file_chan <- &f
					}
				}
			*/

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

// KWAdminRole represents an admin role within the Kiteworks system.
type KWAdminRole struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	GUID  string `json:"guid"`
	Links string `json:"links"`
}

/* How AdminRoles should function

// AdminRoles retrieves a map of KWAdminRoles indexed by their ID.
// It fetches admin roles via a data call and returns them as a map.
func (K KWSession) AdminRoles() (output map[int]KWAdminRole, err error) {
	var admin_roles []KWAdminRole
	err = K.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/adminRoles"),
		Output: &admin_roles,
	}, -1, 1000)
	if err != nil {
		return nil, err
	}
	output = make(map[int]KWAdminRole)
	for _, v := range admin_roles {
		output[v.ID] = v
	}
	return
}

*/

func (K KWSession) AdminRoles() (output map[int]KWAdminRole, err error) {
	var admin_roles struct {
		Data []KWAdminRole `json:"data"`
	}
	err = K.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/adminRoles"),
		Output: &admin_roles,
	})
	if err != nil {
		return nil, err
	}
	output = make(map[int]KWAdminRole)
	for _, v := range admin_roles.Data {
		output[v.ID] = v
	}
	return
}

// kw_profile represents a collection of KWProfiles, indexed by ID.
type kw_profile struct {
	profile_map map[int]KWProfile
}

// Find searches for a profile by its name (case-insensitive).
// It returns the profile pointer and nil error if found,
// otherwise returns nil profile and an error.
func (K kw_profile) Find(name string) (profile *KWProfile, err error) {
	for _, v := range K.profile_map {
		if strings.ToLower(name) == strings.ToLower(v.Name) {
			return &v, nil
		}
	}

	return nil, fmt.Errorf("No profile with name '%s' found.", name)
}

// Get retrieves a profile by its ID.
// Returns the profile if found, otherwise returns an error.
func (K kw_profile) Get(id int) (profile *KWProfile, err error) {
	if v, ok := K.profile_map[id]; ok {
		return &v, nil
	} else {
		return nil, fmt.Errorf("No profile with id '%d' found.", id)
	}
}

// KWProfile represents a profile with associated features.
type KWProfile struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Features struct {
		AllowSFTP    bool  `json:"allowSftp"`
		MaxStorage   int64 `json:"maxStorage"`
		SendExternal bool  `json:"sendExternal"`
		FolderCreate int   `json:"folderCreate"`
		FileTime     int   `json:"fileLifetime"`
		FolderTime   int   `json:"folderExpirationLimit"`
	} `json:"features"`
}

// KWProfileFeatures models the full features object of a Kiteworks profile as
// returned by GET /rest/profiles/{id} and accepted by PUT /rest/profiles/{id}.
// All fields are pointers so an update can send only the values that were read
// back (a nil field is omitted, avoiding ERR_INPUT_* on read-only/absent keys).
type KWProfileFeatures struct {
	AllowSFTP                        *bool     `json:"allowSftp,omitempty"`
	MaxStorage                       *int64    `json:"maxStorage,omitempty"`
	LinkExpiration                   *int      `json:"linkExpiration,omitempty"`
	MaxLinkExpiration                *int      `json:"maxLinkExpiration,omitempty"`
	SetExpirationLower               *bool     `json:"setExpirationLower,omitempty"`
	SendExternal                     *bool     `json:"sendExternal,omitempty"`
	AcNoAuth                         *bool     `json:"acNoAuth,omitempty"`
	FolderCreate                     *int      `json:"folderCreate,omitempty"`
	AllowedFolderRoles               *[]string `json:"allowedFolderRoles,omitempty"`
	SysFolderCreate                  *int      `json:"sysFolderCreate,omitempty"`
	SysFolderMaxQuota                *int64    `json:"sysFolderMaxQuota,omitempty"`
	SysFolderMaxCount                *int64    `json:"sysFolderMaxCount,omitempty"`
	SysFolderDefaultQuota            *int64    `json:"sysFolderDefaultQuota,omitempty"`
	StorageQuota                     *int64    `json:"storageQuota,omitempty"`
	LdapMapping                      *string   `json:"ldapMapping,omitempty"`
	AcVerifyRecipient                *bool     `json:"acVerifyRecipient,omitempty"`
	Acl                              *[]string `json:"acl,omitempty"`
	DefaultAcl                       *string   `json:"defaultAcl,omitempty"`
	MobileSyncItemsLimit             *int      `json:"mobileSyncItemsLimit,omitempty"`
	PersonalFolder                   *bool     `json:"personalFolder,omitempty"`
	RequireScanZipContentDefault     *bool     `json:"requireScanZipContentDefault,omitempty"`
	BlockNewFileTypesDefault         *bool     `json:"blockNewFileTypesDefault,omitempty"`
	ExcludedFileExtensions           *[]string `json:"excludedFileExtensions,omitempty"`
	FileFilterExclusionGroups        *[]string `json:"fileFilterExclusionGroups,omitempty"`
	FileFilterCustomFileTypes        *[]string `json:"fileFilterCustomFileTypes,omitempty"`
	SecureMessageBody                *string   `json:"secureMessageBody,omitempty"`
	SecureMessageBodyDefault         *bool     `json:"secureMessageBodyDefault,omitempty"`
	SecureContainerRequired          *bool     `json:"secureContainerRequired,omitempty"`
	ReturnReceipt                    *string   `json:"returnReceipt,omitempty"`
	ReturnReceiptDefault             *bool     `json:"returnReceiptDefault,omitempty"`
	SelfCopy                         *string   `json:"selfCopy,omitempty"`
	SelfCopyDefault                  *bool     `json:"selfCopyDefault,omitempty"`
	IncludeFingerprint               *string   `json:"includeFingerprint,omitempty"`
	IncludeFingerprintDefault        *bool     `json:"includeFingerprintDefault,omitempty"`
	RequestFile                      *bool     `json:"requestFile,omitempty"`
	RequestFileAllowViewableFile     *bool     `json:"requestFileAllowViewableFile,omitempty"`
	RequestFileUploadAuth            *string   `json:"requestFileUploadAuth,omitempty"`
	RequestFileAuthDefault           *string   `json:"requestFileAuthDefault,omitempty"`
	RequestFileExpiration            *int      `json:"requestFileExpiration,omitempty"`
	RequestFileExpirationUserDecide  *bool     `json:"requestFileExpirationUserDecide,omitempty"`
	RequestFileExpirationMax         *int      `json:"requestFileExpirationMax,omitempty"`
	RequestFileUploadLimit           *int      `json:"requestFileUploadLimit,omitempty"`
	RequestFileUploadLimitUserDecide *bool     `json:"requestFileUploadLimitUserDecide,omitempty"`
	RequestFileUploadsMax            *int      `json:"requestFileUploadsMax,omitempty"`
	TwoFactorAuth                    *string   `json:"twoFactorAuth,omitempty"`
	InactiveExpiration               *int      `json:"inactiveExpiration,omitempty"`
	UserCanReactivate                *string   `json:"userCanReactivate,omitempty"`
	CleanupInactiveAccount           *bool     `json:"cleanupInactiveAccount,omitempty"`
	WithdrawInactiveAccountFileLinks *bool     `json:"withdrawInactiveAccountFileLinks,omitempty"`
	AllowCollaboration               *bool     `json:"allowCollaboration,omitempty"`
	AllowLeavingSharedFolder         *bool     `json:"allowLeavingSharedFolder,omitempty"`
	SendFileLimit                    *int      `json:"sendFileLimit,omitempty"`
	RemoteWipe                       *bool     `json:"remoteWipe,omitempty"`
	DeleteUnsharedData               *bool     `json:"deleteUnsharedData,omitempty"`
	RetainData                       *bool     `json:"retainData,omitempty"`
	RetainPermissionToSharedData     *bool     `json:"retainPermissionToSharedData,omitempty"`
	FolderExpirationLimit            *int      `json:"folderExpirationLimit,omitempty"`
	FileLifetime                     *int      `json:"fileLifetime,omitempty"`
}

// KWFullProfile is the full profile object from GET /rest/profiles/{id}. Unlike
// the compact KWProfile used by MapProfiles (which reads /rest/admin/profiles),
// this targets /rest/profiles where every setting is present — the shape needed
// to clone a profile faithfully. Prototype is a *int because a built-in profile
// reports it as null.
type KWFullProfile struct {
	ID        int               `json:"id"`
	Name      string            `json:"name"`
	Prototype *int              `json:"prototype"`
	BuiltIn   int               `json:"builtIn"`
	Cloneable int               `json:"cloneable"`
	Features  KWProfileFeatures `json:"features"`
}

// FullProfiles lists all profiles with their complete feature set via
// GET /rest/profiles.
func (K KWSession) FullProfiles() (profiles []KWFullProfile, err error) {
	err = K.DataCall(APIRequest{
		Method: "GET",
		Path:   "/rest/profiles",
		Output: &profiles,
	}, -1, 1000)
	return
}

// GetProfile returns a single profile with its full feature set via
// GET /rest/profiles/{id}.
func (K KWSession) GetProfile(id int) (profile KWFullProfile, err error) {
	err = K.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/profiles/%d", id),
		Output: &profile,
	})
	return
}

// NewProfile creates a custom profile cloned from a built-in prototype and
// returns the created entity. Per the API, this only establishes the profile
// (name + prototype); its configuration is applied separately via UpdateProfile.
func (K KWSession) NewProfile(name string, prototype int, params ...interface{}) (profile KWFullProfile, err error) {
	err = K.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/profiles",
		Params: SetParams(PostJSON{"name": name, "prototype": prototype}, Query{"returnEntity": true}, params),
		Output: &profile,
	})
	return
}

// FeaturesToMap flattens a KWProfileFeatures into a top-level map, preserving
// pointer/omitempty semantics (nil fields drop out entirely). Exported for
// callers that need to compare a saved profile against fields they submitted.
func FeaturesToMap(features KWProfileFeatures) (map[string]interface{}, error) {
	return featuresToMap(features)
}

// featuresToMap flattens a KWProfileFeatures into a top-level map, preserving
// pointer/omitempty semantics (nil fields drop out entirely).
func featuresToMap(features KWProfileFeatures) (map[string]interface{}, error) {
	raw, err := json.Marshal(features)
	if err != nil {
		return nil, err
	}
	m := make(map[string]interface{})
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// readOnlyProfileFeatures are feature fields present in the GET response but NOT
// accepted by the PUT /rest/profiles/{id} body (per the API schema). They are
// derived/output-only or managed elsewhere, so sending them has no effect and
// they must not count as a difference during cloning. Kept in sync with the
// documented PUT request body: any GET field absent from that body belongs here.
//   - acl/defaultAcl: computed from acNoAuth/acVerifyRecipient + system settings
//   - allowedFolderRoles, personalFolder, mobileSyncItemsLimit, *Default scan
//     flags, folderExpirationLimit, fileLifetime, allowLeavingSharedFolder:
//     not writable via this endpoint.
var readOnlyProfileFeatures = map[string]struct{}{
	"acl":                          {},
	"defaultAcl":                   {},
	"allowedFolderRoles":           {},
	"allowLeavingSharedFolder":     {},
	"mobileSyncItemsLimit":         {},
	"personalFolder":               {},
	"requireScanZipContentDefault": {},
	"blockNewFileTypesDefault":     {},
	"folderExpirationLimit":        {},
	"fileLifetime":                 {},
}

// FeaturesDiff returns the subset of desired feature fields that differ from
// current. Cloning sends only these changed fields (at the top level, matching
// the FeaturesList schema) so we never resend fields that already match — which
// is what avoids tripping built-in profiles' conditional per-field validation
// (allowed_if / not_in_list) on fields that don't actually need to change.
// Read-only/derived fields (see readOnlyProfileFeatures) are never included.
func FeaturesDiff(current, desired KWProfileFeatures) (PostJSON, error) {
	cur, err := featuresToMap(current)
	if err != nil {
		return nil, err
	}
	des, err := featuresToMap(desired)
	if err != nil {
		return nil, err
	}
	diff := make(PostJSON)
	for k, dv := range des {
		if _, ro := readOnlyProfileFeatures[k]; ro {
			continue
		}
		if cv, ok := cur[k]; !ok || !reflect.DeepEqual(cv, dv) {
			diff[k] = dv
		}
	}
	return diff, nil
}

// UpdateProfile applies a features configuration to an existing profile via
// PUT /rest/profiles/{id}. The body is the FeaturesList object: feature fields
// are sent at the TOP LEVEL (not wrapped in a "features" key). nil fields on
// KWProfileFeatures are omitted so only known values are sent.
func (K KWSession) UpdateProfile(id int, features KWProfileFeatures, params ...interface{}) (err error) {
	body, err := featuresToMap(features)
	if err != nil {
		return err
	}
	_, err = K.UpdateProfileFields(id, PostJSON(body), params...)
	return err
}

// UpdateProfileFields applies an arbitrary set of top-level feature fields to a
// profile via PUT /rest/profiles/{id}, returning the saved profile (the
// endpoint echoes the full entity via returnEntity=true). Used with
// FeaturesDiff to send only the fields that changed and to verify they stuck.
func (K KWSession) UpdateProfileFields(id int, body PostJSON, params ...interface{}) (saved KWFullProfile, err error) {
	err = K.Call(APIRequest{
		Method: "PUT",
		Path:   SetPath("/rest/profiles/%d", id),
		Params: SetParams(body, Query{"returnEntity": true}, params),
		Output: &saved,
	})
	return
}

// DeleteProfileReplace deletes a custom profile and reassigns its users to
// new_profile_id via DELETE /rest/profiles/{id}/replace/{new_profile}. params
// may carry UserDemoteOptions (retainData / deleteUnsharedData / retainToUser).
func (K KWSession) DeleteProfileReplace(id, new_profile_id int, params ...interface{}) (err error) {
	return K.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/profiles/%d/replace/%d", id, new_profile_id),
		Params: SetParams(params),
	})
}

// Profiles retrieves a map of KWProfiles indexed by their ID.
// It fetches profiles via a data call and returns them as a map.
func (K KWSession) Profiles() (output map[int]KWProfile, err error) {
	var profiles []KWProfile
	err = K.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/profiles"),
		Output: &profiles,
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

// dli_admin encapsulates admin-specific functionality.
// It extends the KWSession type with administrative features.
type dli_admin struct {
	*KWSession
}

// DLIAdmin returns a DLIAdmin instance associated with the KWSession.
func (K KWSession) DLIAdmin() dli_admin {
	return dli_admin{
		&K,
	}
}

// ActivityCount returns the number of activities for a given user
// within a specified number of days.
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

const (
	DLI_Activities = 1
	DLI_Files      = 2
	DLI_Emails     = 3
)

// DLIRequest represents a Data Leak Investigator request.
// It encapsulates details about the export, including its ID,
// date range, status, and download information.
type DLIRequest struct {
	ID            string `json:"id"`
	StartDate     string `json:"startDate"`
	EndDate       string `json:"endDate"`
	Status        string `json:"status"`
	DownloadURL   string `json:"downloadURL"`
	Type          string `json:"type"`
	UserID        string `json:"userId"`
	GeneratedDate string `json:"generatedDate"`
	Filename      string `json:"fileName"`
	Links         string `json:"links"`
}

// DownloadExport downloads an export file to the specified local path.
// It first checks the export status and returns an error if it's in progress or has an error.
// It then creates the local path if it doesn't exist and downloads the file,
// resuming if an incomplete file already exists.
func (K dli_admin) DownloadExport(request *DLIRequest, file_name, local_path string) (err error) {
	status, err := K.CheckExport(request)
	if err != nil {
		return err
	}
	if status == "inprocess" {
		return fmt.Errorf("Export not ready.")
	}
	if status == "error" {
		return fmt.Errorf("Server error on export.")
	}

	if err = MkDir(local_path); err != nil {
		return
	}

	fname := fmt.Sprintf("%s%s%s", local_path, SLASH, request.Filename)
	tmpname := fname + ".incomplete"

	fstat, err := os.Stat(tmpname)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	req, err := K.NewRequest("GET", SetPath("/rest/dli/exports/%s", request.ID))
	if err != nil {
		return err
	}

	dst, err := os.OpenFile(tmpname, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return
	}
	defer dst.Close()

	src := K.WebDownload(req)
	defer src.Close()
	if fstat != nil {
		offset, err := dst.Seek(fstat.Size(), 0)
		if err != nil {
			return err
		}
		_, err = src.Seek(offset, 0)
		if err != nil {
			return err
		}
	}

	_, err = io.Copy(dst, src)

	return
}

// CheckExport checks the status of a DLI export request.
// It returns the export status string and any error encountered.
func (K dli_admin) CheckExport(request *DLIRequest) (string, error) {
	var DLIStatus struct {
		Status string `json:"status"`
	}

	err := K.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/dli/exports/%s", request.ID),
		Params: SetParams(Query{"id": request.ID}),
		Output: &DLIStatus,
	})
	if err != nil {
		return "unknown", err
	}
	request.Status = DLIStatus.Status
	return DLIStatus.Status, nil
}

// GenerateReport generates a DLI report for a given user, based on specified types and date range.
// It returns a slice of DLIRequests and any error encountered during the process.
func (K dli_admin) GenerateReport(user KiteUser, activities, files, emails bool, start_date, end_date time.Time) (requests []DLIRequest, err error) {
	if !activities && !files && !emails {
		return nil, fmt.Errorf("No report type specified.")
	}

	var report_types []string

	if activities {
		report_types = append(report_types, "activities")
	}

	if files {
		report_types = append(report_types, "files")
	}

	if emails {
		report_types = append(report_types, "emails")
	}

	err = K.DataCall(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/dli/exports/users/%v", user.ID),
		Params: SetParams(PostJSON{"startDate": WriteKWTime(start_date), "endDate": WriteKWTime(end_date), "types": report_types}, Query{"returnEntity": true}),
		Output: &requests,
	}, -1, 1000)
	if err != nil {
		return
	}
	return
}

// DeleteExport deletes an export with the given ID.
// It returns an error if the deletion fails.
func (K dli_admin) DeleteExport(id string) (err error) {
	return K.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/dli/exports/%s", id),
	})

}

// CheckForActivity determines if a user has had any activity within a specified number of days.
// It checks for activity reports and returns true if found, false otherwise.
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
}
