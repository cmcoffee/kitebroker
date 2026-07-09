package kiteworks

import (
	"fmt"
	"strings"

	. "github.com/cmcoffee/kitebroker/core"
)

// CopyOptions controls copy behavior. Used by both the kiteworks migration task
// (built from its flags in Init()) and external callers (built directly via
// NewCopier).
type CopyOptions struct {
	NoFiles           bool
	NoMail            bool
	NoSshKeys         bool
	Cleanup           bool
	DeactivateSrcUser bool
	DeleteUserFirst   bool
	SrcDomain         string
	NewDomain         string
	DstProfileName    string
	SrcProfileName    string
	UserEmails        []string
	CloneProfiles     bool
	Observer          Observer
	// DstFolderResolver, when set, maps a source folder id to its known
	// destination folder id (from persisted sync state). It lets folder cloning
	// target the exact destination folder by id instead of resolving by path —
	// which is ambiguous when a user has distinct folders with the same name
	// (e.g. an owned folder and a shared-in folder). Returns ("", false) when no
	// mapping exists, in which case the copier falls back to path resolution.
	DstFolderResolver func(src_folder_id string) (dst_folder_id string, ok bool)
}

// Observer receives notifications as objects are mirrored from source to
// destination. Hooks are invoked from worker goroutines, so implementations
// must be safe for concurrent use. A nil Observer in CopyOptions is fine —
// no hooks fire.
type Observer interface {
	OnUserMapped(src KiteUser, dst KiteUser, dst_profile_id int)
	OnFolderCloned(src KiteObject, dst KiteObject, owner_email string)
	OnFileUploaded(src KiteObject, dst KiteObject, parent_src_id string, owner_email string)
	OnPermissionGranted(src_folder_id string, member_email string, role_id int, owner_email string)
	OnSshKeyCopied(owner_email string, src KiteSshPublicKey, dst KiteSshPublicKey)
}

// NewCopier returns a KW_TO_KWTask ready to drive a copy. The parent task's
// DB, KW session, and Report are reused; src is the pre-configured source API
// client; src_admin is the admin email used for admin-scoped lookups against
// source. Tallies are registered on the parent's Report.
func NewCopier(parent *KiteBrokerTask, src KWAPI, src_admin string, opts CopyOptions) *KW_TO_KWTask {
	t := &KW_TO_KWTask{
		KiteBrokerTask: *parent,
		SRC:            src,
		src_admin:      src_admin,
		opts:           opts,
		failed_users:   make(map[string]any),
		users:          make(map[string]struct{}),
		limiter:        NewLimitGroup(50),
	}
	t.users_count = parent.Report.Tally("Synced Users")
	t.FailedUsers = parent.Report.Tally("Failed Users")
	t.folders_count = parent.Report.Tally("Synced Folders")
	t.files_count = parent.Report.Tally("Synced Files")
	t.files_copied = parent.Report.Tally("Files Transferred")
	t.mail_count = parent.Report.Tally("Mail Archived")
	t.ssh_keys_count = parent.Report.Tally("SSH Keys Copied")
	t.transfer_counter = parent.Report.Tally("Data Transferred", HumanSize)
	return t
}

// getSourceUsers resolves users from the source server according to T.opts
// filters (UserEmails, SrcProfileName, SrcDomain).
func (T *KW_TO_KWTask) getSourceUsers() ([]KiteUser, error) {
	var src_profile_id int
	if !IsBlank(T.opts.SrcProfileName) {
		src_profile, err := T.SRC.Session(T.src_admin).Admin().FindProfile(T.opts.SrcProfileName)
		if err != nil {
			return nil, fmt.Errorf("Source profile resolution failed: %v", err)
		}
		src_profile_id = src_profile.ID
	}

	params := SetParams(Query{"active": true, "deleted": false, "suspended": false, "verified": true})
	if !IsBlank(T.opts.SrcDomain) {
		params = SetParams(params, Query{"email:contains": T.opts.SrcDomain})
	}

	user_getter, err := T.SRC.Session(T.src_admin).Admin().Users(T.opts.UserEmails, src_profile_id, params)
	if err != nil {
		return nil, err
	}

	var all_users []KiteUser
	for {
		users, err := user_getter.Next()
		if err != nil {
			return nil, err
		}
		if len(users) == 0 {
			break
		}
		all_users = append(all_users, users...)
	}
	return all_users, nil
}

