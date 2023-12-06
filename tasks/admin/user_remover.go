package admin

import (
	"encoding/csv"
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"os"
	"strings"
	"time"
)

type UserRemoverTask struct {
	input struct {
		all_users         bool
		profile_id        int
		user_emails       []string
		unverified        bool
		suspended         bool
		deactivated       bool
		reassign_to       string
		dry_run           bool
		retain_perms      bool
		delete_myfolder   bool
		remote_wipe       bool
		withdraw_links    bool
		external_only     bool
		limit             int
		csv_file          string
		inactive_days     uint
		ignore_inactivity bool
	}
	limit            int
	prefix           string
	user_count       Tally
	user_removed     Tally
	inactivity_time  time.Time
	read_csv_file    bool
	last_activity    map[string]time.Time
	reassign_to_sess KWSession
	reassign_to_id   int
	KiteBrokerTask
}

func (T UserRemoverTask) New() Task {
	return new(UserRemoverTask)
}

func (T UserRemoverTask) Name() string {
	return "user_remover"
}

func (T UserRemoverTask) Desc() string {
	return "Delete and reassign inactive accounts."
}

func (T *UserRemoverTask) Init() (err error) {
	T.last_activity = make(map[string]time.Time)

	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "Specify inactive user(s) to cleanup")
	T.Flags.BoolVar(&T.input.ignore_inactivity, "danger", "Overrides Inactivity Flag for accounts.")
	T.Flags.BoolVar(&T.input.all_users, "all_users", "Process all inactive users.")
	T.Flags.UintVar(&T.input.inactive_days, "inactive_days", 0, "Maximum number in days of inactivity.")
	T.Flags.BoolVar(&T.input.unverified, "unverified", "Delete only users who are unverified.")
	T.Flags.BoolVar(&T.input.suspended, "suspended", "Delete only users who are suspended.")
	T.Flags.StringVar(&T.input.reassign_to, "reassign_to", "<user@domain.com>", "User to reassign folders to.")
	T.Flags.BoolVar(&T.input.retain_perms, "retain_perms", "Retain permissions to shared data.")
	T.Flags.IntVar(&T.input.profile_id, "profile_id", 0, "Target inactive users within specified profile.")
	//T.Flags.BoolVar(&T.input.delete_myfolder, "del_my_folder", "Delete My Folders for users reassigned.")
	T.Flags.BoolVar(&T.input.dry_run, "dry_run", "Simulate removal of users without actually deleting accounts.")
	T.Flags.BoolVar(&T.input.remote_wipe, "remote_wipe", "Remote wipe any data on user's mobile device.")
	T.Flags.BoolVar(&T.input.withdraw_links, "withdraw_links", "Withdraw all file links and request file links sent by user.")
	T.Flags.BoolVar(&T.input.external_only, "external_only", "Only remove external accounts")
	T.Flags.IntVar(&T.input.limit, "limit", -1, "Limit number of accounts to remove, -1 for all users.")
	T.Flags.StringVar(&T.input.csv_file, "csv_file", "<Users-Report-1652909468.csv>", "Users Report CSV File")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	T.limit = T.input.limit

	if IsBlank(T.input.csv_file) && T.input.inactive_days > 0 {
		return fmt.Errorf("--csv_file is required if --inactive_days is specified.")
	}

	if T.input.inactive_days > 0 {
		T.inactivity_time = time.Now().UTC().Add((time.Hour * 24) * time.Duration(T.input.inactive_days) * -1)
	}

	if !IsBlank(T.input.csv_file) {
		T.read_csv_file = true
	}

	if !T.input.all_users && len(T.input.user_emails) < 1 && T.input.profile_id == 0 && !T.read_csv_file {
		return fmt.Errorf("Must either run with --all_users, provide a --profile_id or specify --users.")
	}

	if T.input.all_users && (len(T.input.user_emails) > 0 || T.input.profile_id != 0) && !T.read_csv_file {
		return fmt.Errorf("--all_users is incompatible with --users and --profile_id.")
	}

	//if IsBlank(T.input.reassign_to) {
	//	return fmt.Errorf("Must provide a --reassign_to account to proceed.")
	//}

	return
}

func (T *UserRemoverTask) Main() (err error) {
	T.user_count = T.Report.Tally("Accounts evaluated")

	if !T.input.dry_run {
		T.user_removed = T.Report.Tally("Accounts removed")
	} else {
		T.prefix = "(DRY-RUN ONLY) "
		T.user_removed = T.Report.Tally("Accounts to remove")
	}

	if T.read_csv_file {
		err = T.ReadCSV(T.input.csv_file)
		if err != nil {
			return err
		}
	}

	if !IsBlank(T.input.reassign_to) {
		T.reassign_to_sess = T.KW.Session(T.input.reassign_to)
		r, err := T.reassign_to_sess.MyUser()
		if err != nil {
			return err
		}
		T.reassign_to_id = r.ID
	}

	params := SetParams(Query{"deleted": false}, Query{"active": true})

	if T.input.unverified {
		params = SetParams(params, Query{"verified": false})
	}

	if T.input.suspended {
		params = SetParams(params, Query{"suspended": true})
	}

	if !T.read_csv_file {
		if T.input.all_users {
			user_emails, err := T.KW.Admin().GetAllUsers(params)
			if err != nil {
				return err
			}
			T.input.user_emails = append(T.input.user_emails, user_emails[0:]...)
		}
	} else {
		for k := range T.last_activity {
			T.input.user_emails = append(T.input.user_emails, k)
		}
	}
	/*
		if len(T.input.user_emails) == 0 && T.input.profile_id > 0 {
			user_emails, err := T.KW.Admin().FindProfileUsers(T.input.profile_id, params)
			if err != nil {
				return err
			}
			T.input.user_emails = append(T.input.user_emails, user_emails[0:]...)
		}
	*/
	user_list := T.input.user_emails

	if T.input.all_users {
		user_list = nil
	}

	user_getter, err := T.KW.Admin().Users(user_list, T.input.profile_id, params)
	if err != nil {
		return err
	}

	message := func() string {
		return fmt.Sprintf("Please wait ... [users: processed %d of %d, removed %d]", T.user_count.Value(), user_getter.Total(), T.user_removed.Value())
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
			if T.limit > 0 || T.limit < 0 {
				if T.RemoveUser(user) {
					T.limit--
				}
			}
			if T.limit == 0 {
				return nil
			}
		}
	}

	return nil
}

