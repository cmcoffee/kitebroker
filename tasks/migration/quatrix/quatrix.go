package quatrix

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	. "github.com/cmcoffee/kitebroker/core"
	"github.com/cmcoffee/snugforge/nfo"
)

func init() { RegisterMigrationTask(new(QuatrixMigrationTask)) }

// QuatrixMigrationTask represents a task for migrating data from Quatrix to Kiteworks. It extends the KiteBrokerTask struct, inheriting its properties and methods.
// The structure includes variables and configuration settings for interacting with Quatrix, as well as various maps and locks for managing concurrent operations.
type QuatrixMigrationTask struct {
	KiteBrokerTask
	quatrix_url         string
	quatrix_api_key     string
	quatrix_db          Database
	quatrix_config      Table
	qsess               *QSession
	users_created       map[string]any
	failed_users        map[string]any
	FolderCount         Tally
	UserCount           Tally
	FileCount           Tally
	FileTransferred     Tally
	Transferred         Tally
	FailedUsers         Tally
	target_profile_name string
	target_profile_id   int
	failed_lock         sync.RWMutex
	list_perm           string
	qfolders            struct {
		paths    map[string]string
		home_ids map[string]any
		lock     sync.RWMutex
	}
	user_emails []string
	report      bool
	report_data struct {
		users map[string]*userReport
		lock  sync.Mutex
	}
}

func (T *QuatrixMigrationTask) ignoreUser(username string) bool {
	T.failed_lock.RLock()
	defer T.failed_lock.RUnlock()
	return T.failed_users[username] != nil
}

func (T *QuatrixMigrationTask) setIgnoreUser(username string) {
	T.failed_lock.Lock()
	T.failed_users[username] = nil
	T.failed_lock.Unlock()
}

// auxConfig holds configuration for specific migrations.
var auxConfig struct {
	setOptions  func(*nfo.Options, *string)
	saveConfig  func(string)
	loadConfig  func() string
	setDatabase func(Database)
	apiConfig   func(setup *APIClient)
	setCustom   func(string, *KiteUser) (err error)
	setAdmin    func(string)
}

// quatrixUserCloner is a per-user wrapper around QuatrixMigrationTask that holds
// a user-scoped folder_map. This avoids cross-user cache collisions when
// two users have identically-named folder paths, and allows the map to be
// garbage-collected once the user migration is complete.
type quatrixUserCloner struct {
	*QuatrixMigrationTask
	folder_map map[string]*KiteObject
	lock       sync.RWMutex
	username   string
}

func filterInvalidChars(input string) string {
	var output []rune
	for _, v := range input {
		switch v {
		case '\\':
			fallthrough
		case '*':
			fallthrough
		case '?':
			fallthrough
		case '"':
			fallthrough
		case '<':
			fallthrough
		case '>':
			fallthrough
		case '|':
			output = append(output, []rune(url.QueryEscape(string(v)))...)
		default:
			output = append(output, v)
		}
	}
	return string(output)
}

// ResolvePath resolves the Kite object path for a given object.
// It retrieves the object's info, constructs the path, filters
// invalid characters, and returns the corresponding Kite object.
// It uses a per-user folder map to cache resolved paths.
func (U *quatrixUserCloner) ResolvePath(obj *QObject) (*KiteObject, error) {
	U.RegisterFolder(obj)
	path := U.Path(obj)

	split := strings.Split(path, "/")
	if obj.Type == "F" {
		path = strings.Join(split[:len(split)-1], "/")
	} else {
		path = strings.Join(split, "/")
	}

	path = filterInvalidChars(path)

	U.lock.RLock()
	if folder, found := U.folder_map[path]; found {
		U.lock.RUnlock()
		return folder, nil
	}
	startFolder := "0"
	resolvePath := path

	if folder, found := U.folder_map[strings.Join(split[0:len(split)-1], "/")]; found {
		startFolder = folder.ID
		resolvePath = obj.Name
	}
	U.lock.RUnlock()

	folder, err := U.KW.Session(U.username).Folder(startFolder).ResolvePath(resolvePath)
	if err != nil {
		return nil, fmt.Errorf("Error resolving path %s for user '%s': %v", resolvePath, U.username, err)
	}

	U.lock.Lock()
	U.folder_map[path] = &folder
	U.lock.Unlock()
	return &folder, nil
}

