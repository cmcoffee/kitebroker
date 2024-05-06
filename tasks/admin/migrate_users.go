package admin

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"strings"
	"time"
)

type MigrateProfileTask struct {
	// input variables
	input struct {
		dst_profile_id      int
		src_profile_id      int
		src_admin_user      string
		src_server          string
		src_appid           string
		src_secret          string
		src_sig             string
		src_redirect        string
		user_emails         []string
		delete_user_first   bool
		cleanup             bool
		deactivate_src_user bool
	}
	old_domain          string
	new_domain          string
	SRC                 KWAPI
	users               map[string]struct{}
	limiter             LimitGroup
	folders_count       Tally
	files_count         Tally
	files_copied        Tally
	transfer_counter    Tally
	src_dst_profile_map map[int]int
	// Required for all tasks
	KiteBrokerTask
}

func (T MigrateProfileTask) New() Task {
	return new(MigrateProfileTask)
}

func (T MigrateProfileTask) Name() string {
	return "migrate_profile"
}

func (T MigrateProfileTask) Desc() string {
	return "Migrate profile and users from remote kiteworks server."
}

func (T *MigrateProfileTask) Init() (err error) {

	var (
		secret string
		sig    string
	)

	T.SRC.APIClient = new(APIClient)

	T.Flags.IntVar(&T.input.dst_profile_id, "dest_profile_id", 0, "Destination KW Profile ID")
	T.Flags.IntVar(&T.input.src_profile_id, "src_profile_id", 0, "Source Profile ID")
	T.Flags.StringVar(&T.SRC.Server, "src_server", "<source kw server>", "Source KW Server")
	T.Flags.StringVar(&T.input.src_admin_user, "src_admin", "<admin@domain.com>", "Source Admin User Account")
	T.Flags.StringVar(&T.SRC.ApplicationID, "src_app_id", "<app id>", "Source Client App ID")
	T.Flags.StringVar(&secret, "src_client_secret", "<client_secret>", "Source Client Secret")
	T.Flags.StringVar(&sig, "src_signature", "<signature secret>", "Source Signature Key")
	T.Flags.StringVar(&T.SRC.RedirectURI, "src_redirect_uri", "https://kitebroker/", "Source Redirect URI")
	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "User(s) to copy.")
	T.Flags.Order("src_profile_id", "src_server", "src_appid", "src_secret")
	T.Flags.BoolVar(&T.input.delete_user_first, "delete_user_first", "Delete destination user prior to migration.")
	T.Flags.StringVar(&T.new_domain, "new_domain", "<new_domain.com>", "Replace users domain with domain specified.")
	T.Flags.BoolVar(&T.input.cleanup, "cleanup", "Remove source files if they exist on destination already.")
	T.Flags.BoolVar(&T.input.deactivate_src_user, "deactivate", "Deactivate Users after copy command")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	if len(T.input.user_emails) == 0 && T.input.src_profile_id == 0 {
		err = fmt.Errorf("Must provide --user_emails to copy.")
		return
	}

	T.SRC.APIClient.NewToken = T.SRC.KWNewToken
	T.SRC.SetDatabase(OpenCache())
	T.SRC.Signature(sig)
	T.SRC.ClientSecret(secret)
	T.SRC.VerifySSL = false
	T.SRC.Retries = 3
	T.SRC.ConnectTimeout = time.Second * 12
	T.SRC.RequestTimeout = time.Second * 60

	return
}

func (T *MigrateProfileTask) MapProfiles() (err error) {
	T.src_dst_profile_map = make(map[int]int)
	src_profiles, err := T.SRC.Session(T.input.src_admin_user).Admin().Profiles()
	if err != nil {
		Fatal("Error talking to source server: %s", err.Error())
	}
	dst_profiles, err := T.KW.Admin().Profiles()
	if err != nil {
		Fatal("Error talking to destination server: %s", err.Error())
	}
	dst_profile_map := make(map[string]int)
	for _, v := range dst_profiles {
		dst_profile_map[strings.ToLower(v.Name)] = v.ID
	}
	if !T.input.cleanup {
		for _, v := range src_profiles {
			if x, ok := dst_profile_map[strings.ToLower(v.Name)]; ok {
				T.src_dst_profile_map[v.ID] = x
			}
		}
	}
	return nil
}

