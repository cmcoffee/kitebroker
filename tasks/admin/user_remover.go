package admin

import (
	"encoding/csv"
	"fmt"
	. "kitebroker/core"
	"os"
	"strings"
	"time"
)

type UserRemoverTask struct {
	input struct {
		profile_id      int
		user_emails     []string
		unverified      bool
		suspended       bool
		deactivated     bool
		reassign_to     string
		dry_run         bool
		retain_perms    bool
		delete_myfolder bool
		remote_wipe     bool
		withdraw_links  bool
		external_only   bool
		limit           int
		csv_file        string
		inactive_days   uint
		process_active  bool
		force           bool
	}
	limit                   int
	prefix                  string
	user_count              Tally
	user_removed            Tally
	inactivity_time         time.Time
	read_csv_file           bool
	last_activity           map[string]time.Time
	reassign_to_sess        KWSession
	reassign_to_id          string
	last_activity_available bool
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

// Initializes the UserRemoverTask, parsing flags, setting up variables, and performing initial validation.
func (T *UserRemoverTask) Init() (err error) {
	T.last_activity = make(map[string]time.Time)

	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "Specify user(s) to cleanup.")
	T.Flags.BoolVar(&T.input.process_active, "active", "Process both active and inactive accounts. (DANGEROUS)")
	T.Flags.BoolVar(&T.input.process_active, "danger", "")
	T.Flags.BoolVar(&T.input.force, "force", "Override critical safeties on account deletions. (EXTREMELY DANGEROUS)")
	T.Flags.UintVar(&T.input.inactive_days, "inactive_days", 0, "Maximum number in days of inactivity.")
	T.Flags.BoolVar(&T.input.unverified, "unverified", "Delete only users who are unverified.")
	T.Flags.BoolVar(&T.input.suspended, "suspended", "Delete only users who are suspended.")
	T.Flags.StringVar(&T.input.reassign_to, "reassign_to", "<user@domain.com>", "User to reassign folders to.")
	T.Flags.BoolVar(&T.input.retain_perms, "retain_perms", "Retain permissions to shared data.")
	T.Flags.IntVar(&T.input.profile_id, "profile_id", 0, "Target users within specified profile.")
	T.Flags.BoolVar(&T.input.dry_run, "dry_run", "Simulate removal of users without actually deleting accounts.")
	T.Flags.BoolVar(&T.input.remote_wipe, "remote_wipe", "Remote wipe any data on user's mobile device.")
	T.Flags.BoolVar(&T.input.withdraw_links, "withdraw_links", "Withdraw all file links and request file links sent by user.")
	T.Flags.BoolVar(&T.input.external_only, "external_only", "Only remove external accounts")
	T.Flags.IntVar(&T.input.limit, "limit", -1, "Limit number of accounts to remove, -1 for all users.")
	T.Flags.StringVar(&T.input.csv_file, "csv_file", "<Users-Report-1652909468.csv>", "Specify a Users Report CSV File for targeting users for cleanup.")
	// "active" and "force" flags are ordered first to highlight their importance and potential danger,
	// ensuring they appear together and are easily noticed by users.
	T.Flags.Order("active", "force", "inactive_days", "users", "profile_id", "csv_file")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	T.limit = T.input.limit

	if T.input.inactive_days == 0 && !T.input.process_active && !T.input.suspended && !T.input.force {
		return fmt.Errorf("Please specify --inactive_days or --suspended to proceed.")
	}

	if T.input.process_active && IsBlank(T.input.csv_file) && len(T.input.user_emails) == 0 && !T.input.force && T.input.inactive_days == 0 && !T.input.suspended {
		return fmt.Errorf("Please specify --suspended, --inactive_days, --csv_file or specified --users.")
	}
	T.inactivity_time = time.Now().UTC().Add((time.Hour * 24) * -time.Duration(T.input.inactive_days))
	if T.input.inactive_days > 0 {
		T.inactivity_time = time.Now().UTC().Add((time.Hour * 24) * time.Duration(T.input.inactive_days) * -1)
	}

	if !IsBlank(T.input.csv_file) {
		T.read_csv_file = true
	}

	if T.input.inactive_days == 0 && T.input.profile_id == 0 && len(T.input.user_emails) == 0 && !T.input.unverified && !T.input.suspended && T.input.process_active && T.input.force && IsBlank(T.input.csv_file) {
		return fmt.Errorf("Aborting as this would remove ALL users from the system, MUST specify some filtering criteria.")
	}

	return
}

