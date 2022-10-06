package admin

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"time"
)

type FileCleanerTask struct {
	input struct {
		all_users bool
		user_emails []string
		profile_id   int
		folders []string
		max_file_age int
		perm_delete bool
		resume bool
		dry_run bool
	}
	user_count   Tally
	limiter LimitGroup
	files_removed Tally
	files_count Tally
	folders_count Tally
	space_recovered Tally
	pcache Table
	users        Table
	KiteBrokerTask
}

func (T FileCleanerTask) New() Task {
	return new(FileCleanerTask)
}

func (T FileCleanerTask) Name() string {
	return "file_cleanup"
}

func (T FileCleanerTask) Desc() string {
	return "Remove files from system older than a specified date."
}

func (T *FileCleanerTask) Init() (err error) {
	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "User(s) to cleanup.")
	T.Flags.MultiVar(&T.input.folders, "folders", "<My Folder>", "Folder(s) to check and clean.")
	T.Flags.IntVar(&T.input.max_file_age, "max_days", -1, "Maximum lifetime of file, delete anything older.")
	T.Flags.BoolVar(&T.input.perm_delete, "perm", "Permanently delete files older than --max_days.")
	T.Flags.BoolVar(&T.input.dry_run, "dry_run", "Don't actually delete, just show expired files.")
	T.Flags.BoolVar(&T.input.resume, "resume", "Resume previous deletion of files.")
	T.Flags.IntVar(&T.input.profile_id, "profile_id", 0, "Target Profile ID.")
	T.Flags.BoolVar(&T.input.all_users, "all_users", "Clean out all users files past their profile expiration.")
	T.Flags.Order("users","folder","max_days","perm")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	if !T.input.all_users && T.input.profile_id < 1 && len(T.input.user_emails) == 0 {
		err = fmt.Errorf("Select users based on --profile_id or --users.")
	}


	return
}