// Name returns the name of this task, which is "quatrix". This method should be used to identify the specific instance of a task in a collection.
func (T *QuatrixMigrationTask) Name() string {
	return "quatrix"
}

// Desc returns a string describing the task, which is "Migrates users and folders from Quatrix to Kiteworks."
func (T *QuatrixMigrationTask) Desc() string {
	return "Migrate users, folders, files, permissions from Quatrix to Kiteworks."
}

// configure_api configures the Quatrix API client with necessary settings.
// It sets up the server URL, SSL verification, proxy, and API key,
// and initializes the database and session.
func (T *QuatrixMigrationTask) configure_api() (err error) {
	quatrix_api := new(APIClient)
	quatrix_api.Server = T.quatrix_url
	quatrix_api.VerifySSL = true
	quatrix_api.ProxyURI = T.KW.ProxyURI
	T.quatrix_db.Drop("tokens")

	var token string
	if found := T.quatrix_config.Get("api_key", &token); !found {
		return fmt.Errorf("No API Key found.")
	}
	auth := &Auth{
		AccessToken: token,
		Expires:     99999999999999,
	}
	quatrix_api.NewToken = func(username string) (*Auth, error) {
		return auth, nil
	}
	quatrix_api.SetDatabase(T.quatrix_db)
	quatrix_api.ReaquireToken = false
	quatrix_api.MaxChunkSize = T.KW.MaxChunkSize

	quatrix_api.SetLimiter(T.KW.GetLimit())
	quatrix_api.SetTransferLimiter(T.KW.GetTransferLimit())
	quatrix_api.RequestTimeout = T.KW.RequestTimeout
	quatrix_api.ConnectTimeout = T.KW.ConnectTimeout
	qapi := &QAPI{quatrix_api}
	T.qsess, err = qapi.Session()
	if auxConfig.apiConfig != nil {
		auxConfig.setAdmin(T.KW.Username)
		auxConfig.apiConfig(T.KW.APIClient)
	}
	return
}

// Init initializes the Quatrix migration task by setting up the database
// connection, retrieving configuration, and preparing necessary maps.
func (T *QuatrixMigrationTask) Init() (err error) {
	T.quatrix_db = T.DB.Sub("quatrix")
	T.quatrix_config = T.quatrix_db.Table("quatrix_config")
	if auxConfig.setDatabase != nil {
		auxConfig.setDatabase(T.quatrix_db.Sub("AuxConfig"))
	}

	// Attempt to retrieve configuration from the database
	T.quatrix_config.Get("host_url", &T.quatrix_url)
	T.quatrix_config.Get("api_key", &T.quatrix_api_key)
	T.quatrix_config.Get("list_perm", &T.list_perm)
	if IsBlank(T.list_perm) {
		T.list_perm = "ignore"
	}

	T.qfolders.paths = make(map[string]string)
	T.qfolders.home_ids = make(map[string]any)

	// Configure Quatrix if setup is requested or configuration is missing
	if err := T.configureQuatrix(); err != nil {
		return err
	}

	return nil
}

// configureQuatrix handles Quatrix configuration, either through flags or interactive prompt.
func (T *QuatrixMigrationTask) configureQuatrix() (err error) {
	setup := T.Flags.Bool("setup", "Configure Quatrix Connection")
	T.Flags.StringVar(&T.target_profile_name, "profile", "Standard", "Destination profile for migrated users. (Needs permission to create folders)")
	T.Flags.MultiVar(&T.user_emails, "users", "<user@domain.com>", "User(s) to migrate.")
	migrate := T.Flags.Bool("migrate", "Perform the actual migration.")
	T.Flags.BoolVar(&T.report, "report", "Generate a report of Quatrix users, folders and files.")
	T.Flags.Order("migrate", "report")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	if *migrate && T.report {
		return fmt.Errorf("--migrate and --report are mutually exclusive, please specify only one")
	}

	if !*migrate && !T.report && !*setup {
		return fmt.Errorf("must specify either --migrate or --report")
	}

	var customSetting string
	if auxConfig.loadConfig != nil {
		customSetting = auxConfig.loadConfig()
	}

	if *setup || T.quatrix_url == "" || T.quatrix_api_key == "" {
		quatrix_auth := NewOptions("--- Quatrix API Configuration ---", "(selection or 'q' to save & exit)", 'q')
		quatrix_auth.StringVar(&T.quatrix_url, "Quatrix Host Name", T.quatrix_url, "Please input the FQDN of your Quatrix host.")
		quatrix_auth.SecretVar(&T.quatrix_api_key, "Quatrix API Key", T.quatrix_api_key, "Please input the API key associated with the quatrix admin account.")
		quatrix_auth.StringSelectVar(&T.list_perm, "List View Permission", T.list_perm, "ignore", "viewer", "downloader")
		if auxConfig.setOptions != nil {
			auxConfig.setOptions(quatrix_auth, &customSetting)
		}
		if quatrix_auth.Select(false) {
			T.quatrix_config.Set("host_url", &T.quatrix_url)
			T.quatrix_config.CryptSet("api_key", &T.quatrix_api_key)
			T.quatrix_config.CryptSet("list_perm", &T.list_perm)
			if auxConfig.saveConfig != nil {
				auxConfig.saveConfig(customSetting)
			}
		}
		Exit(0)
	}
	return nil
}

