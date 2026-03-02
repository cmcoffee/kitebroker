package admin

import (
	"encoding/csv"
	"fmt"
	"os"
	"strings"
	"sync"

	. "github.com/cmcoffee/kitebroker/core"
)

func init() { RegisterAdminTask(new(UserProfilerTask)) }

type UserProfilerTask struct {
	KiteBrokerTask
	csv_file       string
	new_profile_id int
	old_profile_id int
	user_emails    []string
	filter         string
	unverified     bool
	deactivated    bool
	user_changed   Tally
	user_count     Tally
	reassign_to    string
	reassign_to_id string
	worker_chan    chan KiteUser
}

func (T UserProfilerTask) Name() string {
	return "user_reprofiler"
}

func (T UserProfilerTask) Desc() string {
	return "Users:Change user profiles."
}

// Init function.
func (T *UserProfilerTask) Init() (err error) {
	T.Flags.StringVar(&T.csv_file, "csv_file", "<Users-Report-1652909468.csv>", "Specify a Users Report CSV File for targeting users for reprofiling.")
	T.Flags.IntVar(&T.new_profile_id, "new_profile_id", 0, "Profile ID for users to be migrated to.")
	T.Flags.IntVar(&T.old_profile_id, "old_profile_id", 0, "Profile ID of users to match against.")
	T.Flags.MultiVar(&T.user_emails, "users", "<user_account@domain.com>", "Specific users to check.")
	T.Flags.StringVar(&T.reassign_to_id, "reassign_to", "<user_account@domain.com>", "User to reassign data to.")
	T.Flags.BoolVar(&T.deactivated, "deactivated", "Apply only to users that are deactivated.")
	T.Flags.BoolVar(&T.unverified, "unverified", "Apply only to users that are unverfied.")
	T.Flags.StringVar(&T.filter, "domain_filter", "<domain.com>", "Filter out emails from email domain.")
	T.Flags.Order("new_profile_id", "old_profile_id", "deactivated", "unverified", "domain_filter", "users")
	T.Flags.InlineArgs("users")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	if T.old_profile_id == 0 && !T.deactivated && !T.unverified && T.filter == NONE && len(T.user_emails) == 0 && IsBlank(T.csv_file) {
		return fmt.Errorf("You must provide some type of user filter: --csv_file, --deactivated, --unverified, --old_profile_id, --users or --domain_filter.")
	}

	if T.new_profile_id == 0 {
		return fmt.Errorf("You must provide a new profile id to assign users to: --new_profile_id")
	}

	return nil
}

// ReadCSV parses a CSV file containing user data, specifically extracting
// the last activity time for each user and storing it in the T.last_activity map.
// It handles potential errors during file opening, CSV parsing, and time parsing.
func (T *UserProfilerTask) ReadCSV(file string) (err error) {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return err
	}

	for _, val := range records {
		email := strings.ToLower(val[0])
		email = strings.TrimSuffix(email, " (sso)")
		email = strings.TrimSuffix(email, " (ldap)")
		T.user_emails = append(T.user_emails, email)
	}
	return nil
}