// CheckLastActivityAPI determines if the Kite system has last activity data available.
// It retrieves the current user's information from the Kite system.
// If the user's LastActivityDateTime is not blank, it indicates that last activity data is available,
// and the function returns false. Otherwise, it returns true, indicating that last activity data is not available.
func (T *UserRemoverTask) CheckLastActivityAPI() bool {
	me, err := T.KW.Admin().MyUser()
	if err != nil {
		Warn("Got error trying to find info on system: %v", err)
		return false
	}
	if !IsBlank(me.LastActivityDateTime) {
		return false
	} else {
		return true
	}
}

func (T *UserRemoverTask) Main() (err error) {
	T.user_count = T.Report.Tally("Accounts evaluated")

	if !T.input.dry_run {
		T.user_removed = T.Report.Tally("Accounts removed")
	} else {
		T.prefix = "(DRY-RUN ONLY) "
		T.user_removed = T.Report.Tally("Accounts to remove")
	}

	T.last_activity_available = T.CheckLastActivityAPI()

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

	if !T.read_csv_file && len(T.input.user_emails) == 0 {
		if T.input.profile_id == 0 {
			user_emails, err := T.KW.Admin().GetAllUsers(params)
			if err != nil {
				return err
			}
			T.input.user_emails = append(T.input.user_emails, user_emails[0:]...)
		} else {
			user_emails, err := T.KW.Admin().FindProfileUsers(T.input.profile_id, params)
			if err != nil {
				return err
			}
			T.input.user_emails = append(T.input.user_emails, user_emails[:0]...)
		}
	} else {
		for k := range T.last_activity {
			T.input.user_emails = append(T.input.user_emails, k)
		}
	}

	user_list := T.input.user_emails

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

// ReadCSV parses a CSV file containing user data, specifically extracting
// the last activity time for each user and storing it in the T.last_activity map.
// It handles potential errors during file opening, CSV parsing, and time parsing.
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
			if len(val) > 9 {
				return fmt.Errorf("CSV has fewer columns than expected, aborting.")
			}
			if !strings.Contains(strings.ToLower(val[8]), "last activity") {
				Stdout(val[8])
				return fmt.Errorf("Did not find Last Activity in header, aborting.")
			}
			if !strings.Contains(strings.ToLower(val[7]), "created") {
				return fmt.Errorf("Did not find Created in header, aborting.")
			}
			continue
		}
		if len(val) < 9 {
			return fmt.Errorf("CSV has fewer columns than expected, aborting.")
		}
		ttp := val[8]
		if IsBlank(ttp) {
			ttp = val[7]
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
	if len(T.last_activity) == 0 {
		return fmt.Errorf("Error parsing %s, please check file and try again..", file)
	}
	return nil
}

// CheckLastActivity determines if a user is inactive based on their last activity time.
// It first attempts to retrieve the last activity time from the KiteUser interface.
// If that fails, it checks if the last activity time was pre-loaded from the CSV file.
// If a pre-loaded time exists, it compares it to the inactivity threshold.
func (T UserRemoverTask) CheckLastActivity(input KiteUser) (bool, time.Time, error) {
	last_activity_time, err := input.LastActivity()
	if err != nil {
		if last_activity, ok := T.last_activity[strings.ToLower(input.Email)]; ok {
			last_activity_time = last_activity
		} else {
			if T.last_activity_available {
				last_activity, err = ReadKWTime(input.Created)
				if err != nil {
					return false, last_activity_time, err
				}
				last_activity_time = last_activity
			} else {
				return false, last_activity_time, err
			}
		}
	}

	if T.input.inactive_days == 0 {
		return true, last_activity_time, nil
	}

	if T.inactivity_time.Unix() > last_activity_time.Unix() {
		return true, last_activity_time, nil
	}

	return false, last_activity_time, nil
}

