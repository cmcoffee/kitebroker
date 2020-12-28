package admin

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"strings"
	"time"
)

type FolderFileExpiryTask struct {
	input struct {
		profile_id         int
		user_emails        []string
		exclude_my_folder  bool
		folders            []string
		folder_days        int
		file_days          int
		resume             bool
		recover_deleted    bool
		dont_extend        bool
		dont_modify_folder bool
	}
	users        Table
	profiles     Table
	limiter      LimitGroup
	user_count   Tally
	folder_count Tally
	KiteBrokerTask
}

func (T FolderFileExpiryTask) New() Task {
	return new(FolderFileExpiryTask)
}

func (T FolderFileExpiryTask) Name() string {
	return "folder_file_expiry"
}

func (T FolderFileExpiryTask) Desc() string {
	return "Modifies the folder and file expiry."
}

func (T *FolderFileExpiryTask) Init() (err error) {
	all_users := T.Flags.Bool("all_users", false, "Apply folder and file limits to everyone in all profiles.")
	T.Flags.BoolVar(&T.input.exclude_my_folder, "exclude_my_folder", false, "Exclude adding expirations to files in My Folder.")
	T.Flags.IntVar(&T.input.profile_id, "profile_id", 0, "Target Profile ID.")
	T.Flags.MultiVar(&T.input.user_emails, "users", "user@domain.com", "Users to specify.")
	T.Flags.IntVar(&T.input.folder_days, "folder_days", -1, "Expiry in days for folders.\t(Overrides profile, '-1' = don't override.)")
	T.Flags.IntVar(&T.input.file_days, "file_days", -1, "Expiry in days for files.\t(Overrides profile, '-1' = don't override.)")
	T.Flags.MultiVar(&T.input.folders, "folder", "<My Folder>", "Specify folder name you want to modify.")
	T.Flags.BoolVar(&T.input.resume, "resume", false, "Resume previous update of folder/file expiration.")
	T.Flags.BoolVar(&T.input.recover_deleted, "undelete_all", false, "Undelete all deleted folders and files.")
	T.Flags.BoolVar(&T.input.dont_extend, "dont_extend", false, "Don't extend expiration of individual files.")
	T.Flags.BoolVar(&T.input.dont_modify_folder, "dont_modify_folder", false, "Don't modify folder properties.")
	T.Flags.Order("folder_days", "file_days")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	if !*all_users && T.input.profile_id < 1 && len(T.input.user_emails) == 0 {
		err = fmt.Errorf("Please specify --all_users, and/or select users based on --profile_id or --users.")
	}

	if T.input.folder_days > -1 {
		if T.input.folder_days > 0 && T.input.file_days > T.input.folder_days {
			return fmt.Errorf("--file_days cannot be greater than --folder_days.")
		}
		if T.input.file_days < 0 {
			return fmt.Errorf("--file_days must be set if --folder_days is set.")
		}
	}

	if T.input.file_days > -1 {
		if T.input.folder_days < 0 {
			return fmt.Errorf("--folder_days must be set if --file_days is set.")
		}
	}

	return
}

func (T *FolderFileExpiryTask) Main() (err error) {
	type Folder struct {
		ID              int            `json:"ID"`
		CurrentUserRole KitePermission `json:"currentUserRole"`
	}

	T.user_count = T.Report.Tally("Analyzed Users")
	T.folder_count = T.Report.Tally("Folders Updated")
	T.users = T.DB.Table("users")
	T.profiles = OpenCache().Table("profiles")

	T.limiter = NewLimitGroup(50)

	params := Query{"active": true, "verified": true, "allowsCollaboration": true}

	if len(T.input.user_emails) == 0 && T.input.profile_id > 0 {
		user_emails, err := T.KW.Admin().FindProfileUsers(T.input.profile_id, params)
		if err != nil {
			return err
		}
		T.input.user_emails = append(T.input.user_emails, user_emails[0:]...)
	}

	user_count, err := T.KW.Admin().UserCount(T.input.user_emails, params)
	if err != nil {
		return err
	}

	message := func() string {
		return fmt.Sprintf("Please wait ... [users: %d of %d/folders: %d]", T.user_count.Value(), user_count, T.folder_count.Value())
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

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
			Log("Updating folders for %s ..", user.Email)
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
					f, err := sess.Folder(0).Find(v)
					if err != nil {
						Err("%s: [%s]: %v", user.Email, v, err)
						continue
					}
					folders = append(folders, &f)
				}
			}
			for _, v := range folders {
				// Only process folders this user owns.
				if v.CurrentUserRole.Rank < 500000 {
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
	return nil
}

// Finds out the expiration settings for the user in question.
func (T *FolderFileExpiryTask) GetProfileExpiration(user *KiteUser) (folder_expiry int, file_expiry int, err error) {
	var profile struct {
		Features struct {
			FileTime   int `json:"fileLifetime"`
			FolderTime int `json:"folderExpirationLimit"`
		} `json:"features"`
	}

	if found := T.profiles.Get(fmt.Sprintf("%d", user.UserTypeID), &profile); found {
		return profile.Features.FolderTime, profile.Features.FileTime, nil
	}

	err = T.KW.Session(user.Email).Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/profiles/%d", user.UserTypeID),
		Output: &profile,
	})
	if err != nil {
		return -1, -1, err
	}

	if T.input.file_days >= 0 {
		profile.Features.FileTime = T.input.file_days
	}
	if T.input.folder_days >= 0 {
		profile.Features.FolderTime = T.input.folder_days
	}
	T.profiles.Set(fmt.Sprintf("%d", user.UserTypeID), &profile)
	return profile.Features.FolderTime, profile.Features.FileTime, nil
}

