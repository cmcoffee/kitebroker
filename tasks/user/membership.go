package user

import (
	. "github.com/cmcoffee/kitebroker/core"
	"strings"
	"fmt"
)

// Object for task.
type MembershipTask struct {
	target string
	add_users []string
	rem_users []string
	role string
	role_id int
	notify bool
	notify_files bool
	roles []KiteRoles
	KiteBrokerTask
}

func (T MembershipTask) New() Task {
	return new(MembershipTask)
}

func (T MembershipTask) Name() string {
	return "membership"
}

func (T MembershipTask) Desc() string {
	return "Modify membership task for folders."
}

// Task init function, should parse flag, do pre-checks.
func (T *MembershipTask) Init() (err error) {
	T.Flags.StringVar(&T.target, "folder", "<target folder>", "Target kiteworks folder")
	T.Flags.MultiVar(&T.add_users, "add", "<user emails>", "Users to be added to kiteworks folder.")
	T.Flags.MultiVar(&T.rem_users, "del", "<user emails>", "Users to be removed from kiteworks folder.")
	T.Flags.StringVar(&T.role, "role", "<Role(ie.. Collaborator) or Role ID>", "Role or Role ID of user being added, for list of roles run task without options.")
	T.Flags.BoolVar(&T.notify, "notify", "Notify added user of folder invite.")
	T.Flags.BoolVar(&T.notify_files, "sub", "Subscribe added user to file notifications in folder.")
	T.Flags.CLIArgs("folder")
	T.Flags.Order("folder")
	if err = T.Flags.Parse(); err != nil {
		return err
	}
	if len(T.target) == 0 {
		return fmt.Errorf("Please provide a folder you wish to add/remove users from.")
	}

	if len(T.add_users) == 0 && len(T.rem_users) == 0 {
		return fmt.Errorf("Must use either --add=<users> or --del=<users>")
	}

	return nil
}

func (T *MembershipTask) FindRoleID(input string) (id int, err error) {
	input_lower := strings.ToLower(input)
	for _, v := range T.roles {
		role_id := fmt.Sprintf("%d", v.ID)
		if input_lower == strings.ToLower(v.Name) || input_lower == role_id {
			return v.ID, nil
		}
	}
	return -1, fmt.Errorf("Requested Kiteworks Folder Role: '%s', not found.", input)
}

// Main function, Passport hands off KWAPI Session, a Database and a TaskReport object.
func (T *MembershipTask) Main() (err error) {
	T.roles, err = T.KW.Roles()
	if err != nil {
		return err
	}
	if len(T.add_users) > 0 {
		T.AddUsers()
	}
	if len(T.rem_users) > 0 {
		T.RemoveUsers()
	}
	return
}

func (T *MembershipTask) AddUsers() {
	if T.role == NONE {
		Err("You must provide a role or role id you wish to add the user as.")
		Log("")
		Log("Available Folder Roles:")
		Log("")
		for _, v := range T.roles {
			Log("  %s: %d", v.Name, v.ID)
		}
		Exit(1)
	}

	folder, err := T.KW.Folder("0").Find(T.target)
	if err != nil {
		Err(err)
		return
	}
	role_id, err := T.FindRoleID(T.role) 
	if err != nil {
		Fatal(err)
	}

	err = T.KW.Folder(folder.ID).AddUsersToFolder(T.add_users, role_id, T.notify, T.notify_files)
	if err != nil {
		Err(err)
	} else {
		Log("Successfully updated %v on %s.", T.add_users, folder.Path)
	}



}

func (T *MembershipTask) RemoveUsers() {

}