// RunCopy resolves users from source and walks them through the copy phases:
// create-or-verify on destination, archive mail (if enabled), walk folder
// tree. Configuration comes entirely from T.opts.
func (T *KW_TO_KWTask) RunCopy() (err error) {
	if !IsBlank(T.opts.NewDomain) && IsBlank(T.opts.SrcDomain) {
		return fmt.Errorf("if you specify a new_domain, you must specify a src_domain")
	}
	if !IsBlank(T.opts.NewDomain) {
		Debug("Old Domain: %s, New Domain: %s", T.opts.SrcDomain, T.opts.NewDomain)
	}

	all_users, err := T.getSourceUsers()
	if err != nil {
		return err
	}

	// Clone custom profiles onto the destination before mapping/creating users,
	// so freshly-synced profiles are available for name-based mapping and no
	// user falls through to a missing profile.
	if T.opts.CloneProfiles && !T.opts.Cleanup {
		if err := T.CloneProfiles(); err != nil {
			Err("Profile clone step failed: %v", err)
		}
	}

	if IsBlank(T.opts.DstProfileName) || strings.EqualFold(T.opts.DstProfileName, "auto") {
		if err := T.MapProfiles(); err != nil {
			return err
		}
	} else {
		dst_profile, err := T.KW.Admin().FindProfile(T.opts.DstProfileName)
		if err != nil {
			return fmt.Errorf("Destination profile resolution failed: %v", err)
		}
		if dst_profile.Features.FolderCreate == 0 {
			return fmt.Errorf("Destination profile does not have permission to create folders")
		}
		T.dst_profile_id = dst_profile.ID
	}

	for _, u := range all_users {
		T.users[u.Email] = struct{}{}
	}

	wg := NewLimitGroup(25)

	if !T.opts.Cleanup {
		Log("\n=== Creating/Verifying users on Kiteworks. ===\n\n")
		Log("- Found %d valid source users.\n\n", len(all_users))
		for _, u := range all_users {
			wg.Add(1)
			go func(user KiteUser) {
				defer wg.Done()
				T.createDestUser(user)
			}(u)
		}
		wg.Wait()
	}

	if !T.opts.NoMail && !T.opts.Cleanup {
		Log("\n=== Archiving user mail. ===\n\n")
		for _, u := range all_users {
			if T.ignoreUser(u.Email) {
				continue
			}
			wg.Add(1)
			go func(user KiteUser) {
				defer wg.Done()
				if err := T.ArchiveUserMail(user); err != nil {
					Err("[%s]: Mail archive error: %v", user.Email, err)
				}
			}(u)
		}
		wg.Wait()
	}

	Log("\n=== Users created/verified. Starting folder sync. ===\n\n")
	for _, u := range all_users {
		if T.ignoreUser(u.Email) {
			continue
		}
		wg.Add(1)
		go func(user KiteUser) {
			defer wg.Done()
			if err := T.CopyUser(user); err != nil {
				Err("%s: %v", user.Email, err)
			}
		}(u)
	}
	wg.Wait()
	Log("\n=== Copy Complete ===")
	return
}

// isManualProfileOverride reports whether the source user's profile was set
// manually by an admin (as opposed to being auto-assigned by the source's
// login/LDAP/domain mapping rules). Only manual overrides are re-applied on the
// destination; auto-mapped users are left for the destination appliance to map.
//
// TODO: implement via the source's GET /rest/admin/profiles/mappings?user=<email>
// (ProfileMappingsTest) — compare the rule-assigned profile to user.UserTypeID;
// they differ only when an admin manually overrode the profile. Until that API
// is wired in, we treat every user as auto-mapped (return false), which is the
// safe default: we never wrongly pin a profile and defeat destination mapping.
func (T *KW_TO_KWTask) isManualProfileOverride(user KiteUser) bool {
	return false
}