func (T *FileCleanerTask) Main() (err error) {
	T.limiter = NewLimitGroup(50)
	T.pcache = OpenCache().Table("profiles")
	T.folders_count = T.Report.Tally("Folders Analyzed")
	T.files_count = T.Report.Tally("Files Analyzed")
	T.user_count = T.Report.Tally("Analyzed Users")
	T.users = T.DB.Table("users")
	params := Query{"active": true, "verified": true, "allowsCollaboration": true}

	if !T.input.dry_run {
		T.space_recovered = T.Report.Tally("Storage Freed", HumanSize)
		T.files_removed = T.Report.Tally("Files Removed")
	} else {
		T.files_removed = T.Report.Tally("Simulated Files Removed")
		T.space_recovered = T.Report.Tally("Simulated Storage Freed", HumanSize)
	}

	message := func() string {
		return fmt.Sprintf("Working .. [Files Analyzed: %d/Deleted: %d(%s)]", T.files_count.Value(), T.files_removed.Value(), HumanSize(T.space_recovered.Value()))
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	if len(T.input.user_emails) == 0 && T.input.profile_id > 0 {
		user_emails, err := T.KW.Admin().FindProfileUsers(T.input.profile_id, params)
		if err != nil {
			return err
		}
		T.input.user_emails = append(T.input.user_emails, user_emails[0:]...)
	}

	/*user_count, err := T.KW.Admin().UserCount(T.input.user_emails, params)
	if err != nil {
		return err
	}*/

	user_getter := T.KW.Admin().Users(T.input.user_emails, params)

	for {
		users, err := user_getter.Next()
		if err != nil {
			return err
		}
		if len(users) == 0 {
			break
		}	
		for _, user := range users {
			err_start := ErrCount()
			T.user_count.Add(1)
			if T.input.resume && T.users.Get(user.Email, nil) {
				continue
			}
			if T.input.profile_id > 0 && user.UserTypeID != T.input.profile_id {
				Log("Skipping %s, user does not match required profile_id of %d.", user.Email, T.input.profile_id)
				continue
			}
			if user.Suspended || user.Deactivated || !user.Verified || !user.Active {
				continue
			}
			if _, err := T.GetFileExpiration(&user); err != nil && err == ErrNoExpire {
				continue
			}
			Log("Scanning folders owned by %s ..", user.Email)
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
			if ErrCount()-err_start == 0 {
				T.users.Set(user.Email, 1)
			}
		}
	}
	// If we didn't have any errors, we don't need to resume.
	if ErrCount() == 0 {
		T.DB.Drop("users")
	}

	T.limiter.Wait()
 	return nil

}

func (T *FileCleanerTask) getExpiryTime(days int) time.Time {
		return time.Now().UTC().Add((time.Hour * 24) * time.Duration(days) * -1)
}

var ErrNoExpire = fmt.Errorf("No File Expiry.")

// Finds out the expiration settings for the user in question.
func (T *FileCleanerTask) GetFileExpiration(user *KiteUser) (file_expiration time.Time, err error) {
	var profile struct {
		Features struct {
			FileTime   int `json:"fileLifetime"`
		} `json:"features"`
	}

	if found := T.pcache.Get(fmt.Sprintf("%d", user.UserTypeID), &profile); found {
		if profile.Features.FileTime <= 0 {
			return T.getExpiryTime(9999999), ErrNoExpire
		}
		return T.getExpiryTime(profile.Features.FileTime), nil
	}

	if T.input.max_file_age >= 0 {
		profile.Features.FileTime = T.input.max_file_age
	} else {
		err = T.KW.Session(user.Email).Call(APIRequest{
			Method: "GET",
			Path:   SetPath("/rest/profiles/%d", user.UserTypeID),
			Output: &profile,
		})
		if err != nil {
			return
		}

	}

	T.pcache.Set(fmt.Sprintf("%d", user.UserTypeID), &profile)
	if profile.Features.FileTime <= 0 {
		return T.getExpiryTime(9999999), ErrNoExpire
	}
	return T.getExpiryTime(profile.Features.FileTime), nil
}

func (T *FileCleanerTask) CheckFile(sess *KWSession, user *KiteUser, file *KiteObject) {
	T.files_count.Add(1)
	var err error

	expiry, err := T.GetFileExpiration(user)
	if err != nil {
		Err(err)
		return
	}

	mod_time, err := ReadKWTime(file.Modified)
	if err != nil {
		Err("%s: Could not parse modified time on file: %s", file.Path, file.Modified)
		return
	}

	if mod_time.Unix() < expiry.Unix() {
		Log("file: %s (%s) - last modified: %s", file.Path, HumanSize(file.Size), mod_time)
	} else {
		return
	}

	if T.input.dry_run {
		T.space_recovered.Add64(file.Size)
		T.files_removed.Add(1)
		return
	}

	err = sess.File(file.ID).Delete()
	if err == nil {
		T.files_removed.Add(1)
		T.space_recovered.Add64(file.Size)
		if T.input.perm_delete {
			err = sess.File(file.ID).PermDelete()
		}
	}
	if err != nil {
		Err("%s: %v", file.Path, err)
	}
}

func (T *FileCleanerTask) ProcessFolder(sess *KWSession, user *KiteUser, folder *KiteObject) {
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
						go func(folder *KiteObject) {
							defer T.limiter.Done()
							T.ProcessFolder(sess, user, folder)
						}(o)
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
			T.folders_count.Add(1)
			childs, err := sess.Folder(folders[n].ID).Contents()
			if err == nil {
				for i := 0; i < len(childs); i++ {
					if childs[i].Type == "d" {
						next = append(next, &childs[i])
					} else if childs[i].Type == "f" {
						T.CheckFile(sess, user, &childs[i])
					}
				}
			} else {
				Err("%s: %v", folders[n].Path, err)
			}
		} else if folders[n].Type == "f" {
			T.CheckFile(sess, user, folders[n])
		}
		n++
	}
	return


}