package admin

import (
	"fmt"
	. "kitebroker/core"
	"strings"
	"sync"
)

type UserProfilerTask struct {
	KiteBrokerTask
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
}

func (T *UserProfilerTask) New() Task {
	return new(UserProfilerTask)
}

func (T UserProfilerTask) Name() string {
	return "user_reprofiler"
}

func (T UserProfilerTask) Desc() string {
	return "Change user profiles."
}

// Init function.
func (T *UserProfilerTask) Init() (err error) {
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

	if T.old_profile_id == 0 && !T.deactivated && !T.unverified && T.filter == NONE && len(T.user_emails) == 0 {
		return fmt.Errorf("You must provide some type of user filter: --deactivated, --unverified, --old_profile_id, --users or --domain_filter.")
	}

	if T.new_profile_id == 0 {
		return fmt.Errorf("You must provide a new profile id to assign users to: --new_profile_id")
	}

	T.filter = strings.TrimPrefix(T.filter, "@")
	T.filter = fmt.Sprintf("@%s", T.filter)

	return nil
}

// Main function
func (T *UserProfilerTask) Main() (err error) {
	T.user_count = T.Report.Tally("Analyzed Users")
	T.user_changed = T.Report.Tally("Modified Users")

	if !IsBlank(T.reassign_to) {
		reassign_to_sess := T.KW.Session(T.reassign_to)
		r, err := reassign_to_sess.MyUser()
		if err != nil {
			return err
		}
		T.reassign_to_id = r.ID
	}

	params := SetParams(Query{"active": true, "deleted": false, "email:contains": T.filter})
	if T.unverified {
		params = SetParams(params, Query{"verified": false})
	}

	var user_count int
	var users []KiteUser

	user_count, err = T.KW.Admin().UserCount(T.user_emails, params)
	if err != nil {
		return err
	}

	T.user_count.Add(user_count)
	pb := ProgressBar("Users Processed", user_count)
	defer pb.Done()

	var wg sync.WaitGroup
	limiter := make(chan struct{}, 100)

	user_getter, err := T.KW.Admin().Users(T.user_emails, 0, params, Query{"email:contains": T.filter})
	if err != nil {
		return err
	}

	for {
		users, err = user_getter.Next()
		if err != nil {
			return err
		}
		if len(users) == 0 {
			break
		}
		for _, user := range users {
			limiter <- struct{}{}
			wg.Add(1)
			go func(user KiteUser) {
				defer func() { <-limiter }()
				defer wg.Done()
				defer pb.Add(1)
				if T.deactivated && user.Deactivated == false {
					return
				}
				if T.old_profile_id != 0 && user.UserTypeID != T.old_profile_id {
					return
				}

				if err := T.change_profile(user.ID); err != nil {
					Err("%s: %s", user.Email, err.Error())
				} else {
					T.user_changed.Add(1)
					Log("%s: profile updated to profile id %d.", user.Email, T.new_profile_id)
				}
			}(user)
		}
		users = users[0:0]
	}

	wg.Wait()
	return err
}

// Changes the profile.
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
