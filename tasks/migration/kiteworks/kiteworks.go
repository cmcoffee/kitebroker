package kiteworks

import (
	"fmt"
	"strings"
	"sync"

	. "github.com/cmcoffee/kitebroker/core"
)

func init() { RegisterMigrationTask(new(KW_TO_KWTask)) }

type KW_TO_KWTask struct {
	// input variables
	input struct {
		dst_profile_name    string
		src_profile_name    string
		user_emails         []string
		delete_user_first   bool
		cleanup             bool
		deactivate_src_user bool
		no_files            bool
		no_mail             bool
		setup               bool
		src_domain          string
		new_domain          string
	}
	dst_profile_id      int
	src_admin           string
	src_kw_db           Database
	src_kw_config       Table
	SRC                 KWAPI
	users               map[string]struct{}
	failed_users        map[string]any
	failed_lock         sync.RWMutex
	limiter             LimitGroup
	users_count         Tally
	folders_count       Tally
	files_count         Tally
	files_copied        Tally
	mail_count          Tally
	transfer_counter    Tally
	FailedUsers         Tally
	src_dst_profile_map map[int]int
	report              bool
	report_data         struct {
		users map[string]*userReport
		lock  sync.Mutex
	}
	// Required for all tasks
	KiteBrokerTask
}

func (T *KW_TO_KWTask) ignoreUser(email string) bool {
	T.failed_lock.RLock()
	defer T.failed_lock.RUnlock()
	return T.failed_users[email] != nil
}

func (T *KW_TO_KWTask) setIgnoreUser(email string) {
	T.failed_lock.Lock()
	T.failed_users[email] = nil
	T.failed_lock.Unlock()
	T.FailedUsers.Add(1)
}

// Name returns the name of the task.
func (T KW_TO_KWTask) Name() string {
	return "kiteworks"
}

// Desc returns the description of the task.
func (T KW_TO_KWTask) Desc() string {
	return "Migrate users, folders, files, permissions from a remote Kiteworks server."
}

func (T KW_TO_KWTask) testAPI() bool {

	if !T.testConfig() {
		Err("API is missing some required configuration, please revisit '*** UNCONFIGURED ***' settings.")
		NeedInteract()
		return false
	}

	err := T.configAPI()
	if err != nil {
		Err(err)
		return false
	}

	retry_count := T.SRC.Retries
	defer func() {
		T.SRC.Retries = retry_count
	}()
	T.SRC.Retries = 0

	T.SRC.TokenStore.Delete(T.src_admin)

	Flash("[%s]: Authenticating, please wait...", T.SRC.Server)

	_, err = T.SRC.Session(T.src_admin).MyUser()

	if err != nil {
		Stdout("[ERROR] %s", err.Error())
		NeedInteract()
		return false
	}

	Log("[SUCCESS]: %s reports successful API communications!", T.SRC.Server)
	NeedInteract()
	return true
}

// Init initializes the task.
func (T *KW_TO_KWTask) Init() (err error) {

	T.src_kw_db = T.DB.Sub("kiteworks_migration")
	T.src_kw_config = T.src_kw_db.Table("src_kw_config")
	T.src_admin = T.src_kw_config.GetString("src_admin")

	T.Flags.StringVar(&T.input.dst_profile_name, "dest_profile", "Auto", "Destination KW Profile Name (Auto = map by name)")
	T.Flags.StringVar(&T.input.src_profile_name, "src_profile", "<Source Profile>", "Migrating only users from profile.")
	T.Flags.StringVar(&T.input.src_domain, "src_domain", "<old_domain.com>", "Source users domain.")
	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "User(s) to copy.")
	T.Flags.BoolVar(&T.input.delete_user_first, "delete_user_first", "Delete destination user prior to migration.")
	T.Flags.StringVar(&T.input.new_domain, "new_domain", "<new_domain.com>", "Replace users domain with domain specified.")
	T.Flags.BoolVar(&T.input.cleanup, "cleanup", "Remove source files if they exist on destination already.")
	T.Flags.BoolVar(&T.input.deactivate_src_user, "deactivate", "Deactivate Users after copy command")
	T.Flags.BoolVar(&T.input.no_files, "no_files", "Do not copy files.")
	T.Flags.BoolVar(&T.input.no_mail, "no_mail", "Do not archive mail.")
	migrate := T.Flags.Bool("migrate", "Perform the actual migration.")
	T.Flags.BoolVar(&T.report, "report", "Generate a report of source Kiteworks users, folders and files.")
	T.Flags.BoolVar(&T.input.setup, "setup", "Configuration Remote Source Kiteworks Connection.")
	T.Flags.Order("migrate", "report")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	if *migrate && T.report {
		return fmt.Errorf("--migrate and --report are mutually exclusive, please specify only one")
	}

	if T.input.setup {
		err = T.apiSetup()
		return err
	}

	if !T.testConfig() {
		fmt.Printf("Incomplete configuration found for source Kiteworks system...\n\n")
		T.apiSetup()
	}

	if !*migrate && !T.report {
		return fmt.Errorf("must specify either --migrate or --report")
	}

	/*if len(T.input.user_emails) == 0 && IsBlank(T.input.src_profile_name) {
		err = fmt.Errorf("Must provide --users or --src_profile to copy.")
		return
	}*/

	return
}

