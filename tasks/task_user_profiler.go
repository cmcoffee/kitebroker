package tasks

import (
	"fmt"
	. "github.com/cmcoffee/go-kwlib"
	. "github.com/cmcoffee/kitebroker/common"
	"strings"
	"sync"
	"time"
)

var passport *Passport

type UserProfilerTask struct {
	cut_off_days   int
	new_profile_id int
	old_profile_id int
	dli_email      string
	dli_admin      *Session
	user_emails    string
	filter         string
	unverified     bool
	deactivated    bool
}

func (T *UserProfilerTask) New() Task {
	return new(UserProfilerTask)
}

// Init function.
func (T *UserProfilerTask) Init(flag *FlagSet) (err error) {
	flag.StringVar(&T.dli_email, "dli_admin", "<dliadmin@domain.com>", "Admin used for activities lookup.")
	flag.IntVar(&T.cut_off_days, "older_than", 90, "Number of days since last activity.")
	flag.IntVar(&T.new_profile_id, "new_profile_id", 0, "Profile ID for users to be migrated to.")
	flag.IntVar(&T.old_profile_id, "old_profile_id", 0, "Profile ID of users to match against.")
	flag.StringVar(&T.user_emails, "users", "<email@domain.com>", "Specific users to check, multiple entries seperated by comma.")
	flag.BoolVar(&T.deactivated, "deactivated", false, "Filter out accounts that are deactivated.")
	flag.BoolVar(&T.unverified, "unverified", false, "Filter out accounts that are unverfied.")
	flag.StringVar(&T.filter, "domain_filter", "<domain.com>", "Filter out emails from email domain.")

	if err = flag.Parse(); err != nil {
		return err
	}

	if T.new_profile_id == 0 || T.old_profile_id == 0 {
		return fmt.Errorf("--new_profile_id and --old_profile_id are required.")
	}

	T.filter = strings.TrimPrefix(T.filter, "@")
	T.filter = fmt.Sprintf("@%s", T.filter)

	return nil
}

// Main function
func (T *UserProfilerTask) Main(pass *Passport) (err error) {

	passport = pass

	if T.dli_email == NONE {
		T.dli_email = passport.User.Username
		T.dli_admin = &passport.User
	}

	if T.dli_admin == nil {
		T.dli_admin.KWSession, err = passport.User.KWAPI.SigAuth(T.dli_email)
		if err != nil {
			return fmt.Errorf("DLI Admin Error - (%s): %s", T.dli_email, err.Error())
		}
	}
	day := time.Duration(time.Hour * 24)
	date := time.Now().Add((day * time.Duration(T.cut_off_days)) * -1)
	date = date.UTC()

	params := SetParams(Query{"active": true, "deleted": false, "email:contains": T.filter})
	if T.unverified {
		params = SetParams(params, Query{"verified": false})
	}

	var user_count int
	var users []KiteUser

	if T.user_emails == NONE {
		user_count, err = passport.User.GetUserCount(params)
		if err != nil {
			return err
		}
	} else {
		for _, email := range strings.Split(T.user_emails, ",") {
			user_getter := passport.User.GetUsers(params, Query{"email": strings.ToLower(email)})
			u, err := user_getter.Next()
			if err != nil {
				Err(err)
				continue
			}
			if len(u) == 0 {
				Log("[%s]: User not found matching critera or does not meet critera.", email)
				continue
			}
			users = append(users, u[0:]...)
			user_count = user_count + len(u)
		}
	}

	ProgressBar.New("users", user_count)

	var wg sync.WaitGroup
	limiter := make(chan struct{}, 50)

	var tested bool

	user_getter := passport.User.GetUsers(params, Query{"email:contains": T.filter})

	for {
		if T.user_emails == NONE {
			users, err = user_getter.Next()
			if err != nil {
				return err
			}
		}
		if len(users) == 0 {
			break
		}
		if !tested {
			test_date := DateString(time.Now().UTC())
			if _, err := T.lastActivity(users[0].ID, Query{"startDate": test_date, "endDate": test_date}); err != nil {
				return fmt.Errorf("DLI Admin Error - (%s): %s", T.dli_email, err.Error())
			}
			tested = true
		}
		for _, user := range users {
			limiter <- struct{}{}
			wg.Add(1)
			go func(user KiteUser) {
				defer func() { <-limiter }()
				defer wg.Done()
				defer ProgressBar.Add(1)
				if T.deactivated && user.Deactivated == false {
					return
				}
				if user.UserTypeID != T.old_profile_id {
					return
				}
				last_active_time, err := T.lastActivity(user.ID, Query{"startDate": DateString(date.UTC()), "endDate": DateString(time.Now().UTC())})
				if err != nil {
					Err(err)
					return
				}
				if last_active_time.Unix() < date.Unix() {
					if err := T.change_profile(user.ID); err != nil {
						Err("%s: %s", user.Email, err.Error())
					} else {
						Log("%s: profile updated to profile id %d.", user.Email, T.new_profile_id)
					}
				} else {
					if T.user_emails != NONE {
						Log("%s: Last active time is newer than cut-off date: %v", user.Email, last_active_time.In(time.Local))
					}
				}
			}(user)
		}
		users = users[0:0]
	}

	wg.Wait()
	return err
}

// Changes the profile.
func (T *UserProfilerTask) change_profile(user_id int) (err error) {
	return passport.User.Call(APIRequest{
		Method: "PUT",
		Path:   SetPath("/rest/admin/profiles/%d/users", T.new_profile_id),
		Params: SetParams(Query{"id:in": user_id}),
		Output: nil,
	})
}

// Grab last activity.
func (T *UserProfilerTask) lastActivity(user_id int, params ...interface{}) (last_activity time.Time, err error) {
	var activities []struct {
		Created string `json:"created"`
	}
	err = T.dli_admin.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/dli/users/%d/activities", user_id),
		Params: SetParams(params),
		Output: &activities,
	}, -1, 1000)
	for _, k := range activities {
		la, err := ReadKWTime(k.Created)
		if err != nil {
			Err(err)
			err = nil
			continue
		}
		if la.Unix() > last_activity.Unix() {
			last_activity = la
		}
	}
	return
}
