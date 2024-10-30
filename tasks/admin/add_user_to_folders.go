package admin

import (
	"fmt"
	. "kitebroker/core"
)

type AddUserTask struct {
	input struct {
		all_users     bool
		user_emails   []string
		profile_id    int
		folders       []string
		user_to_add   string
		group_to_add  int
		perm_string   string
		permission_id int
	}
	limiter       LimitGroup
	user_count    Tally
	folder_count  Tally
	skipped_users int64
	KiteBrokerTask
}

func (T AddUserTask) New() Task {
	return new(AddUserTask)
}

func (T AddUserTask) Name() string {
	return "add_user_to_folder"
}

func (T AddUserTask) Desc() string {
	return "Adds Downloader to top-level folders."
}

func (T *AddUserTask) Init() (err error) {
	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "User(s) to target.")
	T.Flags.MultiVar(&T.input.folders, "folders", "<My Folder>", "Folder(s) to check and clean.")
	T.Flags.IntVar(&T.input.profile_id, "profile_id", 0, "Target Profile ID.")
	T.Flags.BoolVar(&T.input.all_users, "all_users", "Add user to all folders.")
	T.Flags.StringVar(&T.input.user_to_add, "user", "<user to add to folders>", "User account to add to folders.")
	T.Flags.IntVar(&T.input.group_to_add, "group_id", -1, "LDAP Group ID to add to folders.")
	T.Flags.StringVar(&T.input.perm_string, "permission", "downloader", "Permission to add to folders.")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	if !T.input.all_users && T.input.profile_id < 1 && len(T.input.user_emails) == 0 {
		err = fmt.Errorf("Select users based on --profile_id or --users.")
	}

	if IsBlank(T.input.user_to_add) && T.input.group_to_add == -1 {
		err = fmt.Errorf("Must provide a --user or --group_id.")
	}

	return
}

func (T *AddUserTask) Main() (err error) {
	T.limiter = NewLimitGroup(50)
	T.user_count = T.Report.Tally("Analyzed Users")
	T.folder_count = T.Report.Tally("Folders Updated")

	params := Query{"active": true, "verified": true, "allowsCollaboration": true}
	user_getter, err := T.KW.Admin().Users(T.input.user_emails, T.input.profile_id, params)
	if err != nil {
		return err
	}

	T.input.permission_id, err = T.KW.FindPermission(T.input.perm_string)
	if err != nil {
		return err
	}

	total_users := user_getter.Total()

	message := func() string {
		return fmt.Sprintf("Please wait ... [users: %d of %d/folders: %d]", T.user_count.Value(), total_users, T.folder_count.Value())
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	for {
		users, err := user_getter.Next()
		if err != nil {
			return err
		}
		if len(users) == 0 {
			break
		}
		for _, user := range users {
			if user.Email == T.input.user_to_add {
				total_users--
				continue
			}
			if !user.IsActive() {
				Notice("%s: account is not currently active, skipping user.", user.Email)
				total_users--
				continue
			}
			Log("Adding %s to folders owned by %s ..", T.input.user_to_add, user.Email)
			var folders []*KiteObject
			sess := T.KW.Session(user.Email)
			if len(T.input.folders) == 0 {
				if err := sess.DataCall(APIRequest{
					Method: "GET",
					Path:   "/rest/folders/top",
					Params: SetParams(Query{"deleted": false, "with": "(currentUserRole)"}),
					Output: &folders,
				}, -1, 1000); err != nil {
					Err("%s: %v", user.Email, err)
					continue
				}
			} else {
				for _, v := range T.input.folders {
					f, err := sess.Folder("0").Find(v)
					if err != nil {
						Err("%s: [%s]: %v", user.Email, v, err)
						continue
					}
					folders = append(folders, &f)
				}
			}
			T.user_count.Add(1)
			for _, v := range folders {
				// Only process folders this user owns.
				if v.CurrentUserRole.ID != 5 {
					continue
				}
				T.limiter.Add(1)
				go func(sess KWSession, user KiteUser, folder *KiteObject) {
					defer T.limiter.Done()
					T.ProcessFolder(&sess, &user, folder)
				}(sess, user, v)
			}
			T.limiter.Wait()

		}
	}

	return nil
}

func (T *AddUserTask) ProcessFolder(sess *KWSession, user *KiteUser, folder *KiteObject) {
	if folder.Path != "My Folder" {
		err := sess.Folder(folder.ID).AddUsersToFolder([]string{T.input.user_to_add}, 2, false, false)
		if err != nil && !IsAPIError(err, "ERR_ENTITY_ROLE_IS_ASSIGNED") {
			Err("[%s]: Could not add %s to %s: %v", user.Email, T.input.user_to_add, folder.Path, err)
		} else {
			Log("[%s]: Added %s to %s as downloader.", user.Email, T.input.user_to_add, folder.Path)
		}
	} else {
		folders, err := sess.Folder(folder.ID).Folders()
		if err != nil {
			Err("[%s]: Error getting 'My Folder' folders: %v", user.Email, err)
		}
		for _, f := range folders {
			T.ProcessFolder(sess, user, &f)
		}
	}
}
