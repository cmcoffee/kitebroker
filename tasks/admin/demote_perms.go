package admin

import (
	"fmt"
	. "kitebroker/core"
)

type DemotePermissionsTask struct {
	input struct {
		all_users     bool
		user_emails   []string
		profile_id    int
		folders       []string
		perm_string   string
		permission_id int
	}
	limiter       LimitGroup
	user_count    Tally
	folder_count  Tally
	perm_count    Tally
	skipped_users int64
	KiteBrokerTask
}

func (T DemotePermissionsTask) New() Task {
	return new(DemotePermissionsTask)
}

func (T DemotePermissionsTask) Name() string {
	return "demote_permissions"
}

func (T DemotePermissionsTask) Desc() string {
	return "Demotes folder permissions from a profile, or a user."
}

func (T *DemotePermissionsTask) Init() (err error) {
	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "User(s) to target.")
	T.Flags.MultiVar(&T.input.folders, "folders", "<My Folder>", "Folder(s) to check and clean.")
	T.Flags.IntVar(&T.input.profile_id, "profile_id", 0, "Target Profile ID.")
	T.Flags.StringVar(&T.input.perm_string, "permission", "downloader", "Permission to downgrade to.")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	if T.input.profile_id < 1 && len(T.input.user_emails) == 0 {
		err = fmt.Errorf("Select users based on --profile_id or --users.")
	}

	return
}

func (T *DemotePermissionsTask) Main() (err error) {
	T.limiter = NewLimitGroup(50)
	T.user_count = T.Report.Tally("Users Analyzed")
	T.folder_count = T.Report.Tally("Folders Analyzed")
	T.perm_count = T.Report.Tally("Permissions Updated")

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
		return fmt.Sprintf("Please wait ... [users: %d of %d/folders: %d/permissions updated: %d]", T.user_count.Value(), total_users, T.folder_count.Value(), T.perm_count.Value())
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
			if !user.IsActive() {
				Notice("%s: account is not currently active, skipping user.", user.Email)
				total_users--
				continue
			}
			Log("Checking folders shared with %s ..", user.Email)
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
				T.folder_count.Add(1)
				// Only process folders this user has too high of permissions, or is the owner of folder.
				if T.input.permission_id < 5 {
					if v.CurrentUserRole.ID > 5 {
						continue
					}
					if v.CurrentUserRole.ID <= T.input.permission_id {
						continue
					}
				} else if T.input.permission_id > 5 {
					if v.CurrentUserRole.ID >= T.input.permission_id {
						continue
					}
				} else {
					continue // User is folder owner, needs reassignment when permission taken away.
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

func (T *DemotePermissionsTask) ProcessFolder(sess *KWSession, user *KiteUser, folder *KiteObject) {
	o_sess := T.KW.Session(folder.UserID) // Owner session for demoting user.
	Log("Demoting permissions to %s for %s on %s ...", T.input.perm_string, user.Email, folder.Path)
	if err := o_sess.Folder(folder.ID).ChangeMember(user.ID, T.input.permission_id, true); err != nil {
		Err("[%s] %s could not be demoted to %s on %s: %v", o_sess.Username, user.Email, T.input.perm_string, folder.Path, err)
	} else {
		T.perm_count.Add(1)
	}
}