// testConfig verifies required configuration values are set.
// It returns true if all values are present, false otherwise.
func (T *KW_TO_KWTask) testConfig() bool {
	for _, v := range []string{"jwt_key", "jwt_uid", "jwt_iss", "app_id", "client_secret", "redirect_uri", "server", "admin"} {
		if x := T.src_kw_config.GetString(v); x == NONE {
			return false
		}
	}
	return true
}

// configAPI configures the API client with settings from the config.
// It sets the server address, JWT settings, client credentials, and
// other parameters for the API client used for communication with
// the source Kiteworks instance.
func (T *KW_TO_KWTask) configAPI() (err error) {
	T.SRC.APIClient = new(APIClient)
	T.SRC.APIClient.Server = T.src_kw_config.GetString("server")
	jwt := T.SRC.JWT()
	jwt.Issuer(T.src_kw_config.GetString("jwt_iss"))
	jwt.Key(T.src_kw_config.GetString("jwt_key"))
	jwt.UIDAttribute(T.src_kw_config.GetString("jwt_uid"))
	T.SRC.APIClient.ClientSecret(T.src_kw_config.GetString("client_secret"))
	T.SRC.APIClient.ApplicationID = T.src_kw_config.GetString("app_id")
	T.SRC.APIClient.RedirectURI = T.src_kw_config.GetString("redirect_uri")
	T.SRC.APIClient.ErrorScanner = T.KW.ErrorScanner
	T.SRC.Flags.Set(JWT_AUTH)
	T.SRC.APIClient.NewToken = T.KW.KWNewToken
	T.SRC.ReaquireToken = true
	T.SRC.SetDatabase(T.src_kw_db)
	T.SRC.MaxChunkSize = T.KW.MaxChunkSize
	T.SRC.SetLimiter(T.KW.GetLimit())
	T.SRC.SetTransferLimiter(T.KW.GetTransferLimit())
	T.SRC.RequestTimeout = T.KW.RequestTimeout
	T.SRC.ConnectTimeout = T.KW.ConnectTimeout
	T.src_admin = T.src_kw_config.GetString("src_admin")
	return nil
}