// RemoveUser removes a user based on configured criteria and flags.
// It checks for various conditions such as self-removal prevention,
// external/internal user status, profile ID matching, deactivation status,
// verification status, suspension status, and inactivity based on last activity time.
// If all criteria are met, the user is either dry-run removed or actually deleted
// via the Kite API, potentially reassigning data or withdrawing file links.
func (T UserRemoverTask) RemoveUser(input KiteUser) bool {
	T.user_count.Add(1)

	Log("Inspecting user %s ...", input.Email)

	// Prevent self-removal.
	if input.Email == T.KW.Username {
		Log("[%s]: Cannot remove self, skipping user..", input.Email)
		return false
	}

	// Skip internal users if --external_only is set.
	if T.input.external_only {
		if input.Internal {
			Debug("[%s]: User is not external, skipping user..", input.Email)
			return false
		}
	}

	// Skip users whose profile ID doesn't match the configured ID.
	if T.input.profile_id != 0 {
		if input.UserTypeID != T.input.profile_id {
			Debug("[%s]: Profile ID (%d) does not match required profile id %d, skipping user..", input.Email, input.UserTypeID, T.input.profile_id)
			return false
		}
	}

	// Skip active users unless --active is set.
	if !input.Deactivated {
		if !T.input.process_active {
			Debug("[%s]: User is not marked as deactivated, skipping user.. (must use --active to override)", input.Email)
			return false
		}
	}

	// Skip verified users if --unverified is set.
	if T.input.unverified {
		if input.Verified {
			Debug("[%s]: User is verified, skipping user..", input.Email)
			return false
		}
	}

	// Skip suspended users if --suspended is set.
	if T.input.suspended {
		if !input.Suspended {
			Debug("[%s]: User is not suspended, skipping user..", input.Email)
			return false
		}
	}

	// Check for inactivity based on last activity time.
	chk_inactivity, last_activity, err := T.CheckLastActivity(input)
	if !chk_inactivity {
		if err != nil {
			Err("[%s]: %s", input.Email, err.Error())
		}
		Debug("[%s]: User's last activity (%s) is less than %d days ago, skipping user..", input.Email, last_activity, T.input.inactive_days)
		return false
	}

	Log("%sRemoving user: %s (deactivated:%v/suspended:%v/verified:%v/last activity:%s)", T.prefix, input.Email, input.Deactivated, input.Suspended, input.Verified, strings.TrimSuffix(last_activity.String(), " +0000 UTC"))

	// Dry-run removal.
	if T.input.dry_run {
		T.user_removed.Add(1)
		return true
	}

	// Configure API parameters for deletion.
	params := SetParams(Query{"partialSuccess": false})

	// Reassign data to another user if retain_to is specified.
	if !IsBlank(T.input.reassign_to) {
		params = SetParams(params, Query{"retainToUser": T.reassign_to_id, "retainData": true})
	} else {
		// Otherwise, delete unshared data.
		params = SetParams(params, Query{"retainData": false, "deleteUnsharedData": true})
	}

	// Configure other deletion options.
	params = SetParams(params, Query{"retainPermissionToSharedData": T.input.retain_perms})
	params = SetParams(params, Query{"remoteWipe": T.input.remote_wipe})
	params = SetParams(params, Query{"withdrawFileLinks": T.input.withdraw_links, "withdrawRequestFiles": T.input.withdraw_links})

	// Delete the user via the Kite API.
	err = T.KW.Admin().DeleteUser(input, params)
	if err != nil {
		Err("[%s]: %s", input.Email, err.Error())
		return false
	}

	T.user_removed.Add(1)
	return true
}
