package box

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	. "github.com/cmcoffee/kitebroker/core"
)

func init() { RegisterMigrationTask(new(BoxMigrationTask)) }

// BoxMigrationTask represents a task for migrating data from Box.com to Kiteworks.
type BoxMigrationTask struct {
	KiteBrokerTask
	box_db          Database
	box_config      Table
	box_json_config []byte
	bapi            *BoxAPI
	bsess           *BoxSession
	task_config     struct {
		current     string
		overdue     string
		completed   string
		no_due_date string
		future_days int
	}
	target_profile_name string
	target_profile_id   int
	failed_users        map[string]any
	failed_lock         sync.RWMutex
	user_emails         []string
	report              bool
	report_data         struct {
		users map[string]*userReport
		lock  sync.Mutex
	}
	UserCount       Tally
	FolderCount     Tally
	FileCount       Tally
	FileTransferred Tally
	Transferred     Tally
	CommentCount    Tally
	TaskCount       Tally
	FailedUsers     Tally
}

func (T *BoxMigrationTask) ignoreUser(username string) bool {
	T.failed_lock.RLock()
	defer T.failed_lock.RUnlock()
	return T.failed_users[username] != nil
}

func (T *BoxMigrationTask) setIgnoreUser(username string) {
	T.failed_lock.Lock()
	T.failed_users[username] = nil
	T.failed_lock.Unlock()
}

// boxUserCloner is a per-user wrapper around BoxMigrationTask that holds
// a user-scoped folder_map. This avoids cross-user cache collisions when
// two users have identically-named folder paths, and allows the map to be
// garbage-collected once the user migration is complete.
type boxUserCloner struct {
	*BoxMigrationTask
	folder_map map[string]*KiteObject
	lock       sync.RWMutex
	username   string
}

func filterInvalidChars(input string) string {
	var output []rune
	for _, v := range input {
		switch v {
		case '\\', '*', '?', '"', '<', '>', '|':
			output = append(output, []rune(url.QueryEscape(string(v)))...)
		default:
			output = append(output, v)
		}
	}
	return string(output)
}

// Name returns the name of this task.
func (T *BoxMigrationTask) Name() string {
	return "box"
}

// Desc returns a description of this task.
func (T *BoxMigrationTask) Desc() string {
	return "Migrate users, folders, files, permissions, comments, and tasks from Box.com to Kiteworks."
}

// Init initializes the Box migration task.
func (T *BoxMigrationTask) Init() (err error) {
	T.box_db = T.DB.Sub("box")
	T.box_config = T.box_db.Table("box_config")

	// Load stored Box JSON config.
	T.box_config.Get("box_json_config", &T.box_json_config)

	// Load task disposition settings.
	T.box_config.Get("task_current", &T.task_config.current)
	T.box_config.Get("task_overdue", &T.task_config.overdue)
	T.box_config.Get("task_completed", &T.task_config.completed)
	T.box_config.Get("task_no_due_date", &T.task_config.no_due_date)
	T.box_config.Get("task_future_days", &T.task_config.future_days)

	if T.task_config.current == NONE {
		T.task_config.current = "import"
	}
	if T.task_config.overdue == NONE {
		T.task_config.overdue = "import"
	}
	if T.task_config.completed == NONE {
		T.task_config.completed = "comment"
	}
	if T.task_config.no_due_date == NONE {
		T.task_config.no_due_date = "import"
	}
	if T.task_config.future_days == 0 {
		T.task_config.future_days = 7
	}

	if err := T.configureBox(); err != nil {
		return err
	}

	return nil
}

