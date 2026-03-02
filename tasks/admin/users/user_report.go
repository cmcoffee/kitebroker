package admin

import (
	"fmt"

	. "github.com/cmcoffee/kitebroker/core"
	"github.com/cmcoffee/snugforge/nfo"
)

func init() { RegisterAdminTask(new(UserReportTask)) }

type UserReportTask struct {
	input struct {
		output_file string
		unverified  bool
		suspended   bool
	}
	KiteBrokerTask
}

func (T UserReportTask) Name() string {
	return "user_report"
}

func (T UserReportTask) Desc() string {
	return "Users:Generate report of users on system."
}

// Init function.
func (T *UserReportTask) Init() (err error) {
	T.Flags.StringVar(&T.input.output_file, "write_to_csv", "<user_report.csv>", "Output file for user report.")
	T.Flags.BoolVar(&T.input.suspended, "suspended", "Filter only suspended accounts")
	T.Flags.BoolVar(&T.input.unverified, "unverified", "Filter only unverified accounts")
	run := T.Flags.Bool("run", "Execute the task.")
	T.Flags.Order("run")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	if !*run {
		return fmt.Errorf("Please specify --run to execute this task.")
	}

	return
}

// WriteCSV writes user report data as a comma-separated value to stdout.
// It takes user details and writes them as a single line to the output.
func (T *UserReportTask) WriteCSV(username string, role string, profile string, account_type string, status string, locked string, created string, last_activity string) (err error) {
	// Implementation for writing CSV data to the output file
	// Example: Write to a CSV file using a CSV writer
	// err = csvWriter.Write([]string{username, role, profile, account_type, status, locked, created, last_activity})
	created_date_time, err := ReadKWTime(created)
	if err == nil {
		Stdout(created_date_time)
	}
	nfo.Stdout("%s,%s,%s,%s,%s,%s,%s,%s", username, role, profile, account_type, status, locked, created, last_activity)
	return nil
}

func (T *UserReportTask) Main() (err error) {
	user_getter, err := T.KW.Admin().Users(nil, 0)
	if err != nil {
		return err
	}
	profile_map, err := T.KW.Profiles()
	if err != nil {
		return err
	}

	roles, err := T.KW.AdminRoles()
	if err != nil {
		return err
	}

	err = T.WriteCSV("Username", "Role", "Profile", "Account Type", "Status", "Deactivated", "Created(GMT)", "Last Activity(GMT)")
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
			if user.Deleted {
				continue
			}
			if T.input.unverified && !user.Verified {
				continue
			}
			if T.input.suspended && !user.Suspended {
				continue
			}
			var profile_name string
			if profile, ok := profile_map[user.UserTypeID]; ok {
				profile_name = profile.Name
			} else {
				profile_name = "Unknown"
			}

			locked := "No"
			if user.Deactivated {
				locked = "Yes"
			}
			account_type := "External"
			if user.Internal {
				account_type = "Internal"
			}

			flags := BitFlag(user.Flags)

			if flags.Has(16) {
				account_type = "Shared mailbox"
			}

			email := user.Email
			if flags.Has(2) {
				email = fmt.Sprintf("%s (LDAP)", user.Email)
			} else if flags.Has(4) {
				email = fmt.Sprintf("%s (SSO)", user.Email)
			}

			var status string
			if !user.Verified {
				status = "Unverified"
			} else if user.Suspended {
				status = "Suspended"
			} else {
				status = "Active"
			}

			var role_name string
			role := roles[user.AdminRoleID]

			if IsBlank(role.Name) {
				role_name = "User"
			} else {
				if role.Name == "System" {
					role.Name = "Sys Admin"
				}
				if role.Name == "Application" {
					role.Name = "App Admin"
				}
				role_name = fmt.Sprintf("\"%s,User\"", role.Name)
			}

			err = T.WriteCSV(email, role_name, profile_name, account_type, status, locked, user.Created, user.LastActivityDateTime)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