// MapPermissions maps Quatrix permissions to Kiteworks permissions.
// It translates Quatrix permission flags to corresponding Kiteworks values.
// Returns the Kiteworks permission value based on the Quatrix flags.
func (T *QuatrixMigrationTask) MapPermissions(quatrix_perms int64) (kwperm int) {
	var folder_perms = map[string]int{
		"downloader":   2,
		"collaborator": 3,
		"manager":      4,
		"viewer":       6,
		"uploader":     7,
	}
	x := BitFlag(quatrix_perms)

	switch {
	case x == QP_LIST:
		switch T.list_perm {
		case "viewer":
			fallthrough
		case "downloader":
			return folder_perms[T.list_perm]
		default:
			return 0
		}
	case x.Has(QP_PREVIEW) && !x.Has(QP_DOWNLOAD):
		return folder_perms["viewer"]
	case x.Has(QP_DOWNLOAD) && !x.Has(QP_UPLOAD):
		return folder_perms["downloader"]
	case x.Has(QP_UPLOAD) && !x.Has(QP_DOWNLOAD):
		return folder_perms["uploader"]
	case x.Has(QP_MANAGE):
		return folder_perms["manager"]
	case x.Has(QP_UPLOAD) && x.Has(QP_DOWNLOAD):
		return folder_perms["collaborator"]
	default:
		return 0 // Or a more appropriate default value if needed
	}
}

// Path returns the path associated with the given folder.
// It retrieves the path from a read-locked map of folder IDs to paths.
func (T *QuatrixMigrationTask) Path(folder *QObject) string {
	T.qfolders.lock.RLock()
	defer T.qfolders.lock.RUnlock()
	return T.qfolders.paths[folder.ID]
}

func (T *QuatrixMigrationTask) RegisterFolder(folder *QObject) {
	T.qfolders.lock.Lock()
	defer T.qfolders.lock.Unlock()
	if _, ok := T.qfolders.home_ids[folder.ID]; ok {
		T.qfolders.paths[folder.ID] = folder.Name
	} else {
		path := fmt.Sprintf("%s/%s", T.qfolders.paths[folder.ParentID()], folder.Name)
		T.qfolders.paths[folder.ID] = path
	}
}

