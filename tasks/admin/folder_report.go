package admin

import (
	. "github.com/cmcoffee/kitebroker/core"
	"strings"
)

type FolderReportTask struct {
	input struct {
		user_emails []string
	}
	KiteBrokerTask
}

func (T FolderReportTask) New() Task {
	return new(FolderReportTask)
}

func (T FolderReportTask) Name() string {
	return "folder_report"
}

func (T FolderReportTask) Desc() string {
	return "Provides permission details of folders in Kiteworks."
}

func (T *FolderReportTask) Init() (err error) {
	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "Users to specify, blank for all users.")
	err = T.Flags.Parse()
	return
}

func (T *FolderReportTask) Main() (err error) {
	params := Query{"active": true, "verified": true, "allowsCollaboration": true}
	user_getter, err := T.KW.Admin().Users(T.input.user_emails, 0, params)
	if err != nil {
		return err
	}

	for {
		users, err := user_getter.Next()
		if err != nil {
			return err
		}
		if len(users) == 0 {
			break
		}
		for _, user := range users {
			sess := T.KW.Session(user.Email)
			var folders []*KiteObject
			if err := sess.DataCall(APIRequest{
				Method: "GET",
				Path:   "/rest/folders/top",
				Params: SetParams(Query{"deleted": false, "with": "(currentUserRole)"}),
				Output: &folders,
			}, -1, 1000); err != nil {
				Err("%s: %v", user.Email, err)
				continue
			}
			for _, v := range folders {
				// Only process folders this user owns.
				if v.CurrentUserRole.ID != 5 {
					continue
				}
				T.ProcessFolder(&sess, &user, v)
			}
		}
	}
	return
}

func (T *FolderReportTask) ReportFolder(sess *KWSession, user *KiteUser, folder *KiteObject) {
	members, err := sess.Folder(folder.ID).Members()
	if err != nil {
		Err("%s - %s: %v", sess.Username, folder.Path, err)
		return
	}
	for _, m := range members {
		Log("\"/%s\",%s,%s", folder.Path, strings.ToLower(m.Role.Name), strings.ToLower(m.User.Email))
	}

}

func (T *FolderReportTask) ProcessFolder(sess *KWSession, user *KiteUser, folder *KiteObject) {
	var next []*KiteObject
	childs, err := sess.Folder(folder.ID).Folders()
	if err == nil {
		for i := 0; i < len(childs); i++ {
			if childs[i].Type == "d" {
				next = append(next, &childs[i])
			}
		}
	} else {
		Err("%s - %s: %v", sess.Username, folder.Path, err)
	}

	T.ReportFolder(sess, user, folder)
	for _, v := range next {
		T.ProcessFolder(sess, user, v)
	}

	return
}