func (T *UserRemoverTask) ReadCSV(file string) (err error) {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return err
	}

	const time_format = "02 Jan 2006 15:04:05"

	pad_zero := func(in string) (out string) {
		if len(in) == 1 {
			return fmt.Sprintf("0%s", in)
		} else {
			return in
		}
	}

	rewrite_time := func(input string) string {
		split_string := strings.Split(input, "/")
		if len(split_string) == 3 {
			mn := split_string[0]
			dy := split_string[1]
			yr_split := strings.Split(split_string[2], " ")
			if len(yr_split) > 1 {
				return fmt.Sprintf("%s-%s-%sT%s:00Z", yr_split[0], pad_zero(mn), pad_zero(dy), yr_split[1])
			}
		}
		return input
	}

	for i, val := range records {
		if i == 0 {
			continue
		}
		ttp := val[7]
		if IsBlank(ttp) {
			ttp = val[6]
		}
		parse_time, err := time.Parse(time_format, ttp)
		if err != nil {
			ttp = rewrite_time(ttp)
			parse_time, err = time.Parse(time.RFC3339, ttp)
			if err != nil {
				Err("Cannot parse last activity time for %s on line %d: Expected 'DY Mon YEAR HR:MN:SC' or 'YEAR-MON-DAYTHR:MNZ', got '%s' ... skipping user.", val[0], i, ttp)
				continue
			}
		}
		email := strings.ToLower(val[0])
		email = strings.TrimSuffix(email, " (sso)")
		email = strings.TrimSuffix(email, " (ldap)")

		T.last_activity[strings.ToLower(email)] = parse_time
	}
	return nil
}

func (T UserRemoverTask) RemoveUser(input KiteUser) bool {
	T.user_count.Add(1)
	Log("Inspecting user %s...", input.Email)

	//if T.input.profile_id > 0 {
	//	if input.ProfileID != T.input.profile_id {
	//		return false
	//	}
	//}

	if !input.Deactivated {
		if input.Verified && !T.input.ignore_inactivity {
			return false
		}
		if T.inactivity_time.IsZero() {
			return false
		}
	}

	if T.input.unverified {
		if input.Verified {
			return false
		}
	}
	if T.input.suspended {
		if !input.Suspended {
			return false
		}
	}
	if T.input.external_only {
		if input.Internal {
			return false
		}
	}

	if T.input.profile_id != 0 {
		if input.UserTypeID != T.input.profile_id {
			return false
		}
	}

	if !T.inactivity_time.IsZero() {
		if last_activity, ok := T.last_activity[strings.ToLower(input.Email)]; ok {
			if T.inactivity_time.Unix() < last_activity.Unix() {
				return false
			}
			Log("%sRemoving user: %s (deactivated:%v/suspended:%v/verified:%v/last activity:%s)", T.prefix, input.Email, input.Deactivated, input.Suspended, input.Verified, strings.TrimSuffix(last_activity.String(), " +0000 UTC"))
		} else {
			return false
		}
	} else {
		Log("%sRemoving user: %s (deactivated:%v/suspended:%v/verified:%v)", T.prefix, input.Email, input.Deactivated, input.Suspended, input.Verified)
	}
	if T.input.dry_run {
		T.user_removed.Add(1)
		return true
	}

	params := SetParams(Query{"partialSuccess": false})

	if !IsBlank(T.input.reassign_to) {
		params = SetParams(params, Query{"retainToUser": T.reassign_to_id, "retainData": true})
	} else {
		params = SetParams(params, Query{"retainData": false, "deleteUnsharedData": true})
	}

	params = SetParams(params, Query{"retainPermissionToSharedData": T.input.retain_perms})
	params = SetParams(params, Query{"remoteWipe": T.input.remote_wipe})
	params = SetParams(params, Query{"withdrawFileLinks": T.input.withdraw_links, "withdrawRequestFiles": T.input.withdraw_links})

	err := T.KW.Admin().DeleteUser(input, params)
	if err != nil {
		Err(err)
		return false
	}

	/* My folder is still a protected folder.
	if T.input.delete_myfolder && !IsBlank(input.SyncDirID) {
		err = T.KW.Folder(input.SyncDirID).Delete()
		Err(err)
	}*/

	T.user_removed.Add(1)
	return true
}