// apiSetup configures the Kiteworks API settings via user input.
// It gathers necessary credentials and stores them securely.
func (T *KW_TO_KWTask) apiSetup() (err error) {
	PleaseWait.Hide()
	defer PleaseWait.Show()
	var src_server, src_app_id, src_secret, src_redirect, jwt_key, jwt_uid, jwt_iss string
	T.src_kw_config.Get("jwt_key", &jwt_key)
	T.src_kw_config.Get("jwt_uid", &jwt_uid)
	T.src_kw_config.Get("jwt_iss", &jwt_iss)
	T.src_kw_config.Get("app_id", &src_app_id)
	T.src_kw_config.Get("client_secret", &src_secret)
	T.src_kw_config.Get("redirect_uri", &src_redirect)
	T.src_kw_config.Get("server", &src_server)
	T.src_kw_config.Get("src_admin", &T.src_admin)

	auth := NewOptions(" [Kiteworks JWT Configuration] ", "(selection or 'q' to return to previous)", 'q')
	auth.TextAreaVar(&jwt_key, "JWT RSA Private Key", "Paste JWT RSA Private Key Here...")
	auth.StringVar(&jwt_uid, "JWT UID Attribute", jwt_uid, "Please provide UID Attribute for JWT Claims. (should match Kiteworks UID Attribute within the API Configuration.)")
	auth.StringVar(&jwt_iss, "JWT Issuer", jwt_iss, "Please provide the Issuer (ISS) for JWT claims. (should match Kiteworks Issuer within the API Configuration.)")

	src_kw_auth := NewOptions("--- Source Kiteworks API Configuration ---", "(selection or 'q' to save & exit)", 'q')
	src_kw_auth.StringVar(&T.src_admin, "Source Kiteworks Admin Account", T.src_admin, "Please input the Kiteworks source's admin email")
	src_kw_auth.Options("Source Kiteworks JWT Settings", auth, false)
	src_kw_auth.StringVar(&src_server, "Source Kiteworks Server", src_server, "Please input the Kiteworks source host.")
	src_kw_auth.StringVar(&src_app_id, "Source Application ID", src_app_id, "Application ID for Kiteworks source.")
	src_kw_auth.SecretVar(&src_secret, "Source Client Secret", src_secret, "Client secure for Kiteworks source.")
	src_kw_auth.StringVar(&src_redirect, "Source Kiteworks Redirect URI", src_redirect, "Rediret URI for Kiteworks source.")
	if src_kw_auth.Select(false) {
		T.src_kw_config.Set("server", &src_server)
		T.src_kw_config.Set("src_admin", &T.src_admin)
		T.src_kw_config.Set("client_id", &src_app_id)
		T.src_kw_config.CryptSet("client_secret", &src_secret)
		T.src_kw_config.Set("app_id", &src_app_id)
		T.src_kw_config.Set("redirect_uri", &src_redirect)
		T.src_kw_config.CryptSet("jwt_key", &jwt_key)
		T.src_kw_config.CryptSet("jwt_uid", &jwt_uid)
		T.src_kw_config.CryptSet("jwt_iss", &jwt_iss)
	}
	Exit(0)
	return nil
}

// MapProfiles maps source profiles to destination profiles.
func (T *KW_TO_KWTask) MapProfiles() (err error) {
	Log("\n=== Auto-Mapping Destination Profiles. ===\n\n")
	T.src_dst_profile_map = make(map[int]int)
	src_profiles, err := T.SRC.Session(T.src_admin).Admin().Profiles()
	if err != nil {
		Fatal("Error talking to source server: %s", err.Error())
	}
	dst_profiles, err := T.KW.Admin().Profiles()
	if err != nil {
		Fatal("Error talking to destination server: %s", err.Error())
	}
	dst_profile_map := make(map[string]int)
	for _, v := range dst_profiles {
		if v.Features.FolderCreate == 0 {
			Log("Destination profile '%s' does not have permission to create folders, skipping.", v.Name)
			continue
		}
		dst_profile_map[strings.ToLower(v.Name)] = v.ID
	}
	Log("")
	if !T.input.cleanup {
		for _, v := range src_profiles {
			if x, ok := dst_profile_map[strings.ToLower(v.Name)]; ok {
				Log("Mapping profile: %s, Destination Profile ID: %d", v.Name, x)
				T.src_dst_profile_map[v.ID] = x
			}
		}
	}
	return nil
}

// FindDestProfileID returns the destination profile ID based on the source ID.
// It first checks if a global destination profile ID is set. If not, it looks up the
// destination profile ID in a map that maps source profile IDs to destination profile IDs.
// If no mapping is found, it returns 0.
func (T *KW_TO_KWTask) FindDestProfileID(id int) int {
	if T.dst_profile_id != 0 {
		return T.dst_profile_id
	}
	if v, ok := T.src_dst_profile_map[id]; ok {
		return v
	}
	return 0
}