// CloneFolder clones a Quatrix folder and its contents to Kiteworks.
// It handles files and subfolders, including permissions.
func (U *quatrixUserCloner) CloneFolder(folder *QObject) (err error) {
	if folder == nil {
		return fmt.Errorf("Folder error, nil folder object provided.")
	}

	p := func(sess *QSession, obj *QObject) error {
		if obj.IsSystemFolder() {
			return nil
		}
		folder, err := U.ResolvePath(obj)
		if err != nil {
			return err
		}
		switch obj.Type {
		case "F":
			U.FileCount.Add(1)
			dl, err := obj.Download()
			if err != nil {
				return err
			}
			defer dl.Close()
			x := TransferCounter(dl, U.Transferred.Add)
			z, err := U.KW.Session(U.username).Upload(filterInvalidChars(obj.Name), obj.Size, time.Unix(obj.ModTime, 0), false, false, true, *folder, x)
			if err != nil {
				return err
			}
			if z != nil {
				U.FileTransferred.Add(1)
			}
		case "D":
			U.FolderCount.Add(1)
			Log("[%s]: Quatrix: %s -> Kiteworks: /%s", U.username, U.Path(obj), folder.Path)
		case "S":
			U.FolderCount.Add(1)
			Log("[%s]: Quatrix: %s -> Kiteworks: /%s", U.username, U.Path(obj), folder.Path)
			if perms, err := obj.Permissions(); err == nil {
				perm_map := make(map[int][]string)
				var counter int
				for _, p := range perms.Users {
					if p.Email == U.username {
						continue
					}
					email := strings.ToLower(p.Email)
					perm_id := U.MapPermissions(p.Operations)
					perm_map[perm_id] = append(perm_map[perm_id], email)
					counter++
				}
				existingPerms, _ := U.KW.Session(U.username).Folder(folder.ID).Members()
				counter -= SkipExistingPerms(perm_map, existingPerms)
				if counter > 0 {
					Log("[%s]: %s - Adding %d permissions to folder.", U.username, folder.Path, counter)
				}
				for k, v := range perm_map {
					if k == 0 {
						for _, email := range v {
							for _, kw_perm := range existingPerms {
								if strings.ToLower(kw_perm.User.Email) == strings.ToLower(email) {
									Log("[%s]: Removing user from %s.", U.username, folder.Path)
									removeUser, err := U.KW.Admin().FindUser(email)
									if err != nil {
										Err("Error removing user (%s) from folder %s: %v", email, folder.Name, err)
										continue
									}
									err = U.KW.Session(U.username).Folder(folder.ID).RemoveUserFromFolder(removeUser.ID, Query{"downgradeNested": true})
									if err != nil {
										Err("Error removing user (%s) from folder %s: %v", email, folder.Name, err)
										continue
									}
								}
							}
						}
					} else {
						err = U.KW.Session(U.username).Folder(folder.ID).AddUsersToFolder(v, k, false, true)
						if err != nil && !IsAPIError(err, "ERR_ENTITY_ROLE_IS_ASSIGNED") {
							Err("Error adding user(s) to folder %s: %v", folder.Name, err)
						}
					}
				}
			} else {
				if IsAPIError(err, "QUATRIX_CODE_20") {
					return nil
				}
				return err
			}
		}

		return nil
	}
	U.qsess.FolderCrawler(p, folder)
	return nil
}

// CloneUser clones a user's folders to the new system.
// It creates a per-user cloner with its own folder_map, iterates through
// the user's folders, and clones each non-system folder.
func (T *QuatrixMigrationTask) CloneUser(user *Userdata) (err error) {
	T.UserCount.Add(1)
	username := strings.ToLower(user.Email)

	kw_user, err := T.KW.Admin().FindUser(username)
	if err != nil {
		return fmt.Errorf("[%s]: Error looking up user: %v", username, err)
	}
	if kw_user.UserTypeID != T.target_profile_id {
		err = T.KW.Admin().UpdateUserProfile(T.target_profile_id, []string{kw_user.ID})
		if err != nil {
			return err
		}
	}

	// Create a per-user cloner with its own folder_map.
	cloner := &quatrixUserCloner{
		QuatrixMigrationTask: T,
		folder_map:           make(map[string]*KiteObject),
		username:             username,
	}

	user_folders, err := T.qsess.File(user.HomeID)
	if err != nil {
		return err
	}
	for _, f := range user_folders.Content {
		if f.IsSystemFolder() {
			continue
		}
		f, err := T.qsess.File(f.ID)
		if err != nil {
			Err("Error processing user %s: %v", username, err)
			continue
		}
		err = cloner.CloneFolder(&f)
		if err != nil {
			Err("Error cloning folder %s:  %v", f.Name, err)
		}
	}
	return nil
}