// Main function
func (T *UserProfilerTask) Main() (err error) {
	T.user_count = T.Report.Tally("Analyzed Users")
	T.user_changed = T.Report.Tally("Modified Users")
	T.worker_chan = make(chan KiteUser)

	if !IsBlank(T.csv_file) {
		if err := T.ReadCSV(T.csv_file); err != nil {
			return err
		}

	}

	if !IsBlank(T.reassign_to) {
		reassign_to_sess := T.KW.Session(T.reassign_to)
		r, err := reassign_to_sess.MyUser()
		if err != nil {
			return err
		}
		T.reassign_to_id = r.ID
	}

	var q Query
	if !IsBlank(T.filter) {
		T.filter = strings.TrimPrefix(T.filter, "@")
		T.filter = fmt.Sprintf("@%s", T.filter)
		q = Query{"email:contains": T.filter}
	}

	params := SetParams(Query{"active": true, "deleted": false}, q)
	if T.unverified {
		params = SetParams(params, Query{"verified": false})
	}

	var user_count int
	var users []KiteUser

	if len(T.user_emails) == 0 {
		user_count, err = T.KW.Admin().UserCount(T.user_emails, params)
		if err != nil {
			return err
		}
	} else {
		user_count = len(T.user_emails)
	}

	T.user_count.Add(user_count)
	pb := ProgressBar("Users Processed", user_count)
	defer pb.Done()

	var wg sync.WaitGroup
	//limiter := make(chan struct{}, 100)

	user_getter, err := T.KW.Admin().Users(T.user_emails, 0, params)
	if err != nil {
		return err
	}

	wg.Add(1)
	go func() {
		change_user_profiles := func(users []KiteUser) {
			params := make(PostJSON)

			var user_ids []string
			for _, u := range users {
				user_ids = append(user_ids, u.ID)
			}

			if T.reassign_to_id != "" {
				params["retainToUser"] = T.reassign_to_id
				params["retainData"] = true
			} else {
				params["retainData"] = false
				params["deleteUnsharedData"] = true
			}

			err = T.KW.Call(APIRequest{
				Method: "PUT",
				Path:   SetPath("/rest/admin/profiles/%d/users", T.new_profile_id),
				Params: SetParams(Query{"id:in": strings.Join(user_ids, ",")}, params),
				Output: nil,
			})
			pb.Add(len(user_ids))
			T.user_changed.Add(len(user_ids))
			for _, u := range users {
				Log("%s: profile updated to profile id %d.", u.Email, T.new_profile_id)
			}
			if err != nil {
				Err(err.Error())
			}
		}

		defer wg.Done()
		var users []KiteUser
		for {
			user := <-T.worker_chan
			if user.Email == "::STOP::" {
				if len(users) > 0 {
					change_user_profiles(users)
				}
				break
			} else {
				users = append(users, user)
				if len(users) > 100 {
					change_user_profiles(users)
					users = users[0:0]
				}
			}
		}
	}()

	for {
		users, err = user_getter.Next()
		if err != nil {
			return err
		}
		if len(users) == 0 {
			break
		}
		for _, user := range users {
			//limiter <- struct{}{}
			//wg.Add(1)
			//go func(user KiteUser) d{
			//	defer func() { <-limiter }()
			//	defer wg.Done()
			//	defer pb.Add(1)
			if T.deactivated && user.Deactivated == false {
				pb.Add(1)
				return
			}
			if T.old_profile_id != 0 && user.UserTypeID != T.old_profile_id {
				pb.Add(1)
				return
			}

			T.worker_chan <- user
		}
		users = users[0:0]
	}
	T.worker_chan <- KiteUser{Email: "::STOP::"}
	wg.Wait()
	return err
}

/*
if err := T.change_profile(user.ID); err != nil {
Err("%s: %s", user.Email, err.Error())
} else {
T.user_changed.Add(1)
Log("%s: profile updated to profile id %d.", user.Email, T.new_profile_id)
}

*/

// change_profile changes the profile of a user.
// It takes the user ID as input and updates the user's profile to the new profile ID specified in the UserProfilerTask.
// It also handles data retention and deletion based on the reassign_to_id field.
func (T *UserProfilerTask) change_profile(user_id string) (err error) {
	params := make(PostJSON)

	if T.reassign_to_id != "" {
		params["retainToUser"] = T.reassign_to_id
		params["retainData"] = true
	} else {
		params["retainData"] = false
		params["deleteUnsharedData"] = true
	}

	return T.KW.Call(APIRequest{
		Method: "PUT",
		Path:   SetPath("/rest/admin/profiles/%d/users", T.new_profile_id),
		Params: SetParams(Query{"id:in": user_id}, params),
		Output: nil,
	})
}