// Updates folder to profile expiry
func (T *FolderFileExpiryTask) ModifyFolder(sess *KWSession, user *KiteUser, folder *KiteObject) (err error) {

	folder_days, file_days, err := T.GetProfileExpiration(user)
	if err != nil {
		return err
	}

	if folder_days < file_days && folder_days != 0 {
		file_days = folder_days
	}

	var params []interface{}

	if folder_days > 0 {
		if folder_days == file_days {
			file_days--
		}
	}

	if !T.input.dont_modify_folder {
		if len(strings.Split(folder.Path, "/")) == 1 {
			if folder_days != 0 {
				t := time.Now().UTC().Add((time.Hour * 24) * time.Duration(folder_days))
				params = SetParams(PostForm{"expire": DateString(t), "fileLifetime": file_days})
			} else {
				params = SetParams(PostForm{"expire": 0, "fileLifetime": file_days})
			}
		} else {
			params = SetParams(PostForm{"fileLifetime": file_days})
		}
	}

	err = sess.Call(APIRequest{
		Version: 15,
		Method:  "PUT",
		Path:    SetPath("/rest/folders/%d", folder.ID),
		Params:  SetParams(params, PostForm{"applyFileLifetimeToFiles": !T.input.dont_extend}),
	})
	if err != nil && IsAPIError(err, "ERR_ENTITY_IS_SYNC_DIR") {
		err = T.ChangeMyFolderFiles(sess, user, folder)
		if err != nil {
			return err
		}
	}
	T.folder_count.Add(1)
	return
}

// Sets all files within My Folder to an expiration date.
func (T *FolderFileExpiryTask) ChangeMyFolderFiles(sess *KWSession, user *KiteUser, folder *KiteObject) (err error) {
	if T.input.exclude_my_folder || T.input.dont_extend {
		return nil
	}

	folder_expiry, file_expiry, err := T.GetProfileExpiration(user)
	if err != nil {
		return err
	}

	var files []KiteObject

	err = sess.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%d/files", folder.ID),
		Params: SetParams(Query{"deleted": false}),
		Output: &files,
	}, -1, 1000)
	if err != nil {
		return err
	}

	if file_expiry == 0 {
		file_expiry = folder_expiry
	}

	expiry_time := time.Now().UTC().Add((time.Hour * 24) * time.Duration(file_expiry))
	for _, file := range files {
		err = sess.Call(APIRequest{
			Method: "PUT",
			Path:   SetPath("/rest/files/%d", file.ID),
			Params: SetParams(PostJSON{"expire": WriteKWTime(expiry_time)}),
		})
		if err != nil {
			Err("%s - %s/%s: %v", user.Email, folder.Path, file.Name, err)
		}
	}
	T.folder_count.Add(1)
	return
}

func (T *FolderFileExpiryTask) Recover(sess *KWSession, folder *KiteObject) (err error) {
	if !T.input.recover_deleted {
		return nil
	}

	children, err := sess.Folder(folder.ID).Contents(SetParams(Query{"deleted": true}))
	if err != nil {
		return err
	}
	for _, child := range children {
		if child.Type == "d" {
			Log("%s - %s/%s: Recovering folder from trash.", sess.Username, folder.Path, child.Name)
			err = sess.Folder(child.ID).Recover()
			if err != nil {
				Err("%s - %s: %s", sess.Username, child.Path, err.Error())
			}
		} else {
			Log("%s - %s/%s: Recovering file from trash.", sess.Username, folder.Path, child.Name)
			err = sess.File(child.ID).Recover()
			if err != nil {
				Err("%s - %s: %s", sess.Username, child.Path, err.Error())
			}
		}
	}
	return
}

func (T *FolderFileExpiryTask) ProcessFolder(sess *KWSession, user *KiteUser, folder *KiteObject) {
	// Folder is already complete, return to caller.
	var folders []*KiteObject

	folders = append(folders, folder)

	var n int
	var next []*KiteObject

	err := T.Recover(sess, folder)
	if err != nil {
		Err("%s - %s: %v", sess.Username, folder.Path, err)
	}

	for {
		if len(folders) < n+1 {
			folders = folders[0:0]
			if len(next) > 0 {
				for i, o := range next {
					if T.limiter.Try() {
						go func(sess *KWSession, user *KiteUser, folder *KiteObject) {
							defer T.limiter.Done()
							T.ProcessFolder(sess, user, folder)
						}(sess, user, o)
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
			err = T.Recover(sess, folders[n])
			if err != nil {
				Err("%s - %s: %v", sess.Username, folder.Path, err)
			}
			if err := T.ModifyFolder(sess, user, folders[n]); err != nil {
				Err("%s - %s: %v", sess.Username, folders[n].Path, err)
				n++
				continue
			}
			childs, err := sess.Folder(folders[n].ID).Folders()
			if err == nil {
				for i := 0; i < len(childs); i++ {
					if childs[i].Type == "d" {
						next = append(next, &childs[i])
					}
				}
			} else {
				Err("%s - %s: %v", sess.Username, folders[n].Path, err)
			}
		}
		n++
	}
	return
}