// createDestUser ensures a destination user exists, is verified and
// unsuspended, and is on the right profile. Fires OnUserMapped when a
// destination user has been confirmed for a source user.
func (T *KW_TO_KWTask) createDestUser(user KiteUser) {
	username := T.SwapEmails(user.Email)

	if T.opts.DeleteUserFirst {
		Debug("Deleting %s", username)
		dst_user, err := T.KW.Admin().FindUser(username)
		if err != nil {
			Err("[%s]: Error finding user for deletion: %v (skipping)", username, err)
			T.setIgnoreUser(user.Email)
			return
		}
		if username != dst_user.Email {
			T.setIgnoreUser(user.Email)
			return
		}
		params := SetParams(Query{"retainData": false, "deleteUnsharedData": true})
		if err := T.KW.Admin().DeleteUser(*dst_user, params); err != nil {
			Err("[%s]: Error deleting user: %v (skipping)", username, err)
			T.setIgnoreUser(user.Email)
			return
		}
	}

	// The destination profile that corresponds to the source user's profile.
	// This is only *applied* when the source assignment is a manual override;
	// otherwise the user is created without a profile so the destination
	// appliance auto-maps them via its own login/LDAP/domain rules. Passing a
	// userTypeId to NewUser/UpdateUserProfile pins it as a manual override, which
	// is exactly what we must NOT do for auto-mapped users.
	mapped_profile_id := T.FindDestProfileID(user.UserTypeID)

	// pin_profile_id is non-zero only when the source user's profile was manually
	// overridden (differs from what source mapping rules would assign) — those,
	// and only those, are pinned on the destination.
	pin_profile_id := 0
	if mapped_profile_id > 0 && T.isManualProfileOverride(user) {
		pin_profile_id = mapped_profile_id
	}

	kw_user, err := T.KW.Admin().FindUser(username, true)
	if err != nil && err != ERR_NO_USER_FOUND {
		Err("Error finding user %s: %v", username, err)
		T.setIgnoreUser(user.Email)
		return
	}
	if kw_user == nil {
		Log("[%s]: Creating user on Kiteworks..", username)
		// pin_profile_id == 0 => omit userTypeId => appliance auto-maps.
		if kw_user, err = T.KW.Admin().NewUser(username, pin_profile_id, true, false); err != nil {
			if !IsAPIError(err, "ERR_ENTITY_EXISTS") {
				Err("[%s]: Failed to create user: %v (skipping)", username, err)
				T.setIgnoreUser(user.Email)
				return
			}
		}
	} else {
		Log("[%s]: User already exists on Kiteworks.", username)
	}
	if kw_user != nil && (kw_user.Suspended || !kw_user.Verified) {
		if kw_user.Suspended {
			Log("[%s]: User suspended on Kiteworks, reactivating.", username)
		}
		if !kw_user.Verified {
			Log("[%s]: User unverified on Kiteworks source, verifying.", username)
		}
		if err := T.KW.Admin().UpdateUser(kw_user.ID, SetParams(PostJSON{"suspended": false, "verified": true})); err != nil {
			Err("Error updating user %s: %v (skipping)", username, err)
			T.setIgnoreUser(user.Email)
			return
		}
	}

	// Apply a manual profile override, if any — for both newly created and
	// existing users (existing active users were previously never corrected).
	if pin_profile_id > 0 && kw_user != nil {
		if err := T.KW.Admin().UpdateUserProfile(pin_profile_id, []string{kw_user.ID}); err != nil {
			Err("Error setting profile override for %s: %v", username, err)
		}
	}

	if kw_user != nil {
		// Record the profile the user actually ended up on: the pinned override
		// when set, otherwise whatever the appliance auto-mapped (kw_user's type).
		observed_profile := kw_user.UserTypeID
		if pin_profile_id > 0 {
			observed_profile = pin_profile_id
		}
		T.notifyUserMapped(user, *kw_user, observed_profile)
	}
}

func (T *KW_TO_KWTask) notifyUserMapped(src, dst KiteUser, dst_profile_id int) {
	if T.opts.Observer != nil {
		T.opts.Observer.OnUserMapped(src, dst, dst_profile_id)
	}
}

func (T *KW_TO_KWTask) notifyFolderCloned(src, dst KiteObject, owner_email string) {
	if T.opts.Observer != nil {
		T.opts.Observer.OnFolderCloned(src, dst, owner_email)
	}
}

func (T *KW_TO_KWTask) notifyFileUploaded(src, dst KiteObject, parent_src_id, owner_email string) {
	if T.opts.Observer != nil {
		T.opts.Observer.OnFileUploaded(src, dst, parent_src_id, owner_email)
	}
}

func (T *KW_TO_KWTask) notifyPermissionGranted(src_folder_id, member_email string, role_id int, owner_email string) {
	if T.opts.Observer != nil {
		T.opts.Observer.OnPermissionGranted(src_folder_id, member_email, role_id, owner_email)
	}
}

func (T *KW_TO_KWTask) notifySshKeyCopied(owner_email string, src, dst KiteSshPublicKey) {
	if T.opts.Observer != nil {
		T.opts.Observer.OnSshKeyCopied(owner_email, src, dst)
	}
}