// configureBox handles Box configuration via flags or interactive prompt.
func (T *BoxMigrationTask) configureBox() error {
	setup := T.Flags.Bool("setup", "Configure Box.com Connection")
	T.Flags.StringVar(&T.target_profile_name, "profile", "Standard", "Destination profile for migrated users. (Needs permission to create folders)")
	T.Flags.MultiVar(&T.user_emails, "users", "<user@domain.com>", "User(s) to migrate.")
	migrate := T.Flags.Bool("migrate", "Perform the actual migration.")
	T.Flags.BoolVar(&T.report, "report", "Generate a report of Box.com users, folders and files.")
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

	if *setup || len(T.box_json_config) == 0 {
		var box_json_str string
		if len(T.box_json_config) > 0 {
			box_json_str = string(T.box_json_config)
		}

		box_auth := NewOptions("--- Box.com API Configuration ---", "(selection or 'q' to save & exit)", 'q')
		box_auth.TextAreaVar(&box_json_str, "Box JWT Config", "Paste Box.com App JSON Config Here...")
		box_auth.StringSelectVar(&T.task_config.current, "Current Tasks Disposition", T.task_config.current, "import", "comment", "drop")
		box_auth.StringSelectVar(&T.task_config.overdue, "Overdue Tasks Disposition", T.task_config.overdue, "import", "comment", "drop")
		box_auth.StringSelectVar(&T.task_config.completed, "Completed Tasks Disposition", T.task_config.completed, "import", "comment", "drop")
		box_auth.StringSelectVar(&T.task_config.no_due_date, "No Due Date Tasks Disposition", T.task_config.no_due_date, "import, comment, or drop.", "import", "comment", "drop")
		//"How to handle completed tasks: import, comment, or drop."
		if box_auth.Select(false) {
			if box_json_str != NONE {
				T.box_json_config = []byte(box_json_str)
				T.box_config.CryptSet("box_json_config", T.box_json_config)
			}
			T.box_config.Set("task_current", T.task_config.current)
			T.box_config.Set("task_overdue", T.task_config.overdue)
			T.box_config.Set("task_completed", T.task_config.completed)
			T.box_config.Set("task_no_due_date", T.task_config.no_due_date)
			T.box_config.Set("task_future_days", T.task_config.future_days)
		}
		Exit(0)
	}
	return nil
}

// configure_api creates and configures the Box API client.
func (T *BoxMigrationTask) configure_api() error {
	box_api := new(APIClient)
	box_api.Server = "api.box.com"
	box_api.VerifySSL = true
	box_api.ProxyURI = T.KW.ProxyURI
	T.box_db.Drop("tokens")

	box_api.NewToken = boxNewToken(T.box_json_config)
	box_api.ErrorScanner = boxError
	box_api.TokenErrorCodes = []string{"BOX_UNAUTHORIZED"}
	box_api.RetryErrorCodes = []string{"BOX_INTERNAL_SERVER_ERROR", "BOX_SERVICE_UNAVAILABLE", "BOX_RATE_LIMIT_EXCEEDED"}
	box_api.ReaquireToken = true
	box_api.Retries = 3

	box_api.SetDatabase(T.box_db)
	box_api.MaxChunkSize = T.KW.MaxChunkSize
	box_api.SetLimiter(T.KW.GetLimit())
	box_api.SetTransferLimiter(T.KW.GetTransferLimit())
	box_api.RequestTimeout = T.KW.RequestTimeout
	box_api.ConnectTimeout = T.KW.ConnectTimeout

	T.bapi = &BoxAPI{box_api}
	T.bsess = T.bapi.Session("")
	return nil
}

// ResolvePath resolves a Kiteworks folder path, creating folders as needed.
func (U *boxUserCloner) ResolvePath(path string) (*KiteObject, error) {
	path = filterInvalidChars(path)

	U.lock.RLock()
	if folder, found := U.folder_map[path]; found {
		U.lock.RUnlock()
		return folder, nil
	}
	U.lock.RUnlock()

	folder, err := U.KW.Session(U.username).Folder("0").ResolvePath(path)
	if err != nil {
		return nil, fmt.Errorf("Error resolving path %s for user '%s': %v", path, U.username, err)
	}

	U.lock.Lock()
	U.folder_map[path] = &folder
	U.lock.Unlock()
	return &folder, nil
}

