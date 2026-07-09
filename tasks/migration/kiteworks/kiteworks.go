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
		no_ssh_keys         bool
		dont_clone_profiles bool
		setup               bool
		src_domain          string
		new_domain          string
	}
	opts                CopyOptions
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
	ssh_keys_count      Tally
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
	// Source credentials live in a single shared store (see core.SourceConfig)
	// so the migration and mirror tasks share one source configuration. Only the
	// config is shared — this task's token store stays in T.src_kw_db.
	T.src_kw_config = SourceConfig()
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
	T.Flags.BoolVar(&T.input.no_ssh_keys, "no_ssh_keys", "Do not copy SSH public keys.")
	T.Flags.BoolVar(&T.input.dont_clone_profiles, "dont_clone_profiles", "Do not clone custom user profiles onto the destination (cloning is on by default).")
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
	return SourceConfigComplete(T.src_kw_config)
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
	// Bind token minting to the SOURCE's own KWAPI receiver so authentication
	// uses the source server, JWT config, and app credentials — not the
	// destination's. (T.KW.KWNewToken would mint against the destination.)
	T.SRC.APIClient.NewToken = T.SRC.KWNewToken
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
	// Map every destination profile by name. Profiles without folder-create
	// permission (e.g. recipient, restricted) are still mapped so their users
	// are created on the destination — such users simply own no folders, so the
	// lack of folder-create is expected and not a reason to skip them.
	dst_profile_map := make(map[string]int)
	for _, v := range dst_profiles {
		dst_profile_map[strings.ToLower(v.Name)] = v.ID
	}
	Log("")
	if !T.opts.Cleanup {
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

	T.opts = CopyOptions{
		NoFiles:           T.input.no_files,
		NoMail:            T.input.no_mail,
		NoSshKeys:         T.input.no_ssh_keys,
		Cleanup:           T.input.cleanup,
		DeactivateSrcUser: T.input.deactivate_src_user,
		DeleteUserFirst:   T.input.delete_user_first,
		SrcDomain:         T.input.src_domain,
		NewDomain:         T.input.new_domain,
		DstProfileName:    T.input.dst_profile_name,
		SrcProfileName:    T.input.src_profile_name,
		UserEmails:        T.input.user_emails,
		CloneProfiles:     !T.input.dont_clone_profiles,
	}

	T.users_count = T.Report.Tally("Synced Users")
	T.FailedUsers = T.Report.Tally("Failed Users")
	T.folders_count = T.Report.Tally("Synced Folders")
	T.files_count = T.Report.Tally("Synced Files")
	T.files_copied = T.Report.Tally("Files Transferred")
	T.mail_count = T.Report.Tally("Mail Archived")
	T.ssh_keys_count = T.Report.Tally("SSH Keys Copied")
	T.transfer_counter = T.Report.Tally("Data Transferred", HumanSize)

	T.limiter = NewLimitGroup(50)
	T.users = make(map[string]struct{})
	T.failed_users = make(map[string]any)

	if T.report {
		all_users, err := T.getSourceUsers()
		if err != nil {
			return err
		}
		return T.RunReport(all_users)
	}

	message := func() string {
		return fmt.Sprintf("Working .. [ Folders: %d | Files: %d | Files Transferred: %d (%s) ]",
			T.folders_count.Value(), T.files_count.Value(), T.files_copied.Value(), HumanSize(T.transfer_counter.Value()))
	}
	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	Log("Starting Kiteworks Migration...")
	return T.RunCopy()
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
	if IsBlank(T.opts.NewDomain) {
		return input
	}
	return strings.Replace(input, T.opts.SrcDomain, T.opts.NewDomain, -1)
}

