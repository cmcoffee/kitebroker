package FTA

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"strings"
)

// Tests user for this session, to make sure token & account are valid.
func (T Broker) TestKWUser(email string) bool {
	if found := T.cache.Get("kw_user_test", strings.ToLower(email), nil); found {
		return true
	} else {
		err := T.KW.Session(email).Call(APIRequest{
			Method: "GET",
			Path:   "/rest/users/me",
		})
		if err != nil {
			T.cache.Unset("kw_users", email)
			T.DB.Unset("temp_kw_users", email)
			T.DB.Unset("perm_kw_users", email)
			return false
		}
		T.cache.Set("kw_user_test", strings.ToLower(email), 1)
		return true
	}
}

// Main function to look up the kitework user.
func (T Broker) KWUser(email string, permanent, notify bool) (kw_user *KiteUser, err error) {

	// First check if we have this user already in our database.
	if found := T.cache.Get("kw_users", email, &kw_user); found && kw_user != nil {
		if permanent {
			T.DB.Unset("temp_kw_users", kw_user.Email)
			T.DB.Set("perm_kw_users", kw_user.Email, kw_user.ID)
		}
		if kw_user.Verified == false {
			if err := T.activate_kw_user(kw_user); err != nil {
				return kw_user, err
			} else {
				kw_user.Verified = true
				kw_user.Active = true
				kw_user.Suspended = false
				T.cache.Set("kw_users", email, &kw_user)
			}
		}
		T.cache.Set("kw_users", email, kw_user)
		if T.TestKWUser(email) {
			return kw_user, nil
		}
	}

	// No user record found, time to search for user on kiteworks.
	if kw_user, err = T.find_kw_user(email); err == nil {
		if kw_user != nil {
			if err := T.activate_kw_user(kw_user); err != nil {
				return kw_user, err
			}
			// We found the user, if non-permanent add to temp_kw_users.
			if !permanent && !kw_user.Verified {
				if found := T.DB.Get("perm_kw_users", kw_user.Email, nil); !found {
					T.DB.Set("temp_kw_users", kw_user.Email, kw_user.ID)
				}
			} else if permanent { // If permanent flag is set, remove from temp_kw_users which is used for cleanup after.
				T.DB.Set("perm_kw_users", kw_user.Email, kw_user.ID)
				T.DB.Unset("temp_kw_users", kw_user.Email)
			}
			kw_user.Verified = true
			kw_user.Active = true
			kw_user.Suspended = false
			T.cache.Set("kw_users", email, &kw_user)
			if T.TestKWUser(email) {
				return kw_user, nil
			}
		}
	} else if err != nil && err != ErrNotFound {
		return nil, err
	}

	// We have not found the user, it's time to create a user at this point.
	if err := T.KW.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/users",
		Params: SetParams(PostJSON{"email": email, "verified": true, "sendNotification": notify, "active": true}, Query{"returnEntity": true}),
		Output: &kw_user,
	}); err != nil {
		return nil, fmt.Errorf("Error initializing user %s: %s", email, err.Error())
	}

	_, err = T.KW.Session(kw_user.Email).MyUser()
	if err != nil {
		return nil, err
	}

	if permanent {
		T.DB.Set("perm_kw_users", kw_user.Email, kw_user.ID)
	} else {
		T.DB.Set("temp_kw_users", kw_user.Email, kw_user.ID)
	}

	T.cache.Set("kw_users", kw_user.Email, &kw_user)

	return kw_user, nil
}

// Search for specific kiteworks user.
func (T Broker) find_kw_user(user_email string) (kw_user *KiteUser, err error) {

	if found := T.cache.Get("kw_users", user_email, &kw_user); found && kw_user != nil {
		return kw_user, nil
	}

	var users struct {
		Data []KiteUser `json:"data"`
	}

	req := APIRequest{
		Method: "GET",
		Path:   "/rest/users",
		Params: SetParams(Query{"email": user_email, "deleted": false}),
		Output: &users,
	}

	if err := T.KW.Call(req); err != nil {
		return nil, err
	}

	if len(users.Data) == 0 {
		return nil, ErrNotFound
	}

	T.cache.Set("kw_users", user_email, &users.Data[0])

	return &users.Data[0], nil
}

// Clean Temp KW Users
func (T Broker) RemoveTempUsers() {
	var user_id int
	keys := T.DB.Keys("temp_kw_users")
	for _, login := range keys {
		T.DB.Get("temp_kw_users", login, &user_id)
		err := T.KW.Call(APIRequest{
			Method: "DELETE",
			Path:   SetPath("/rest/admin/users/%d", user_id),
			Params: SetParams(Query{"retainData": false, "retainPermissionToSharedData": false, "deleteUnsharedData": true}),
		})
		if err != nil {
			Err("Failure cleaning up temporary kiteworks user %s: %s", login, err.Error())
		} else {
			T.DB.Unset("temp_kw_users", login)
		}
	}
}

// Activate the kiteworks user.
func (T Broker) activate_kw_user(kw_user *KiteUser) (err error) {
	if !kw_user.Verified || !kw_user.Active || kw_user.Deactivated || kw_user.Suspended {
		req := APIRequest{
			Method: "PUT",
			Path:   SetPath("/rest/admin/users/%d", kw_user.ID),
			Params: SetParams(PostJSON{"suspended": false, "verified": true}),
		}
		return T.KW.Call(req)
	}

	return nil
}
