package admin

import (
	"fmt"
	. "kitebroker/core"
)

type MoveMyFolder struct {
	input struct {
		user_emails []string
	}
	// Required for all tasks
	KiteBrokerTask
}

func (T *MoveMyFolder) New() Task {
	return new(MoveMyFolder)
}

func (T *MoveMyFolder) Name() string {
	return "move_my_folder"
}

func (T *MoveMyFolder) Desc() string {
	return "Relocate folders under My Folder."
}

func (T *MoveMyFolder) Init() (err error) {
	T.Flags.MultiVar(&T.input.user_emails, "user_emails", "<email@domain.com>", "Users to run on.")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	return
}

func (T *MoveMyFolder) RelocateUserMyFolder(username string) (err error) {
	Log("Processing user %s ...", username)
	user, err := T.KW.Admin().FindUser(username)
	if err != nil {
		return err
	}
	if user.Deactivated || !user.Verified || !user.Active {
		Log("%s: User is not active, skipping user.", user.Email)
		return nil
	}
	if IsBlank(user.SyncDirID) {
		Log("%s: User does not have a 'My Folder', skipping user.", user.Email)
		return nil
	}
	sub_folders, err := T.KW.Session(user.Email).Folder(user.SyncDirID).Folders()
	if err != nil {
		return fmt.Errorf("%s: %v", user.Email, err)
	}
	Log("%s: %d Sub folders found under 'My Folder'.", user.Email, len(sub_folders))
	for _, v := range sub_folders {
		err := T.KW.Session(user.Email).Folder(v.ID).MoveToFolder(user.BaseDirID)
		if err != nil {
			Err("(%s) %s: %v", user.Email, v.Name, err)
			continue
		}
		Log("(%s) Moved My Folder/%s to Top Level.", user.Email, v.Name)
	}

	return
}

func (T *MoveMyFolder) Main() (err error) {
	// Main function
	if len(T.input.user_emails) == 0 {
		user_emails, err := T.KW.Admin().GetAllUsers(SetParams(PostForm{"deleted": false, "suspended": false, "active": true}))
		if err != nil {
			return err
		}
		T.input.user_emails = append(T.input.user_emails, user_emails[0:]...)
	}

	for _, u := range T.input.user_emails {
		if err := T.RelocateUserMyFolder(u); err != nil {
			Err(err)
		}
	}
	return
}