// Main is the entry point for the Box migration task.
func (T *BoxMigrationTask) Main() (err error) {
	if err := T.configure_api(); err != nil {
		return err
	}

	all_users, err := T.bsess.Users()
	if err != nil {
		return err
	}

	var users []BoxUserRecord
	if len(T.user_emails) > 0 {
		filter := make(map[string]struct{})
		for _, e := range T.user_emails {
			filter[strings.ToLower(e)] = struct{}{}
		}
		for _, u := range all_users {
			if _, ok := filter[strings.ToLower(u.Login)]; ok {
				users = append(users, u)
			}
		}
	} else {
		users = all_users
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
	T.CommentCount = T.Report.Tally("Synced Comments")
	T.TaskCount = T.Report.Tally("Synced Tasks")

	wg := NewLimitGroup(25)

	T.failed_users = make(map[string]any)

	message := func() string {
		return fmt.Sprintf("Working .. [ Folders: %d | Files: %d | Files Transferred: %d (%s) ]", T.FolderCount.Value(), T.FileCount.Value(), T.FileTransferred.Value(), HumanSize(T.Transferred.Value()))
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	Log("Starting Box.com Migration...")
	Log("- Found %d Box.com users.", len(users))
	Log("\n=== Creating/Verifying Box.com users on Kiteworks. ===\n\n")
	for _, u := range users {
		wg.Add(1)
		go func(user BoxUserRecord) {
			defer wg.Done()
			username := strings.ToLower(user.Login)
			if username == NONE || username == "unknown_box_user@box.com" {
				Err("[%s]: Skipping user with no login.", user.ID)
				T.setIgnoreUser(username)
				return
			}
			kw_user, err := T.KW.Admin().FindUser(username, true)
			if err != nil && err != ERR_NO_USER_FOUND {
				Err("Error finding user %s: %v", username, err)
				T.setIgnoreUser(username)
				return
			}
			if kw_user == nil {
				Log("[%s]: Creating user on Kiteworks..", username)
				if kw_user, err = T.KW.Admin().NewUser(username, T.target_profile_id, true, false); err != nil {
					if !IsAPIError(err, "ERR_ENTITY_EXISTS") {
						Err("[%s]: Failed to create user: %v (skipping)", username, err)
						T.setIgnoreUser(username)
						return
					}
				}
			} else {
				Log("[%s]: User already exists on Kiteworks.", username)
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
					T.setIgnoreUser(username)
					return
				}
				if err := T.KW.Admin().UpdateUserProfile(T.target_profile_id, []string{kw_user.ID}); err != nil {
					Err("Error updating user profile %s: %v (skipping)", username, err)
					T.setIgnoreUser(username)
					return
				}
			}
		}(u)
	}
	wg.Wait()
	Log("\n=== Users created/verified. Starting folder sync. ===\n\n")
	for _, u := range users {
		username := strings.ToLower(u.Login)
		if T.ignoreUser(username) {
			T.FailedUsers.Add(1)
			continue
		}
		wg.Add(1)
		go func(user BoxUserRecord) {
			defer wg.Done()
			if cloneErr := T.CloneUser(&user); cloneErr != nil {
				Err("[%s]: %v", strings.ToLower(user.Login), cloneErr)
			}
		}(u)
	}
	wg.Wait()
	Log("\n=== Migration Complete ===")
	return nil
}

// CloneUser migrates a single Box user's folders to Kiteworks.
func (T *BoxMigrationTask) CloneUser(user *BoxUserRecord) error {
	T.UserCount.Add(1)
	username := strings.ToLower(user.Login)

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
	cloner := &boxUserCloner{
		BoxMigrationTask: T,
		folder_map:       make(map[string]*KiteObject),
		username:         username,
	}

	sess := T.bapi.Session(user.ID)
	baseFolder, err := sess.Folder("0")
	if err != nil {
		return fmt.Errorf("[%s]: Error retrieving Box root folder: %v", username, err)
	}

	// Collect owned top-level folders for the crawler.
	var ownedFolders []*BoxFolder
	for _, f := range baseFolder.Items {
		if f.Type != "folder" {
			continue
		}
		folder, err := sess.Folder(f.ID)
		if err != nil {
			Err("[%s]: Error reading folder %s: %v", username, f.Name, err)
			continue
		}
		if folder.Owner != username {
			continue
		}
		ownedFolders = append(ownedFolders, folder)
	}

	// Define the processor callback for the FolderCrawler.
	processor := func(bsess *BoxSession, folder *BoxFolder, item *BoxFolderItem) error {
		if item == nil {
			return cloner.processFolder(folder)
		}
		return cloner.processFile(bsess, folder, item)
	}

	if len(ownedFolders) > 0 {
		sess.FolderCrawler(processor, ownedFolders...)
	}

	// Handle root-level files (files directly in Box folder "0").
	for _, f := range baseFolder.Items {
		if f.Type != "file" {
			continue
		}
		if err := cloner.processFile(sess, baseFolder, &f); err != nil {
			Err("[%s]: Error processing root file %s: %v", username, f.Name, err)
		}
	}

	return nil
}

// processFolder handles a folder encountered during crawling: resolves path and syncs permissions.
func (U *boxUserCloner) processFolder(folder *BoxFolder) error {
	U.FolderCount.Add(1)

	kwFolder, err := U.ResolvePath(folder.FullPath)
	if err != nil {
		return err
	}

	Log("[%s]: Box.com: %s -> Kiteworks: /%s", U.username, folder.FullPath, kwFolder.Path)

	// Sync permissions.
	if len(folder.Permissions) > 0 {
		perm_map := make(map[int][]string)
		var counter int
		for _, p := range folder.Permissions {
			if p.User == U.username {
				continue
			}
			perm_map[p.Role] = append(perm_map[p.Role], p.User)
			counter++
		}
		existingPerms, _ := U.KW.Session(U.username).Folder(kwFolder.ID).Members()
		counter -= SkipExistingPerms(perm_map, existingPerms)
		if counter > 0 {
			Log("[%s]: %s - Adding %d permissions to folder.", U.username, kwFolder.Path, counter)
		}
		for k, v := range perm_map {
			err = U.KW.Session(U.username).Folder(kwFolder.ID).AddUsersToFolder(v, k, false, true)
			if err != nil && !IsAPIError(err, "ERR_ENTITY_ROLE_IS_ASSIGNED") {
				Err("[%s]: Error adding users to folder %s: %v", U.username, kwFolder.Path, err)
			}
		}
	}

	return nil
}

// processFile handles a file encountered during crawling: downloads versions, uploads to KW, syncs comments/tasks.
func (U *boxUserCloner) processFile(sess *BoxSession, folder *BoxFolder, item *BoxFolderItem) error {
	kwFolder, err := U.ResolvePath(folder.FullPath)
	if err != nil {
		return err
	}

	versions, err := sess.FileVersions(item.ID)
	if err != nil {
		return fmt.Errorf("Error getting versions for %s: %v", item.Name, err)
	}

	U.FileCount.Add(1)

	var kwFileID string

	for _, ver := range versions {
		dl, err := sess.Download(item.ID)
		if err != nil {
			return fmt.Errorf("Error downloading %s v%d: %v", ver.Name, ver.Ver, err)
		}

		x := TransferCounter(dl, U.Transferred.Add)
		file, err := U.KW.Session(U.username).Upload(filterInvalidChars(ver.Name), ver.Size, ver.Modified, false, true, true, *kwFolder, x)
		dl.Close()
		if err != nil {
			if !IsAPIError(err, "ERR_ENTITY_EXISTS") {
				Err("[%s]: Error uploading %s v%d: %v", U.username, ver.Name, ver.Ver, err)
			}
			continue
		}
		if file != nil {
			kwFileID = file.ID
			U.FileTransferred.Add(1)
		}
	}

	if kwFileID == NONE {
		return nil
	}

	// Sync comments.
	comments, err := sess.FileComments(item.ID)
	if err != nil {
		Err("[%s]: Error getting comments for %s: %v", U.username, item.Name, err)
	} else {
		U.SyncFileComments(U.username, kwFileID, comments)
	}

	// Sync tasks.
	tasks, err := sess.FileTasks(item.ID)
	if err != nil {
		Err("[%s]: Error getting tasks for %s: %v", U.username, item.Name, err)
	} else {
		U.SyncFileTasks(U.username, kwFileID, kwFolder.ID, tasks)
	}

	return nil
}

// SyncFileComments posts Box comments as comments on the Kiteworks file.
func (T *BoxMigrationTask) SyncFileComments(kwUser, kwFileID string, comments []BoxComment) {
	for _, c := range comments {
		err := T.KW.Session(kwUser).File(kwFileID).AddComment(c.Message)
		if err != nil {
			Err("[%s]: Error posting comment: %v", kwUser, err)
			continue
		}
		T.CommentCount.Add(1)
	}
}

// SyncFileTasks handles Box tasks according to configured disposition (import/comment/drop).
func (T *BoxMigrationTask) SyncFileTasks(kwUser, kwFileID, kwFolderID string, tasks []BoxTask) {
	for i := range tasks {
		task := &tasks[i]

		var ttype string
		switch {
		case task.Completed:
			ttype = "completed"
		case task.Due.IsZero():
			ttype = "no_due_date"
		default:
			if task.Due.Unix() > time.Now().Unix() {
				ttype = "current"
			} else {
				ttype = "overdue"
			}
		}

		var disposition string
		switch ttype {
		case "current":
			disposition = T.task_config.current
		case "overdue":
			disposition = T.task_config.overdue
		case "completed":
			disposition = T.task_config.completed
		case "no_due_date":
			disposition = T.task_config.no_due_date
		}

		switch strings.ToLower(disposition) {
		case "drop":
			Log("[%s]: Drop Task: %s", kwUser, TaskString(task, false))
		case "comment":
			message := TaskString(task, false)
			err := T.KW.Session(kwUser).File(kwFileID).AddComment(message)
			if err != nil {
				Err("[%s]: Error posting task as comment: %v", kwUser, err)
				continue
			}
			T.TaskCount.Add(1)
		case "import":
			T.ImportFileTask(kwUser, kwFileID, kwFolderID, task)
		}
	}
}

// ImportFileTask posts a Box task as a Kiteworks task on the file.
func (T *BoxMigrationTask) ImportFileTask(kwUser, kwFileID, kwFolderID string, task *BoxTask) {
	nowDate := time.Now().UTC()

	for _, assignee := range task.AssignedTo {
		// Ensure the assignee exists on Kiteworks.
		if _, newUserErr := T.KW.Admin().NewUser(assignee, T.target_profile_id, true, false); newUserErr != nil {
			if !IsAPIError(newUserErr, "ERR_ENTITY_EXISTS") {
				Err("[%s]: Error creating task assignee %s: %v", kwUser, assignee, newUserErr)
				continue
			}
		}

		assigneeUser, err := T.KW.Admin().FindUser(assignee)
		if err != nil {
			Err("[%s]: Error finding task assignee %s: %v", kwUser, assignee, err)
			continue
		}

		message := TaskString(task, true)
		dueDate := task.Due.UTC()

		if dueDate.IsZero() || dueDate.Unix() < nowDate.Unix() || dateString(dueDate) == dateString(nowDate) {
			dueDate = nowDate.Add(time.Hour * 24 * time.Duration(T.task_config.future_days))
		}

		err = T.KW.Session(kwUser).File(kwFileID).AddTask(assigneeUser.ID, dateString(dueDate), message)
		if err != nil {
			Err("[%s]: Error importing task: %v", kwUser, err)
			continue
		}
		T.TaskCount.Add(1)
	}
}