// Main executes the Quatrix migration process, handling user creation, folder synchronization, and file transfer operations.
func (T *QuatrixMigrationTask) Main() (err error) {
	err = T.configure_api()
	if err != nil {
		return err
	}

	all_users, err := T.qsess.Users()
	if err != nil {
		return err
	}

	var users []Userdata
	if len(T.user_emails) > 0 {
		filter := make(map[string]struct{})
		for _, e := range T.user_emails {
			filter[strings.ToLower(e)] = struct{}{}
		}
		for _, u := range all_users {
			if _, ok := filter[strings.ToLower(u.Email)]; ok {
				users = append(users, u)
			}
		}
	} else {
		users = all_users
	}

	for _, u := range users {
		T.qfolders.home_ids[u.HomeID] = struct{}{}
	}

	if T.report {
		return T.RunReport(users)
	}

	target_profile, err := T.KW.Admin().FindProfile(T.target_profile_name)
	if err != nil {
		return err
	}
	if target_profile.Features.FolderCreate == 0 {
		return fmt.Errorf("Destination profile does not have permission to create folders")
	}
	T.target_profile_id = target_profile.ID
	T.UserCount = T.Report.Tally("Synced Users")
	T.FailedUsers = T.Report.Tally("Failed Users")
	T.FolderCount = T.Report.Tally("Synced Folders")
	T.FileCount = T.Report.Tally("Synced Files")
	T.FileTransferred = T.Report.Tally("Files Transferred")
	T.Transferred = T.Report.Tally("Data Transferred", HumanSize)
	wg := NewLimitGroup(25)

	T.users_created = make(map[string]any)

	message := func() string {
		return fmt.Sprintf("Working .. [ Folders: %d | Files: %d | Files Transferred: %d (%s) ]", T.FolderCount.Value(), T.FileCount.Value(), T.FileTransferred.Value(), HumanSize(T.Transferred.Value()))
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	Log("Starting Quatrix Migration...")
	Log("- Found %d Quatrix users.", len(users))
	Log("\n=== Creating/Verifying Quatrix users on Kiteworks. ===\n\n")
	for _, u := range users {
		wg.Add(1)
		go func(user Userdata) {
			defer wg.Done()
			username := strings.ToLower(u.Email)
			kw_user, err := T.KW.Admin().FindUser(username, true)
			if err != nil && err != ERR_NO_USER_FOUND {
				Err("Error finding user %s: %v", username, err)
				T.ignoreUser(username)
				return
			}
			if kw_user == nil {
				Log("[%s]: Creating user on Kiteworks..", username)
				if kw_user, err = T.KW.Admin().NewUser(username, T.target_profile_id, true, false); err != nil {
					if !IsAPIError(err, "ERR_ENTITY_EXISTS") {
						Err("[%s]: Failed to create user: %v (skipping)", username, err)
						T.ignoreUser(username)
						return
					}
				}
			} else {
				Log("[%s]: User already exists within Kiteworks.", username)
			}
			if kw_user != nil && (kw_user.Suspended || !kw_user.Verified) {
				if kw_user.Suspended {
					Log("[%s]: User suspended on Kiteworks, re-enabling.", username)
				}
				if !kw_user.Verified {
					Log("[%s]: User unverified on Kiteworks, verifying.", username)
				}
				if err := T.KW.Admin().UpdateUser(kw_user.ID, SetParams(PostJSON{"suspended": false, "verified": true})); err != nil {
					Err("Error updating user %s: %v (skipping)", username, err)
					T.ignoreUser(username)
					return
				}
				if err := T.KW.Admin().UpdateUserProfile(T.target_profile_id, []string{kw_user.ID}); err != nil {
					Err("Error updating user profile %s: %v (skipping)", username, err)
					T.ignoreUser(username)
					return
				}
			}
			if auxConfig.setCustom != nil {
				auxConfig.setCustom(RawString(user.UniqueLogin), kw_user)
			}
		}(u)
	}
	wg.Wait()
	Log("\n=== Users created/verified. Starting folder sync. ===\n\n")
	for _, u := range users {
		if T.ignoreUser(strings.ToLower(u.Email)) {
			T.FailedUsers.Add(1)
			continue
		}
		wg.Add(1)
		go func(user Userdata) {
			defer wg.Done()
			if cloneErr := T.CloneUser(&user); cloneErr != nil {
				if IsAPIError(cloneErr, "QUATRIX_CODE_20") {
					Err("[%s]: Unable to access user folders... %v", user.Email, cloneErr)
				} else {
					Err("[%s]: %v", user.Email, cloneErr)
				}
			}
		}(u)
	}
	wg.Wait()
	Log("\n=== Migration Complete ===")
	return nil
}