// CopyUser copies a user from the source to the destination Kiteworks system.
func (T *KW_TO_KWTask) CopyUser(src_user KiteUser) (err error) {
	T.users_count.Add(1)
	if !src_user.IsActive() {
		Debug("[%s]: Activating source user (ID=%s).", src_user.Email, src_user.ID)
		err = T.SRC.Session(T.src_admin).Admin().ActivateUser(src_user.ID)
		if err != nil {
			return err
		}
	}

	dst_user, err := T.KW.Admin().FindUser(T.SwapEmails(src_user.Email))
	if err != nil {
		return err
	}

	if !dst_user.IsActive() {
		Debug("[%s]: Activating destination user (ID=%s).", dst_user.Email, dst_user.ID)
		err = T.KW.Admin().ActivateUser(dst_user.ID)
		if err != nil {
			return err
		}
	}

	Debug("[%s]: Source base folder ID: %s", src_user.Email, src_user.BaseDirID)
	src_folder, err := T.SRC.Session(src_user.Email).Folder(src_user.BaseDirID).Info()
	if err != nil {
		return err
	}

	if !IsBlank(T.opts.SrcDomain) && (T.opts.SrcDomain != T.opts.NewDomain) {
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

	if !T.opts.NoSshKeys && !T.opts.Cleanup {
		if err := T.CopyUserSshKeys(&migration_user); err != nil {
			Err("[%s]: SSH key copy: %v", src_user.Email, err)
		}
	}

	T.ProcessFolder(&migration_user, &src_folder)
	T.limiter.Wait()

	if T.opts.DeactivateSrcUser {
		err = T.SRC.Session(T.src_admin).Admin().DeactivateUser(src_user.ID)
	}
	return

}

// CopyFolders processes only the given source folders for a user, without
// walking the rest of the tree. It is the folder-scoped counterpart to
// CopyUser: used by the differential sync so that a change in one folder does
// not trigger a re-walk of every folder the user owns. Each folder is resolved
// on the source and cloned via CloneFolder (files + permissions, no recursion).
// CloneFolder targets the destination folder by its persisted id mapping when
// available (avoiding same-name ambiguity) and otherwise resolves by path,
// creating any missing ancestor folders so a new nested folder still lands.
func (T *KW_TO_KWTask) CopyFolders(src_user KiteUser, src_folder_ids []string) (err error) {
	if len(src_folder_ids) == 0 {
		return nil
	}

	dst_user, err := T.KW.Admin().FindUser(T.SwapEmails(src_user.Email))
	if err != nil {
		return err
	}
	if dst_user == nil {
		return fmt.Errorf("destination user not found for %s", src_user.Email)
	}
	if !src_user.IsActive() {
		if err := T.SRC.Session(T.src_admin).Admin().ActivateUser(src_user.ID); err != nil {
			return err
		}
	}
	if !dst_user.IsActive() {
		if err := T.KW.Admin().ActivateUser(dst_user.ID); err != nil {
			return err
		}
	}

	migration_user := MigrateUser{
		&src_user,
		dst_user,
		T.SRC.Session(src_user.Email),
		T.KW.Session(dst_user.Email),
	}

	for _, fid := range src_folder_ids {
		folder, ferr := migration_user.src_sess.Folder(fid).Info()
		if ferr != nil {
			if IsAPIError(ferr, "ERR_ENTITY_DELETED", "ERR_ENTITY_NOT_FOUND", "ERR_ENTITY_PARENT_FOLDER_DELETED") {
				// Folder is gone on source; deletion reconcile handles removal.
				continue
			}
			Err("[%s]: folder %s lookup failed: %v", src_user.Email, fid, ferr)
			continue
		}
		if folder.Type != "d" {
			continue
		}
		if cerr := T.CloneFolder(&migration_user, &folder); cerr != nil {
			Err("[%s]: %s: %v", src_user.Email, folder.Path, cerr)
		}
	}
	return nil
}

// RunFolderCopy processes only specific changed folders per owner, resolving
// each owner to a source user and cloning just those folders (no whole-tree
// walk). changed maps an owner email to the set of source folder ids that had
// content changes. This is the folder-scoped entry point used by the mirror's
// differential sync.
func (T *KW_TO_KWTask) RunFolderCopy(changed map[string][]string) (err error) {
	// Folder cloning touches only folders/files/permissions — no user creation
	// or profile mapping is needed here.
	for owner, folder_ids := range changed {
		if len(folder_ids) == 0 {
			continue
		}
		src_user, uerr := T.SRC.Session(T.src_admin).Admin().FindUser(owner)
		if uerr != nil || src_user == nil {
			Err("Folder copy: source user %s lookup failed: %v", owner, uerr)
			continue
		}
		if ferr := T.CopyFolders(*src_user, folder_ids); ferr != nil {
			Err("Folder copy: %s: %v", owner, ferr)
		}
	}
	return nil
}

// SetPerms sets the permissions for a folder. src_folder_id is the source
// folder's ID — used for the OnPermissionGranted observer hook. The hook
// fires for every source-side perm (whether newly added or already present
// on destination) so observers can keep their state map fresh.
func (T *KW_TO_KWTask) SetPerms(migration_users *MigrateUser, src_folder_id string, folder *KiteObject, members []KiteMember) (err error) {
	perm_map := make(map[int][]string)
	var counter int
	for _, m := range members {
		if m.User.Email == migration_users.dst.Email {
			continue
		}
		email := T.SwapEmails(m.User.Email)
		perm_map[m.RoleID] = append(perm_map[m.RoleID], email)
		counter++
		T.notifyPermissionGranted(src_folder_id, email, m.RoleID, migration_users.src.Email)
	}
	existingPerms, _ := migration_users.dst_sess.Folder(folder.ID).Members()
	counter -= SkipExistingPerms(perm_map, existingPerms)
	if counter > 0 {
		Log("[%s]: %s - Adding %d permissions to folder.", migration_users.dst.Email, folder.Path, counter)
	}
	for role_id, emails := range perm_map {
		err = migration_users.dst_sess.Folder(folder.ID).AddUsersToFolder(emails, role_id, false, true)
		if err != nil {
			if IsAPIError(err, "ERR_ENTITY_ROLE_IS_ASSIGNED") {
				Debug("[%s]: %s - Role %d already assigned to %v, skipping.", migration_users.dst.Email, folder.Path, role_id, emails)
			} else {
				Err(err)
			}
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

	// Prefer the persisted src->dst folder mapping when available: it targets the
	// exact destination folder by id, avoiding the ambiguity of matching by name
	// (a user can have distinct folders with identical names, e.g. an owned
	// folder and a shared-in folder). Fall back to path resolution only when the
	// folder has no mapping yet (first time we've seen it).
	var mapped bool
	if T.opts.DstFolderResolver != nil {
		if dst_id, ok := T.opts.DstFolderResolver(folder.ID); ok && !IsBlank(dst_id) {
			dest_folder, err = migration_users.dst_sess.Folder(dst_id).Info()
			if err != nil {
				if IsAPIError(err, "ERR_ENTITY_DELETED", "ERR_ENTITY_NOT_FOUND", "ERR_ENTITY_PARENT_FOLDER_DELETED") {
					// Mapping is stale (dest folder gone) — fall back to path.
					Debug("[%s]: mapped dst folder %s for '%s' is gone; falling back to path resolution.", migration_users.dst.Email, dst_id, folder.Path)
				} else {
					return err
				}
			} else {
				mapped = true
			}
		}
	}

	if !mapped {
		if !T.opts.Cleanup {
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
	}
	Debug("[%s]: Resolved destination for '%s' (cleanup=%v, mapped=%v) -> dest_id=%s.", migration_users.dst.Email, folder.Path, T.opts.Cleanup, mapped, dest_folder.ID)

	Log("[%s]: Source: %s -> Destination: /%s", migration_users.dst.Email, folder.Path, dest_folder.Path)

	T.notifyFolderCloned(*folder, dest_folder, migration_users.src.Email)

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

	if !T.opts.Cleanup {
		err = T.SetPerms(migration_users, folder.ID, &dest_folder, new_member_list)
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
					if T.opts.Cleanup {
						continue
					}
					Debug("%s: Deleting.", v.Name)
					if err := migration_users.dst_sess.File(v.ID).Delete(); err != nil {
						Err("%s: %v", v.Name, err)
					}
				} else {
					if T.opts.Cleanup {
						if v.ClientModified == f.ClientModified && v.Fingerprint == f.Fingerprint {
							Debug("%s/%s: Deleting source as file is same on server.", v.Path, v.Name)
							if err := migration_users.src_sess.File(f.ID).Delete(); err != nil {
								Err("%s: %v", f.Name, err)
							}
							continue
						}
					} else {
						Debug("%s: Skipping file, already uploaded.", f.Name)
						T.notifyFileUploaded(f, v, folder.ID, migration_users.src.Email)
					}
					continue
				}
			} else {
				if T.opts.Cleanup {
					Debug("%s: Deleting.", v.Name)
					if err := migration_users.dst_sess.File(v.ID).Delete(); err != nil {
						Err("%s: %v", v.Name, err)
					}
				}
			}
		}

		if T.opts.Cleanup || T.opts.NoFiles {
			continue
		}

		source_info := fmt.Sprintf("%s - download - %s/%s", migration_users.src_sess.Username, dest_folder.Name, f.Name)
		dest_info := fmt.Sprintf("%s - upload - %s/%s", migration_users.dst_sess.Username, dest_folder.Name, f.Name)

		retry_download := T.KW.InitRetry(migration_users.src_sess.Username, source_info)
		retry_upload := T.KW.InitRetry(migration_users.dst_sess.Username, dest_info)
		for {
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

			uploaded, err := migration_users.dst_sess.Upload(f.Name, f.Size, modtime, false, false, true, dest_folder, down)
			if err != nil {
				if retry_upload.CheckForRetry(err) {
					continue
				} else {
					Err("%s: %s", dest_info, err.Error())
					break
				}
			}
			if uploaded != nil {
				T.files_copied.Add(1)
				T.transfer_counter.Add64(uploaded.Size)
				T.notifyFileUploaded(f, *uploaded, folder.ID, migration_users.src.Email)
			}
			break
		}
	}

	return
}

// CopyUserSshKeys mirrors the source user's SSH public keys onto the destination
// account. Dedup is by key name (Kiteworks enforces uniqueness per user). Private
// keys cannot be migrated — only public keys are stored server-side, so any
// keypairs originally generated via /generate must be re-issued by the user.
func (T *KW_TO_KWTask) CopyUserSshKeys(mu *MigrateUser) error {
	src_keys, err := mu.src_sess.MySshPublicKeys()
	if err != nil {
		if IsAPIError(err, "ERR_PROFILE_SFTP_DISABLED", "ERR_SYSTEM_ROLE_SFTP_DISABLED", "ERR_ACCESS_USER") {
			Debug("[%s]: SFTP not enabled on source — skipping SSH keys.", mu.src.Email)
			return nil
		}
		return err
	}
	if len(src_keys) == 0 {
		return nil
	}

	dst_keys, err := mu.dst_sess.MySshPublicKeys()
	if err != nil {
		if IsAPIError(err, "ERR_PROFILE_SFTP_DISABLED", "ERR_SYSTEM_ROLE_SFTP_DISABLED", "ERR_ACCESS_USER") {
			Err("[%s]: SFTP not enabled on destination — cannot copy SSH keys.", mu.dst.Email)
			return nil
		}
		return err
	}
	by_name := make(map[string]KiteSshPublicKey)
	for _, k := range dst_keys {
		by_name[k.Name] = k
	}

	for _, sk := range src_keys {
		if dk, ok := by_name[sk.Name]; ok {
			// Only record a mapping when the destination key has a real id;
			// otherwise a later prune would try to delete id 0 (ERR_ACCESS_USER).
			if dk.ID > 0 {
				Debug("[%s]: SSH key '%s' already on destination, recording mapping.", mu.dst.Email, sk.Name)
				T.notifySshKeyCopied(mu.src.Email, sk, dk)
			} else {
				Debug("[%s]: SSH key '%s' present on destination but has no id; skipping mapping.", mu.dst.Email, sk.Name)
			}
			continue
		}
		created, err := mu.dst_sess.CreateMySshPublicKey(sk.Name, sk.PublicKey)
		if err != nil {
			if IsAPIError(err, "ERR_SSH_PUBLIC_KEY_EXISTS") {
				continue
			}
			Err("[%s]: Failed to register SSH key '%s': %v", mu.dst.Email, sk.Name, err)
			continue
		}
		Log("[%s]: Copied SSH key '%s'.", mu.dst.Email, sk.Name)
		T.ssh_keys_count.Add(1)
		if created.ID > 0 {
			T.notifySshKeyCopied(mu.src.Email, sk, created)
		} else {
			Debug("[%s]: SSH key '%s' created but response carried no id; skipping mapping.", mu.dst.Email, sk.Name)
		}
	}
	return nil
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