func (T *MigrateProfileTask) Main() (err error) {
	T.folders_count = T.Report.Tally("Folders Analyzed")
	T.files_count = T.Report.Tally("Files Analyzed")
	T.files_copied = T.Report.Tally("Files Copied")
	T.transfer_counter = T.Report.Tally("Total Transfered", HumanSize)

	T.SRC.SetLimiter(T.KW.APIClient.GetLimit())
	T.SRC.SetTransferLimiter(T.KW.APIClient.GetTransferLimit())

	message := func() string {
		return fmt.Sprintf("Working .. [ Folders Analyzed: %d | Files Analyzed: %d | Files Tranfered: %d(%s) ]", T.folders_count.Value(), T.files_count.Value(), T.files_copied.Value(), HumanSize(T.transfer_counter.Value()))
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	var copy_users []KiteUser

	split := strings.Split(T.input.user_emails[0], "@")
	if len(split) < 2 {
		return fmt.Errorf("Unable to obtain email domain from %s.", T.input.user_emails[0])
	}
	T.old_domain = split[1]

	Debug("Old Domain: %s, New Domain: %s", T.old_domain, T.new_domain)

	T.limiter = NewLimitGroup(50)

	T.SRC.APIClient.ProxyURI = T.KW.APIClient.ProxyURI
	T.SRC.APIClient.RequestTimeout = T.KW.RequestTimeout
	T.SRC.APIClient.ConnectTimeout = T.KW.ConnectTimeout

	// Testing, removing before done.
	//T.SRC = T.KiteBrokerTask.KW.KWAPI

	params := SetParams(Query{"active": true, "deleted": false})
	user_getter, err := T.SRC.Session(T.input.src_admin_user).Admin().Users(T.input.user_emails, T.input.src_profile_id, params)
	if err != nil {
		return err
	}

	if T.input.dst_profile_id == 0 {
		if err := T.MapProfiles(); err != nil {
			return err
		}
	}

	T.users = make(map[string]struct{})
	// Main function
	for {
		users, err := user_getter.Next()
		if err != nil {
			return err
		}
		if len(users) == 0 {
			break
		}
		for _, u := range users {
			if !T.input.cleanup {
				if T.input.delete_user_first {
					Debug("Deleting %s", T.SwapEmails(u.Email))
					user, err := T.KW.Admin().FindUser(T.SwapEmails(u.Email))
					if err != nil {
						return err
					}
					if T.SwapEmails(u.Email) != user.Email {
						continue
					}
					params := SetParams(Query{"retainData": false, "deleteUnsharedData": true})
					err = T.KW.Admin().DeleteUser(*user, params)
					if err != nil {
						return err
					}
				}

				if _, ok := T.src_dst_profile_map[u.UserTypeID]; !ok {
					Err("Could not find profile mapping for %s on destination system, skipping user.", u.Email)
					continue
				}

				if _, err := T.KW.Admin().NewUser(T.SwapEmails(u.Email), T.src_dst_profile_map[u.UserTypeID], true, false); err != nil {
					if !IsAPIError(err, "ERR_ENTITY_EXISTS") {
						Err("Could not create users %s: %v, skipping user.", u.Email, err)
						continue
					}
				}
			}
			T.users[u.Email] = struct{}{}
			copy_users = append(copy_users, u)
		}
	}
	for _, u := range copy_users {
		if err := T.CopyUser(u); err != nil {
			Err("%s: %v", u.Email, err)
		}
	}

	return
}

type MigrateUser struct {
	src      *KiteUser
	dst      *KiteUser
	src_sess KWSession
	dst_sess KWSession
}

func (T MigrateProfileTask) SwapEmails(input string) string {
	if IsBlank(T.new_domain) {
		return input
	}
	return strings.Replace(input, T.old_domain, T.new_domain, -1)
}

func (T *MigrateProfileTask) CopyUser(src_user KiteUser) (err error) {
	err = T.SRC.Session(T.input.src_admin_user).Admin().ActivateUser(src_user.ID)
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

	Log("Source User: %s, Destination User: %s", src_user.Email, dst_user.Email)

	migration_user := MigrateUser{
		&src_user,
		dst_user,
		T.SRC.Session(src_user.Email),
		T.KW.Session(dst_user.Email),
	}

	T.ProcessFolder(&migration_user, &src_folder)
	T.limiter.Wait()

	if T.input.deactivate_src_user {
		err = T.SRC.Session(T.input.src_admin_user).Admin().DeactivateUser(src_user.ID)
	}
	return

}

func (T *MigrateProfileTask) SetPerms(migration_users *MigrateUser, folder *KiteObject, members []KiteMember) (err error) {
	users := make(map[int][]string)
	for _, m := range members {
		if m.User.Email == migration_users.dst.Email {
			continue
		}
		users[m.RoleID] = append(users[m.RoleID], T.SwapEmails(m.User.Email))
	}
	for i := 0; i <= 7; i++ {
		if i == 5 {
			continue
		}
		if v, ok := users[i]; ok {
			if err := migration_users.dst_sess.Folder(folder.ID).AddUsersToFolder(v, i, false, true); err != nil {
				if !IsAPIError(err, "ERR_ENTITY_ROLE_IS_ASSIGNED") {
					Err(err)
				}
			}
		}
	}
	return
}

func (T *MigrateProfileTask) CloneFolder(migration_users *MigrateUser, folder *KiteObject) (err error) {
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
		if T.input.cleanup {
			continue
		}

		down, err := migration_users.src_sess.QDownload(&f)
		if err != nil {
			Err("%s: %s", f.Name, err.Error())
			continue
		}

		modtime, err := ReadKWTime(f.ClientModified)
		if err != nil {
			Err("%s: %s", f.Name, err.Error())
		}

		_, err = migration_users.dst_sess.Upload(f.Name, f.Size, modtime, false, false, true, dest_folder, down)
		if err != nil {
			Err("[%s] %s: %s", migration_users.dst_sess.Username, f.Name, err.Error())
		} else {
			T.files_copied.Add(1)
			T.transfer_counter.Add64(f.Size)
		}
	}

	return
}

func (T *MigrateProfileTask) ProcessFolder(migration_users *MigrateUser, folder *KiteObject) {
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