// Main is the main function of the task.
func (T *KW_TO_KWTask) Main() (err error) {
	if err = T.configAPI(); err != nil {
		return err
	}

	if IsBlank(T.input.src_domain) && len(T.input.user_emails) > 0 {
		split := strings.Split(T.input.user_emails[0], "@")
		if len(split) < 2 {
			return fmt.Errorf("Unable to obtain email domain from %s.", T.input.user_emails[0])
		}
		T.input.src_domain = split[1]
	}

	// Resolve source profile name to ID.
	var src_profile_id int
	if !IsBlank(T.input.src_profile_name) {
		src_profile, err := T.SRC.Session(T.src_admin).Admin().FindProfile(T.input.src_profile_name)
		if err != nil {
			return fmt.Errorf("Source profile resolution failed: %v", err)
		}
		src_profile_id = src_profile.ID
	}

	params := SetParams(Query{"active": true, "deleted": false, "suspended": false, "verified": true})
	if !IsBlank(T.input.src_domain) {
		params = SetParams(params, Query{"email:contains": T.input.src_domain})
	}
	user_getter, err := T.SRC.Session(T.src_admin).Admin().Users(T.input.user_emails, src_profile_id, params)
	if err != nil {
		return err
	}

	// Collect all source users.
	var all_users []KiteUser
	for {
		users, err := user_getter.Next()
		if err != nil {
			return err
		}
		if len(users) == 0 {
			break
		}
		all_users = append(all_users, users...)
	}

	if T.report {
		return T.RunReport(all_users)
	}

	if !IsBlank(T.input.new_domain) && IsBlank(T.input.src_domain) {
		return fmt.Errorf("If you must specify an new_domain, you must specify a src_domain.")
	}

	Debug("Old Domain: %s, New Domain: %s", T.input.src_domain, T.input.new_domain)

	T.users_count = T.Report.Tally("Synced Users")
	T.FailedUsers = T.Report.Tally("Failed Users")
	T.folders_count = T.Report.Tally("Synced Folders")
	T.files_count = T.Report.Tally("Synced Files")
	T.files_copied = T.Report.Tally("Files Transferred")
	T.mail_count = T.Report.Tally("Mail Archived")
	T.transfer_counter = T.Report.Tally("Data Transferred", HumanSize)

	T.limiter = NewLimitGroup(50)

	message := func() string {
		return fmt.Sprintf("Working .. [ Folders: %d | Files: %d | Files Transferred: %d (%s) ]", T.folders_count.Value(), T.files_count.Value(), T.files_copied.Value(), HumanSize(T.transfer_counter.Value()))
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	Log("Starting Kiteworks Migration...")

	// Resolve destination profile.
	if strings.EqualFold(T.input.dst_profile_name, "auto") {
		if err := T.MapProfiles(); err != nil {
			return err
		}
	} else {
		dst_profile, err := T.KW.Admin().FindProfile(T.input.dst_profile_name)
		if err != nil {
			return fmt.Errorf("Destination profile resolution failed: %v", err)
		}
		if dst_profile.Features.FolderCreate == 0 {
			return fmt.Errorf("Destination profile does not have permission to create folders")
		}
		T.dst_profile_id = dst_profile.ID
	}

	T.users = make(map[string]struct{})
	for _, u := range all_users {
		T.users[u.Email] = struct{}{}
	}
	T.failed_users = make(map[string]any)

	wg := NewLimitGroup(25)

	// Phase 1: Create/verify all users on destination (skip in cleanup mode).
	if !T.input.cleanup {
		Log("\n=== Creating/Verifying users on Kiteworks. ===\n\n")
		Log("- Found %d valid source users.\n\n", len(all_users))
		for _, u := range all_users {
			wg.Add(1)
			go func(user KiteUser) {
				defer wg.Done()
				username := T.SwapEmails(user.Email)

				if T.input.delete_user_first {
					Debug("Deleting %s", username)
					dst_user, err := T.KW.Admin().FindUser(username)
					if err != nil {
						Err("[%s]: Error finding user for deletion: %v (skipping)", username, err)
						T.setIgnoreUser(user.Email)
						return
					}
					if username != dst_user.Email {
						T.setIgnoreUser(user.Email)
						return
					}
					params := SetParams(Query{"retainData": false, "deleteUnsharedData": true})
					if err := T.KW.Admin().DeleteUser(*dst_user, params); err != nil {
						Err("[%s]: Error deleting user: %v (skipping)", username, err)
						T.setIgnoreUser(user.Email)
						return
					}
				}

				dest_profile_id := T.FindDestProfileID(user.UserTypeID)
				if dest_profile_id == 0 {
					Err("Could not find profile mapping for %s on destination system, skipping user.", user.Email)
					T.setIgnoreUser(user.Email)
					return
				}

				kw_user, err := T.KW.Admin().FindUser(username, true)
				if err != nil && err != ERR_NO_USER_FOUND {
					Err("Error finding user %s: %v", username, err)
					T.setIgnoreUser(user.Email)
					return
				}
				if kw_user == nil {
					Log("[%s]: Creating user on Kiteworks..", username)
					if kw_user, err = T.KW.Admin().NewUser(username, dest_profile_id, true, false); err != nil {
						if !IsAPIError(err, "ERR_ENTITY_EXISTS") {
							Err("[%s]: Failed to create user: %v (skipping)", username, err)
							T.setIgnoreUser(user.Email)
							return
						}
					}
				} else {
					Log("[%s]: User already exists on Kiteworks.", username)
				}
				if kw_user != nil && (kw_user.Suspended || !kw_user.Verified) {
					if kw_user.Suspended {
						Log("[%s]: User suspended on Kiteworks, skipping..", username)
					}
					if !kw_user.Verified {
						Log("[%s]: User unverified on Kiteworks source, skipping..", username)
					}
					if err := T.KW.Admin().UpdateUser(kw_user.ID, SetParams(PostJSON{"suspended": false, "verified": true})); err != nil {
						Err("Error updating user %s: %v (skipping)", username, err)
						T.setIgnoreUser(user.Email)
						return
					}
					if err := T.KW.Admin().UpdateUserProfile(dest_profile_id, []string{kw_user.ID}); err != nil {
						Err("Error updating user profile %s: %v (skipping)", username, err)
						T.setIgnoreUser(user.Email)
						return
					}
				}
			}(u)
		}
		wg.Wait()
	}

	// Phase 2: Archive user mail.
	if !T.input.no_mail && !T.input.cleanup {
		Log("\n=== Archiving user mail. ===\n\n")
		for _, u := range all_users {
			if T.ignoreUser(u.Email) {
				continue
			}
			wg.Add(1)
			go func(user KiteUser) {
				defer wg.Done()
				if err := T.ArchiveUserMail(user); err != nil {
					Err("[%s]: Mail archive error: %v", user.Email, err)
				}
			}(u)
		}
		wg.Wait()
	}

	// Phase 3: Copy all users.
	Log("\n=== Users created/verified. Starting folder sync. ===\n\n")
	for _, u := range all_users {
		if T.ignoreUser(u.Email) {
			continue
		}
		wg.Add(1)
		go func(user KiteUser) {
			defer wg.Done()
			if err := T.CopyUser(user); err != nil {
				Err("%s: %v", user.Email, err)
			}
		}(u)
	}
	wg.Wait()
	Log("\n=== Migration Complete ===")

	return
}

// MigrateUser represents a user to be migrated.
type MigrateUser struct {
	src      *KiteUser
	dst      *KiteUser
	src_sess KWSession
	dst_sess KWSession
}

// SwapEmails swaps the domain of an email address.
func (T *KW_TO_KWTask) SwapEmails(input string) string {
	if IsBlank(T.input.new_domain) {
		return input
	}
	return strings.Replace(input, T.input.src_domain, T.input.new_domain, -1)
}

// CopyUser copies a user from the source to the destination Kiteworks system.
func (T *KW_TO_KWTask) CopyUser(src_user KiteUser) (err error) {
	T.users_count.Add(1)
	err = T.SRC.Session(T.src_admin).Admin().ActivateUser(src_user.ID)
	if err != nil {
		return err
	}

	dst_user, err := T.KW.Admin().FindUser(T.SwapEmails(src_user.Email))
	if err != nil {
		return err
	}

	err = T.KW.Admin().ActivateUser(dst_user.ID)
	if err != nil {
		return err
	}

	src_folder, err := T.SRC.Session(src_user.Email).Folder(src_user.BaseDirID).Info()
	if err != nil {
		return err
	}

	if !IsBlank(T.input.src_domain) && (T.input.src_domain != T.input.new_domain) {
		Log("Source User: %s, Destination User: %s", src_user.Email, dst_user.Email)
	} else {
		Log("[%s]: %s -> %s", src_user.Email, T.SRC.Server, T.KW.Server)
	}

	migration_user := MigrateUser{
		&src_user,
		dst_user,
		T.SRC.Session(src_user.Email),
		T.KW.Session(dst_user.Email),
	}

	T.ProcessFolder(&migration_user, &src_folder)
	T.limiter.Wait()

	if T.input.deactivate_src_user {
		err = T.SRC.Session(T.src_admin).Admin().DeactivateUser(src_user.ID)
	}
	return

}

// SetPerms sets the permissions for a folder.
func (T *KW_TO_KWTask) SetPerms(migration_users *MigrateUser, folder *KiteObject, members []KiteMember) (err error) {
	perm_map := make(map[int][]string)
	var counter int
	for _, m := range members {
		if m.User.Email == migration_users.dst.Email {
			continue
		}
		perm_map[m.RoleID] = append(perm_map[m.RoleID], T.SwapEmails(m.User.Email))
		counter++
	}
	existingPerms, _ := migration_users.dst_sess.Folder(folder.ID).Members()
	counter -= SkipExistingPerms(perm_map, existingPerms)
	if counter > 0 {
		Log("[%s]: %s - Adding %d permissions to folder.", migration_users.dst.Email, folder.Path, counter)
	}
	for k, v := range perm_map {
		err = migration_users.dst_sess.Folder(folder.ID).AddUsersToFolder(v, k, false, true)
		if err != nil && !IsAPIError(err, "ERR_ENTITY_ROLE_IS_ASSIGNED") {
			Err(err)
		}
	}
	return
}

// CloneFolder clones a folder from the source to the destination.
func (T *KW_TO_KWTask) CloneFolder(migration_users *MigrateUser, folder *KiteObject) (err error) {
	if folder.CurrentUserRole.ID != 5 || folder.Path == "basedir" {
		return nil
	}
	T.folders_count.Add(1)

	var dest_folder KiteObject

	if !T.input.cleanup {
		dest_folder, err = migration_users.dst_sess.Folder(migration_users.dst.BaseDirID).ResolvePath(folder.Path)
		if err != nil {
			return err
		}
	} else {
		dest_folder, err = migration_users.dst_sess.Folder(migration_users.dst.BaseDirID).Find(folder.Path)
		if err != nil {
			return err
		}
	}

	Log("[%s]: Source: %s -> Destination: /%s", migration_users.dst.Email, folder.Path, dest_folder.Path)

	children, err := migration_users.src_sess.Folder(folder.ID).Files()
	if err != nil {
		return err
	}

	members, err := migration_users.src_sess.Folder(folder.ID).Members()
	if err != nil {
		return err
	}

	var new_member_list []KiteMember

	for _, v := range members {
		if !IsBlank(v.User.Email) {
			new_member_list = append(new_member_list, v)
		}
	}

	if !T.input.cleanup {
		err = T.SetPerms(migration_users, &dest_folder, new_member_list)
		if err != nil {
			return err
		}
	}

	dest_children, err := migration_users.dst_sess.Folder(dest_folder.ID).Files()
	if err != nil {
		return err
	}

	file_map := make(map[string]KiteObject)

	for _, v := range dest_children {
		if v.Type != "f" {
			continue
		}
		file_map[v.Name] = v
	}

	for _, f := range children {
		if f.Type != "f" {
			continue
		}
		T.files_count.Add(1)
		//if IsBlank(f.ClientModified) {
		f.ClientModified = f.Modified
		//}

		if v, ok := file_map[f.Name]; ok {
			Debug("%s->%s: [%d vs %d] [%v vs %v]", f.Name, v.Name, v.Size, f.Size, v.ClientModified, f.ClientModified)

			if f.ClientModified == v.ClientModified {
				if v.Size != f.Size {
					if T.input.cleanup {
						continue
					}
					Debug("%s: Deleting.", v.Name)
					if err := migration_users.dst_sess.File(v.ID).Delete(); err != nil {
						Err("%s: %v", v.Name, err)
					}
				} else {
					if T.input.cleanup {
						if v.ClientModified == f.ClientModified && v.Fingerprint == f.Fingerprint {
							Debug("%s/%s: Deleting source as file is same on server.", v.Path, v.Name)
							if err := migration_users.src_sess.File(f.ID).Delete(); err != nil {
								Err("%s: %v", f.Name, err)
							}
							continue
						}
					} else {
						Debug("%s: Skipping file, already uploaded.", f.Name)
					}
					continue
				}
			} else {
				if T.input.cleanup {
					Debug("%s: Deleting.", v.Name)
					if err := migration_users.dst_sess.File(v.ID).Delete(); err != nil {
						Err("%s: %v", v.Name, err)
					}
				}
			}
		}

		// We don't copy any files on a cleanup run.
		if T.input.cleanup || T.input.no_files {
			continue
		}

		// Main download/upload loop.
		source_info := fmt.Sprintf("%s - download - %s/%s", migration_users.src_sess.Username, dest_folder.Name, f.Name)
		dest_info := fmt.Sprintf("%s - upload - %s/%s", migration_users.dst_sess.Username, dest_folder.Name, f.Name)

		retry_download := T.KW.InitRetry(migration_users.src_sess.Username, source_info)
		retry_upload := T.KW.InitRetry(migration_users.dst_sess.Username, dest_info)
		for {
			// Initiate Download
			down, err := migration_users.src_sess.QDownload(&f)
			if err != nil {
				if retry_download.CheckForRetry(err) {
					continue
				} else {
					Err("%s: %s", source_info, err.Error())
					break
				}
			}

			modtime, err := ReadKWTime(f.ClientModified)
			if err != nil {
				Err("%s: %s", f.Name, err.Error())
			}

			// Initiate Upload
			f, err := migration_users.dst_sess.Upload(f.Name, f.Size, modtime, false, false, true, dest_folder, down)
			if err != nil {
				if retry_upload.CheckForRetry(err) {
					continue
				} else {
					Err("%s: %s", dest_info, err.Error())
					break
				}
			}
			if f != nil {
				T.files_copied.Add(1)
				T.transfer_counter.Add64(f.Size)
			}
			break
		}
	}

	return
}

// ArchiveUserMail archives all mail for a source user into their source folders.
// The subsequent folder sync pass will copy the archived mail to the destination.
func (T *KW_TO_KWTask) ArchiveUserMail(src_user KiteUser) error {
	src_sess := T.SRC.Session(src_user.Email)

	mails, err := src_sess.MailList(Query{"with": "(subject,body,rawBody,webFormId,emailFrom)", "deleted": false})
	if err != nil {
		return err
	}

	if len(mails) == 0 {
		Log("[%s]: No mail found to archive.", src_user.Email)
		return nil
	}

	Log("[%s]: Archiving %d mail messages...", src_user.Email, len(mails))

	for _, m := range mails {
		if err := src_sess.Mail(m.ID).Archive(fmt.Sprintf("Migrated Mail For %s", src_user.Email)); err != nil {
			Err("[%s]: Error archiving mail '%s': %v", src_user.Email, m.Subject, err)
		} else {
			T.mail_count.Add(1)
		}
	}

	return nil
}

// ProcessFolder processes a folder and its subfolders.
func (T *KW_TO_KWTask) ProcessFolder(migration_users *MigrateUser, folder *KiteObject) {
	// Folder is already complete, return to caller.
	var folders []*KiteObject

	folders = append(folders, folder)

	var n int
	var next []*KiteObject

	for {
		if len(folders) < n+1 {
			folders = folders[0:0]
			if len(next) > 0 {
				for i, o := range next {
					if T.limiter.Try() {
						go func(migration_users *MigrateUser, folder *KiteObject) {
							defer T.limiter.Done()
							T.ProcessFolder(migration_users, folder)
						}(migration_users, o)
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
			if err := T.CloneFolder(migration_users, folders[n]); err != nil {
				Err("%s - %s: %v", migration_users.src_sess.Username, folders[n].Path, err)
				n++
				continue
			}
			childs, err := migration_users.src_sess.Folder(folders[n].ID).Folders()
			if err == nil {
				for i := 0; i < len(childs); i++ {
					if childs[i].Type == "d" {
						next = append(next, &childs[i])
					}
				}
			} else {
				Err("%s - %s: %v", migration_users.src_sess.Username, folders[n].Path, err)
			}
		}
		n++
	}
	return
}
