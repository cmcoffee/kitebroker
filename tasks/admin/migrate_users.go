package admin

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"time"
	"strings"
)

type MigrateProfileTask struct {
	// input variables
	input struct {
		profile_id int
		src_admin_user string
		src_server string
		src_appid string
		src_secret string
		src_sig string
		src_redirect string
		user_emails []string
		delete_user_first bool
	}
	old_domain string
	new_domain string
	SRC KWAPI
	users map[string]struct{}
	limiter      LimitGroup
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

	T.Flags.IntVar(&T.input.profile_id, "profile_id", 0, "Destination KW Profile ID")
	T.Flags.StringVar(&T.SRC.Server, "dst_server", "<source kw server>", "Dest KW Server")
	T.Flags.StringVar(&T.input.src_admin_user, "src_admin", "<admin@domain.com>", "Source Admin User Account")
	T.Flags.StringVar(&T.SRC.ApplicationID, "src_app_id", "<app id>", "Source Client App ID")
	T.Flags.StringVar(&secret, "src_client_secret", "<client_secret>", "Source Client Secret")
	T.Flags.StringVar(&sig, "src_signature", "<signature secret>", "Source Signature Key")
	T.Flags.StringVar(&T.SRC.RedirectURI, "src_redirect_uri", "https://kitebroker/", "Source Redirect URI")
	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com", "User(s) to copy.")
	T.Flags.Order("src_profile_id","src_server","src_appid","src_secret")
	T.Flags.BoolVar(&T.input.delete_user_first, "delete_user_first", "Delete user prior to migration.")
	T.Flags.StringVar(&T.new_domain, "new_domain", "<new_domain.com>", "Replace users domain with domain specified.")
	if err := T.Flags.Parse(); err != nil {
	 	return err
	}

	if len(T.input.user_emails) == 0 {
		err = fmt.Errorf("Must provide --user_emails to copy.")
		return
	}

	if T.input.profile_id == 0 {
		err = fmt.Errorf("Must specify a destination profile id for new users.")
		return
	}

	T.SRC.APIClient.NewToken = T.SRC.KWNewToken
    T.SRC.SetDatabase(OpenCache())
    T.SRC.Signature(sig)
    T.SRC.ClientSecret(secret)
    T.SRC.SetLimiter(10)
    T.SRC.SetTransferLimiter(10)
    T.SRC.VerifySSL = false
    T.SRC.Retries = 3
    T.SRC.ConnectTimeout = time.Second * 12
	T.SRC.RequestTimeout = time.Second * 60

	return
}

func (T *MigrateProfileTask) Main() (err error) {
	var tmp_users []string

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

	T.users = make(map[string]struct{})
	// Main function
	for _, u := range T.input.user_emails {
		if T.input.delete_user_first {
			Debug("Deleting %s", T.SwapEmails(u))
			user, err := T.KW.Admin().FindUser(T.SwapEmails(u))
			if err != nil {
				return err
			}
			if T.SwapEmails(u) != user.Email {
				continue
			}
			params := SetParams(Query{"retainData": false, "deleteUnsharedData": true})
			err = T.KW.Admin().DeleteUser(*user, params)
			if err != nil {
				return err
			}
		}
		if _, err := T.KW.Admin().NewUser(T.SwapEmails(u), T.input.profile_id, true, false); err != nil {
			if !IsAPIError(err, "ERR_ENTITY_EXISTS") {
				Err("Could not create users %s: %v, skipping user.", u, err)
				continue
			}
		}
		T.users[u] = struct{}{}
		tmp_users = append(tmp_users, u)
	}
	T.input.user_emails = tmp_users

	for _, u := range T.input.user_emails {
		if err := T.CopyUser(u); err != nil {
			Err("%s: %v", u, err)
		}
	}

	return
}

type MigrateUser struct {
	src *KiteUser
	dst *KiteUser
	src_sess KWSession
	dst_sess KWSession
}

func (T MigrateProfileTask) SwapEmails(input string) string {
	if IsBlank(T.new_domain) {
		return input
	}
	return strings.Replace(input, T.old_domain, T.new_domain, -1)
}

func (T *MigrateProfileTask) CopyUser(username string) (err error) {
	src_user, err := T.SRC.Session(T.input.src_admin_user).Admin().FindUser(username)
	if err != nil {
		return err
	}

	err = T.SRC.Session(T.input.src_admin_user).Admin().ActivateUser(src_user.ID)
	if err != nil { 
		return err 
	}

	dst_user, err := T.KW.Admin().FindUser(T.SwapEmails(username))
	if err != nil {
		return err
	}

	err = T.KW.Admin().ActivateUser(dst_user.ID)
	if err != nil { 
		return err 
	}

	src_folder, err := T.SRC.Session(username).Folder(src_user.BaseDirID).Info()
	if err != nil {
		return err
	}

	Log("Source User: %s, Destination User: %s", src_user.Email, dst_user.Email)

	migration_user := MigrateUser{
		src_user,
		dst_user,
		T.SRC.Session(src_user.Email),
		T.KW.Session(dst_user.Email),
	}

	T.ProcessFolder(&migration_user, &src_folder)
	T.limiter.Wait()
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
	dest_folder, err := migration_users.dst_sess.Folder(migration_users.dst.BaseDirID).ResolvePath(folder.Path)
	if err != nil {
		return err
	}

	children, err := migration_users.src_sess.Folder(folder.ID).Files()
	if err != nil {
		return err
	}

	members, err := migration_users.src_sess.Folder(folder.ID).Members()
	if err != nil {
		return err
	}

	err = T.SetPerms(migration_users, &dest_folder, members)
	if err != nil {
		return err
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
		if IsBlank(f.ClientModified) {
			f.ClientModified = f.Created
		}

		if v, ok := file_map[f.Name]; ok {
			Debug("%s->%s: [%d vs %d] [%v vs %v]", f.Name, v.Name, v.Size, f.Size, v.ClientModified, f.ClientModified)

			if f.ClientModified == v.ClientModified {
				if v.Size != f.Size {
					Debug("%s: Deleting.", v.Name)
					if err := migration_users.dst_sess.File(v.ID).Delete(); err != nil {
						Err("%s: %v", v.Name, err)
					}
				} else {
					Debug("%s: Skipping file, already uploaded.", f.Name)
					continue
				}
			} else {
				Debug("%s: Deleting.", v.Name)
				if err := migration_users.dst_sess.File(v.ID).Delete(); err != nil {
					Err("%s: %v", v.Name, err)
				}
			}
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
			Err("%s: %s", f.Name, err.Error())
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
