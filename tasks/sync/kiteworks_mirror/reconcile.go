package mirror

import (
	"fmt"
	"strings"
	"time"

	. "github.com/cmcoffee/kitebroker/core"
)

// This file implements the incremental ("differential") reconcile pass for the
// mirror. The full additive copy only ever ADDS to the destination; it never
// removes anything. Between full scans the standby therefore drifts as objects
// are deleted and folder memberships change on the source. Reconcile closes
// that gap by reading the source admin activity log since a persisted cursor
// and applying deletions and membership removals to the destination.
//
// FAIL-SAFE BY DESIGN: the activity log is only used to NOMINATE candidate
// object ids. Every action is then re-verified against the live source API and
// reconciled from SyncState ground truth. A wrong or missing event name (see
// the seed maps below) degrades to a no-op — never a bad delete. The full-sync
// path remains the always-correct backstop.

// deleteEventNames and membershipEventNames are best-guess Kiteworks admin
// activity event names. They only NOMINATE candidate object ids for
// verification — a wrong or missing name never causes a bad delete, it only
// causes a candidate to be missed (which the periodic/log-gap full sync then
// catches). Confirm these against the target appliance's activity log and edit
// freely; the reconcile logic does not otherwise depend on them.
var deleteEventNames = map[string]struct{}{
	"delete_file":           {}, // confirmed: file deleted
	"delete_folder":         {}, // confirmed: folder deleted
	"remove_file":           {},
	"remove_folder":         {},
	"trash":                 {},
	"trash_file":            {},
	"trash_folder":          {},
	"permanently_delete":    {},
	"delete_file_permanent": {},
	"move_to_trash":         {},
	"delete":                {},
}

// Confirmed on-appliance membership event names: add_permission,
// delete_permission, modify_permission. The remaining entries are retained as
// harmless fallbacks in case other appliance versions differ.
var membershipEventNames = map[string]struct{}{
	"add_permission":           {}, // confirmed: member added to folder
	"delete_permission":        {}, // confirmed: member removed from folder
	"modify_permission":        {}, // confirmed: member role changed
	"remove_permission":        {},
	"update_permission":        {},
	"change_permission":        {},
	"add_member":               {},
	"remove_member":            {},
	"add_members":              {},
	"remove_members":           {},
	"change_role":              {},
	"update_member":            {},
	"add_collaborator":         {},
	"remove_collaborator":      {},
	"folder_membership_change": {},
}

// userEventNames nominate system-level user lifecycle changes (create, delete,
// suspend, activate, deactivate). Same fail-safe contract as the maps above:
// they only mark a user email for verification against the live source; the
// mirror then reflects whatever state the source actually reports.
var userEventNames = map[string]struct{}{
	"add_user":         {},
	"create_user":      {},
	"new_user":         {},
	"user_created":     {},
	"delete_user":      {},
	"remove_user":      {},
	"user_deleted":     {},
	"suspend_user":     {},
	"user_suspended":   {},
	"deactivate_user":  {},
	"user_deactivated": {},
	"activate_user":    {},
	"user_activated":   {},
	"reactivate_user":  {},
}

// sshKeyEventNames nominate a user's SFTP/SSH public-key changes. A key add is
// picked up by re-copying the user; a key removal is reconciled by diffing the
// source user's current keys against our recorded mappings.
var sshKeyEventNames = map[string]struct{}{
	"add_ssh_key":           {},
	"create_ssh_key":        {},
	"ssh_key_created":       {},
	"delete_ssh_key":        {},
	"remove_ssh_key":        {},
	"ssh_key_deleted":       {},
	"add_ssh_public_key":    {},
	"delete_ssh_public_key": {},
	"sftp_key_change":       {},
}

// contentEventNames nominate a change to a folder's contents (a file uploaded or
// a folder created). These drive the folder-scoped differential copy: only the
// folder named by such an event is re-processed, so unchanged folders are never
// re-walked. A newly created sub-folder is picked up because its creation is
// itself one of these events. Missing/unknown names degrade gracefully — the
// periodic full sync remains the backstop.
var contentEventNames = map[string]struct{}{
	"add_file":           {}, // confirmed: file uploaded
	"update_file":        {}, // confirmed: file updated (new version/metadata)
	"move_file":          {}, // confirmed: file moved into folder
	"add_file_version":   {},
	"upload":             {},
	"ec_upload_file":     {},
	"upload_to_tray":     {},
	"filehash_generated": {},
	"add_folder":         {},
	"create_folder":      {},
	"folder_created":     {},
	"move_folder":        {},
	"copy_file":          {},
	"rename":             {},
	"rename_file":        {},
	"rename_folder":      {},
}

const (
	// reconcileCursorKey holds the RFC3339 timestamp (UTC) of the last activity
	// processed by a successful reconcile pass.
	reconcileCursorKey = "reconcile_cursor"
	// lastSuccessfulRunKey holds the RFC3339 timestamp (UTC) of the last run
	// that completed without error (either path).
	lastSuccessfulRunKey = "last_successful_run"
	// lastFullSyncKey holds the RFC3339 timestamp (UTC) of the last completed
	// full sync (copy + full-scan reconcile).
	lastFullSyncKey = "last_full_sync"
)

// getCursor returns the saved reconcile cursor and whether one exists.
func (T *KW_MirrorTask) getCursor() (time.Time, bool) {
	saved := T.src_kw_config.GetString(reconcileCursorKey)
	if IsBlank(saved) {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, saved)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// setCursor persists the reconcile cursor (UTC).
func (T *KW_MirrorTask) setCursor(t time.Time) {
	T.src_kw_config.Set(reconcileCursorKey, t.UTC().Format(time.RFC3339))
}

func (T *KW_MirrorTask) getTimeKey(key string) (time.Time, bool) {
	saved := T.src_kw_config.GetString(key)
	if IsBlank(saved) {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, saved)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func (T *KW_MirrorTask) setTimeKey(key string, t time.Time) {
	T.src_kw_config.Set(key, t.UTC().Format(time.RFC3339))
}

// differentialPossible reports whether the source activity log still covers the
// window since our saved cursor. If the oldest activity currently available on
// the source is newer than the cursor, retention has purged part of our window
// and a differential pass would silently miss events — so a full sync is
// required. Any error querying the log is treated as "not possible" (fall back
// to full) to stay safe.
func (T *KW_MirrorTask) differentialPossible(cursor time.Time) bool {
	sess := T.SRC.Session(T.src_admin)

	// Look far enough back that we reliably capture the oldest retained event.
	start := cursor.Add(-365 * 24 * time.Hour)
	query := Query{
		"startDateTime": start.UTC().Format("2006-01-02T15:04:05.000Z"),
		"endDateTime":   time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"orderBy":       "created:asc",
		"compact":       false,
	}

	var activities []map[string]interface{}
	if err := sess.Admin().Activities(&activities, -1, 1, query); err != nil {
		Debug("reconcile: log-coverage probe failed (%v); forcing full sync.", err)
		return false
	}
	if len(activities) == 0 {
		// No activity at all in the window — nothing to differentially apply,
		// but also no gap. Safe to proceed differentially (it will be a no-op).
		return true
	}

	oldest, err := ReadKWTime(mapStr(activities[0], "created"))
	if err != nil {
		Debug("reconcile: could not parse oldest activity time; forcing full sync.")
		return false
	}
	// If the oldest available activity is after our cursor, the log no longer
	// covers the gap since our last run.
	if oldest.After(cursor) {
		Log("reconcile: source activity log oldest entry (%s) is newer than cursor (%s); a full sync is required.",
			oldest.Format(time.RFC3339), cursor.Format(time.RFC3339))
		return false
	}
	return true
}

// candidateSet is the result of scanning the activity-log window: the object
// ids that may have been deleted or had membership changed, and the owner
// emails whose subtrees the differential copy should refresh.
type candidateSet struct {
	deleteIDs      map[string]struct{}            // object ids nominated for deletion check
	membershipIDs  map[string]struct{}            // folder ids nominated for membership check
	changedFolders map[string]map[string]struct{} // owner email -> set of source folder ids with content changes
	ownerEmails    map[string]struct{}            // user emails whose objects changed (copy scope)
	userEmails     map[string]struct{}            // user emails nominated for lifecycle check
	sshKeyEmails   map[string]struct{}            // user emails nominated for SSH-key reconcile
	newest         time.Time                      // newest activity 'created' seen
}

func newCandidateSet() *candidateSet {
	return &candidateSet{
		deleteIDs:      make(map[string]struct{}),
		membershipIDs:  make(map[string]struct{}),
		changedFolders: make(map[string]map[string]struct{}),
		ownerEmails:    make(map[string]struct{}),
		userEmails:     make(map[string]struct{}),
		sshKeyEmails:   make(map[string]struct{}),
	}
}

// addChangedFolder records that a folder (by source id) had a content change for
// the given owner. Blank owner or folder is ignored.
func (cs *candidateSet) addChangedFolder(owner, folder_id string) {
	if IsBlank(owner) || IsBlank(folder_id) {
		return
	}
	set := cs.changedFolders[owner]
	if set == nil {
		set = make(map[string]struct{})
		cs.changedFolders[owner] = set
	}
	set[folder_id] = struct{}{}
}

// pollActivities scans the source admin activity log from `since` to now,
// classifying events into deletion and membership candidates. It mirrors the
// paging pattern used by the zero_byte_upload_notify polling path.
func (T *KW_MirrorTask) pollActivities(since time.Time) (*candidateSet, error) {
	sess := T.SRC.Session(T.src_admin)
	now := time.Now().UTC()

	cs := newCandidateSet()
	cs.newest = since

	query := Query{
		"startDateTime": since.UTC().Format("2006-01-02T15:04:05.000Z"),
		"endDateTime":   now.Format("2006-01-02T15:04:05.000Z"),
		"orderBy":       "created:asc",
		"compact":       false,
	}

	page := T.input.reconcile_page_size
	offset := 0
	for {
		var activities []map[string]interface{}
		if err := sess.Admin().Activities(&activities, offset, page, query); err != nil {
			return nil, err
		}

		for _, ev := range activities {
			if t, err := ReadKWTime(mapStr(ev, "created")); err == nil && t.After(cs.newest) {
				cs.newest = t
			}

			name := mapStr(ev, "eventName")
			_, is_delete := deleteEventNames[name]
			_, is_member := membershipEventNames[name]
			_, is_user := userEventNames[name]
			_, is_sshkey := sshKeyEventNames[name]
			_, is_content := contentEventNames[name]
			if !is_delete && !is_member && !is_user && !is_sshkey && !is_content {
				continue
			}

			obj_id, folder_id, owner := extractCandidate(ev)
			if !IsBlank(owner) {
				cs.ownerEmails[owner] = struct{}{}
			}
			if is_delete && !IsBlank(obj_id) {
				cs.deleteIDs[obj_id] = struct{}{}
			}
			if is_member && !IsBlank(folder_id) {
				cs.membershipIDs[folder_id] = struct{}{}
			}
			if is_content {
				// The affected folder is the file's parent (uploads) or the
				// folder itself (folder-create); extractCandidate resolves
				// folder_id to that in both cases.
				cs.addChangedFolder(owner, folder_id)
			}
			if is_user || is_sshkey {
				subject := userFromEvent(ev)
				if !IsBlank(subject) {
					if is_user {
						cs.userEmails[subject] = struct{}{}
					}
					if is_sshkey {
						cs.sshKeyEmails[subject] = struct{}{}
					}
				}
			}
		}

		if len(activities) < page {
			break
		}
		offset += page
	}
	return cs, nil
}

// extractCandidate probes an activity's payload for the affected object id, the
// folder id (for membership events), and the owning user's email. Shapes vary
// across event types and appliance versions, so it checks several known
// locations and returns blanks when it can't find one (a blank simply yields a
// no-op downstream).
func extractCandidate(ev map[string]interface{}) (obj_id, folder_id, owner string) {
	data, _ := ev["data"].(map[string]interface{})

	if data != nil {
		if folder, ok := data["folder"].(map[string]interface{}); ok {
			folder_id = mapStr(folder, "id")
			if o := ownerFromMap(folder); !IsBlank(o) {
				owner = o
			}
		}
		if file, ok := data["file"].(map[string]interface{}); ok {
			if id := mapStr(file, "id"); !IsBlank(id) {
				obj_id = id
			}
			if o := ownerFromMap(file); IsBlank(owner) && !IsBlank(o) {
				owner = o
			}
		}
		if parent, ok := data["parent_folder"].(map[string]interface{}); ok {
			if IsBlank(folder_id) {
				folder_id = mapStr(parent, "id")
			}
		}
	}

	// Top-level fallbacks.
	if IsBlank(obj_id) && IsBlank(folder_id) {
		if id := mapStr(ev, "objectId"); !IsBlank(id) {
			switch mapStr(ev, "objectType") {
			case "folder":
				folder_id = id
			default:
				obj_id = id
			}
		}
	}
	if IsBlank(owner) {
		owner = firstNonBlank(mapStr(ev, "userName"), mapStr(ev, "ownerName"))
	}
	// A membership event's affected object IS the folder.
	if IsBlank(folder_id) && !IsBlank(obj_id) {
		folder_id = obj_id
	}
	return
}

// ownerFromMap pulls a likely owner email out of a nested object.
func ownerFromMap(m map[string]interface{}) string {
	if owner, ok := m["owner"].(map[string]interface{}); ok {
		if e := mapStr(owner, "email"); !IsBlank(e) {
			return e
		}
	}
	return firstNonBlank(mapStr(m, "ownerEmail"), mapStr(m, "owner_email"))
}

// userFromEvent extracts the email of the user a lifecycle/SSH-key event is
// about. It only returns a value that looks like an email so we never mistake a
// display name for a subject; a blank result yields a no-op downstream.
func userFromEvent(ev map[string]interface{}) string {
	data, _ := ev["data"].(map[string]interface{})
	if data != nil {
		if user, ok := data["user"].(map[string]interface{}); ok {
			if e := mapStr(user, "email"); looksLikeEmail(e) {
				return e
			}
		}
		if e := mapStr(data, "email"); looksLikeEmail(e) {
			return e
		}
		if e := mapStr(data, "userEmail"); looksLikeEmail(e) {
			return e
		}
	}
	for _, key := range []string{"targetUser", "userEmail", "email", "userName", "objectId"} {
		if e := mapStr(ev, key); looksLikeEmail(e) {
			return e
		}
	}
	return ""
}

// looksLikeEmail is a cheap guard so display names or numeric ids aren't treated
// as user identifiers.
func looksLikeEmail(s string) bool {
	if IsBlank(s) {
		return false
	}
	at := strings.IndexByte(s, '@')
	return at > 0 && at < len(s)-1
}

// reconcile applies deletions and membership removals to the destination for
// the candidates found in the activity-log window. Additions/role-grants are
// handled by the (scoped) copy that runs before this; reconcile handles what
// the copy never does.
func (T *KW_MirrorTask) reconcile(cs *candidateSet) {
	Log("\n=== Reconcile (differential) ===\n")
	Log("- %d deletion, %d membership, %d user, %d ssh-key candidate(s).",
		len(cs.deleteIDs), len(cs.membershipIDs), len(cs.userEmails), len(cs.sshKeyEmails))

	sessions := make(map[string]KWSession)
	dst_session := func(email string) KWSession {
		if s, ok := sessions[email]; ok {
			return s
		}
		s := T.KW.Session(email)
		sessions[email] = s
		return s
	}

	deleted_files := T.Report.Tally("Reconciled File Deletes")
	deleted_folders := T.Report.Tally("Reconciled Folder Deletes")
	skipped := T.Report.Tally("Reconcile Skipped (still present)")
	members_added := T.Report.Tally("Reconciled Members Added")
	members_removed := T.Report.Tally("Reconciled Members Removed")
	users_deactivated := T.Report.Tally("Reconciled Users Deactivated")
	users_deleted := T.Report.Tally("Reconciled Users Deleted")
	keys_removed := T.Report.Tally("Reconciled SSH Keys Removed")

	for id := range cs.deleteIDs {
		T.reconcileDelete(id, dst_session, deleted_folders, deleted_files, skipped)
	}
	for id := range cs.membershipIDs {
		T.reconcileMembership(id, dst_session, members_added, members_removed)
	}
	for email := range cs.userEmails {
		T.reconcileUser(email, users_deactivated, users_deleted)
	}
	for email := range cs.sshKeyEmails {
		T.reconcileSshKeys(email, dst_session, keys_removed)
	}

	Log("\n=== Reconcile Complete ===")
}

// reconcileUser mirrors a user's system-level state from source onto the
// standby. New/active/reactivated users are provisioned by the scoped copy that
// runs before reconcile (which also brings their folders and SSH keys); this
// pass handles the removals the copy never does:
//   - source user fully gone → delete the standby user (retainData:false,
//     deleteUnsharedData:true) and drop all of their state rows;
//   - source user suspended/deactivated → deactivate the standby user.
//
// Source state is ground truth: FindUser returns the user regardless of
// active/verified flags, or ERR_NO_USER_FOUND when truly deleted.
func (T *KW_MirrorTask) reconcileUser(email string, deactivated, deleted Tally) {
	um, mapped := T.state.UserByEmail(email)

	src_user, err := T.SRC.Session(T.src_admin).Admin().FindUser(email)
	gone := err == ERR_NO_USER_FOUND || (src_user != nil && src_user.Deleted)
	if err != nil && err != ERR_NO_USER_FOUND {
		Err("Reconcile user %s: source lookup failed: %v", email, err)
		return
	}

	// Active on source: the scoped copy already (re)provisioned them. Nothing to
	// remove here.
	if !gone && src_user != nil && src_user.IsActive() {
		return
	}

	dst_user, derr := T.KW.Admin().FindUser(email)
	if derr == ERR_NO_USER_FOUND || dst_user == nil {
		// Not on the standby (or never synced) — clean up any stale state row.
		if mapped {
			T.state.DeleteUser(um.Src_id)
		}
		return
	}
	if derr != nil {
		Err("Reconcile user %s: destination lookup failed: %v", email, derr)
		return
	}

	if gone {
		Log("Reconcile user delete: %s (dst_id=%s) — removed on source.", email, dst_user.ID)
		params := SetParams(Query{"retainData": false, "deleteUnsharedData": true})
		if err := T.KW.Admin().DeleteUser(*dst_user, params); err != nil && !isAlreadyGone(err) {
			Err("Reconcile user %s: delete failed: %v", email, err)
			return
		}
		T.forgetUser(email, um, mapped)
		deleted.Add(1)
		return
	}

	// Suspended/deactivated on source (but not deleted): mirror that state.
	if dst_user.Suspended || dst_user.Deactivated {
		return // already inactive on standby
	}
	Log("Reconcile user deactivate: %s (dst_id=%s) — suspended/deactivated on source.", email, dst_user.ID)
	if err := T.KW.Admin().DeactivateUser(dst_user.ID); err != nil {
		Err("Reconcile user %s: deactivate failed: %v", email, err)
		return
	}
	deactivated.Add(1)
}

// forgetUser drops the state rows tied directly to a user that has been deleted
// on the standby (their SSH keys and the user mapping itself), so a later
// reconcile doesn't act on now-invalid ids. Folders and files the user owned are
// left to the next full-scan --prune, which removes them via the cascade the
// server already performed when the account was deleted.
func (T *KW_MirrorTask) forgetUser(email string, um user_mapping, mapped bool) {
	for _, k := range T.state.SshKeysForOwner(email) {
		T.state.DeleteSshKey(email, k.Src_id)
	}
	if mapped {
		T.state.DeleteUser(um.Src_id)
	}
}

// reconcileSshKeys removes SSH keys from the standby user that no longer exist
// on the source user. Key additions are handled by the scoped copy
// (CopyUserSshKeys). Source keys are ground truth; comparison is by fingerprint
// (fall back to name) since ids differ across servers.
func (T *KW_MirrorTask) reconcileSshKeys(email string, dst_session func(string) KWSession, removed Tally) {
	src_keys, err := T.SRC.Session(email).MySshPublicKeys()
	if err != nil {
		if IsAPIError(err, "ERR_PROFILE_SFTP_DISABLED", "ERR_SYSTEM_ROLE_SFTP_DISABLED", "ERR_ACCESS_USER") {
			return
		}
		Err("Reconcile SSH keys %s: source list failed: %v", email, err)
		return
	}

	src_present := make(map[string]struct{})
	for _, k := range src_keys {
		src_present[sshKeyIdentity(k.Fingerprint, k.Name)] = struct{}{}
	}

	for _, m := range T.state.SshKeysForOwner(email) {
		if _, ok := src_present[sshKeyIdentity(m.Fingerprint, m.Name)]; ok {
			continue
		}
		// Resolve the live destination id when the mapping lacks one (create
		// response may not have returned an id), so the key is actually removed.
		dst_id := m.Dst_id
		if dst_id <= 0 {
			resolved, ok := resolveDstSshKeyID(dst_session(email), m.Fingerprint, m.Name)
			if !ok {
				T.state.DeleteSshKey(email, m.Src_id)
				continue
			}
			dst_id = resolved
		}
		Log("Reconcile SSH key remove: [%s] '%s' (dst_id=%d) — removed on source.", email, m.Name, dst_id)
		if err := dst_session(email).DeleteMySshPublicKey(dst_id); err != nil && !isAlreadyGone(err) {
			Err("Reconcile SSH keys %s: delete '%s' failed: %v", email, m.Name, err)
			continue
		}
		T.state.DeleteSshKey(email, m.Src_id)
		removed.Add(1)
	}
}

// sshKeyIdentity keys an SSH key by fingerprint when available, else by name.
func sshKeyIdentity(fingerprint, name string) string {
	if !IsBlank(fingerprint) {
		return "fp:" + fingerprint
	}
	return "name:" + name
}

// resolveDstSshKeyID looks up a destination SSH key's live id by fingerprint
// (preferred) or name, for mappings that were recorded without a valid id.
// Returns (0, false) when the key isn't present on the destination.
func resolveDstSshKeyID(sess KWSession, fingerprint, name string) (int, bool) {
	keys, err := sess.MySshPublicKeys()
	if err != nil {
		return 0, false
	}
	for _, k := range keys {
		if !IsBlank(fingerprint) && k.Fingerprint == fingerprint {
			return k.ID, true
		}
	}
	for _, k := range keys {
		if k.Name == name && k.ID > 0 {
			return k.ID, true
		}
	}
	return 0, false
}

// reconcileDelete verifies a candidate id against the live source and, only if
// the source confirms it is gone, deletes the mapped destination object and
// drops the state row. A candidate we don't have mapped, or one still present
// on source, is a no-op (the latter bumps the "skipped" tally).
func (T *KW_MirrorTask) reconcileDelete(src_id string, dst_session func(string) KWSession, folders_tally, files_tally, skipped Tally) {
	// Folder?
	if fm, ok := T.state.GetFolder(src_id); ok {
		if T.sourceFolderGone(fm.Owner_email, src_id) {
			Log("Reconcile delete folder: [%s] /%s (dst_id=%s)", fm.Owner_email, fm.Path, fm.Dst_id)
			if err := dst_session(fm.Owner_email).Folder(fm.Dst_id).Delete(); err != nil && !isAlreadyGone(err) {
				Err("Failed to delete folder /%s: %v", fm.Path, err)
				return
			}
			T.state.DeleteFolder(src_id)
			folders_tally.Add(1)
		} else {
			skipped.Add(1)
		}
		return
	}

	// File?
	if fm, ok := T.state.GetFile(src_id); ok {
		if T.sourceFileGone(fm.Owner_email, src_id) {
			Log("Reconcile delete file: [%s] %s (dst_id=%s)", fm.Owner_email, fm.Name, fm.Dst_id)
			if err := dst_session(fm.Owner_email).File(fm.Dst_id).Delete(); err != nil && !isAlreadyGone(err) {
				Err("Failed to delete file %s: %v", fm.Name, err)
				return
			}
			T.state.DeleteFile(src_id)
			files_tally.Add(1)
		} else {
			skipped.Add(1)
		}
		return
	}
	// Unmapped candidate — nothing we synced, nothing to do.
}

// sourceFolderGone reports whether the source folder is deleted/absent. It asks
// for the folder including deleted ones; a hard 404 (isAlreadyGone) or the
// Deleted/PermDeleted flags both count as gone.
func (T *KW_MirrorTask) sourceFolderGone(owner_email, src_id string) bool {
	info, err := T.SRC.Session(owner_email).Folder(src_id).Info(SetParams(Query{"deleted": true}))
	if err != nil {
		return isAlreadyGone(err)
	}
	return info.Deleted || info.PermDeleted
}

// sourceFileGone reports whether the source file is deleted/absent.
func (T *KW_MirrorTask) sourceFileGone(owner_email, src_id string) bool {
	info, err := T.SRC.Session(owner_email).File(src_id).Info(SetParams(Query{"deleted": true}))
	if err != nil {
		return isAlreadyGone(err)
	}
	return info.Deleted || info.PermDeleted
}

// reconcileMembership brings a destination folder's membership back in line
// with the source folder's current members: grants (and role-updates) members
// present on source but not in our state, and removes members in our state that
// are no longer on source. Source membership is ground truth.
func (T *KW_MirrorTask) reconcileMembership(src_folder_id string, dst_session func(string) KWSession, added, removed Tally) {
	fm, ok := T.state.GetFolder(src_folder_id)
	if !ok {
		return // folder we never synced (or already deleted)
	}

	// If the source folder is gone, membership is moot — deletion reconcile
	// (or prune) handles the folder itself.
	if T.sourceFolderGone(fm.Owner_email, src_folder_id) {
		return
	}

	members, err := T.SRC.Session(fm.Owner_email).Folder(src_folder_id).Members()
	if err != nil {
		if !isAlreadyGone(err) {
			Err("Reconcile membership: failed to list source members for /%s: %v", fm.Path, err)
		}
		return
	}

	// Desired = current source membership (excluding the folder owner, which the
	// copier likewise skips).
	desired := make(map[string]int) // email -> role_id
	for _, m := range members {
		if IsBlank(m.User.Email) || m.User.Email == fm.Owner_email {
			continue
		}
		desired[m.User.Email] = m.RoleID
	}

	// Have = what we've recorded in state for this folder.
	have := make(map[string]int)
	for _, p := range T.state.PermsForFolder(src_folder_id) {
		have[p.Member_email] = p.Role_id
	}

	// Adds and role-changes: group by role for a batched AddUsersToFolder.
	add_by_role := make(map[int][]string)
	for email, role := range desired {
		if cur, ok := have[email]; !ok || cur != role {
			add_by_role[role] = append(add_by_role[role], email)
		}
	}
	for role_id, emails := range add_by_role {
		if err := dst_session(fm.Owner_email).Folder(fm.Dst_id).AddUsersToFolder(emails, role_id, false, true); err != nil {
			if !IsAPIError(err, "ERR_ENTITY_ROLE_IS_ASSIGNED") {
				Err("Reconcile membership: failed to add %v to /%s: %v", emails, fm.Path, err)
				continue
			}
		}
		for _, email := range emails {
			Log("Reconcile membership add: folder=/%s member=%s role=%d", fm.Path, email, role_id)
			T.state.SetPerm(perm_mapping{
				Src_folder_id: src_folder_id,
				Owner_email:   fm.Owner_email,
				Member_email:  email,
				Role_id:       role_id,
			})
			added.Add(1)
		}
	}

	// Removals: members we recorded that are no longer on source.
	for email := range have {
		if _, still := desired[email]; still {
			continue
		}
		dst_user, err := T.KW.Admin().FindUser(email)
		if err != nil || dst_user == nil {
			Err("Reconcile membership: failed to find dst user %s: %v", email, err)
			continue
		}
		Log("Reconcile membership remove: folder=/%s member=%s", fm.Path, email)
		if err := dst_session(fm.Owner_email).Folder(fm.Dst_id).RemoveUserFromFolder(dst_user.ID); err != nil && !isAlreadyGone(err) {
			Err("Reconcile membership: failed to remove %s from /%s: %v", email, fm.Path, err)
			continue
		}
		T.state.DeletePerm(src_folder_id, email)
		removed.Add(1)
	}
}

// provisionEmails is the deduped set of users needing a full per-user copy on
// the differential path: those whose account changed (new/reactivated) and
// those whose SSH keys changed. A full copy is appropriate here — a new user's
// whole tree is genuinely new, and SSH keys are copied as part of CopyUser.
// Content-only changes are NOT included; they are handled folder-scoped via
// changedFolders so unchanged folders are never re-walked.
func (cs *candidateSet) provisionEmails() []string {
	seen := make(map[string]struct{}, len(cs.userEmails)+len(cs.sshKeyEmails))
	for _, m := range []map[string]struct{}{cs.userEmails, cs.sshKeyEmails} {
		for e := range m {
			seen[e] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for e := range seen {
		out = append(out, e)
	}
	return out
}

// changedFoldersByOwner returns the changed-folder set as owner -> []folder_id,
// excluding owners already covered by a full provision copy (their whole tree
// is walked, so a folder-scoped pass would be redundant).
func (cs *candidateSet) changedFoldersByOwner(skip map[string]struct{}) map[string][]string {
	out := make(map[string][]string, len(cs.changedFolders))
	for owner, folders := range cs.changedFolders {
		if _, skipped := skip[owner]; skipped {
			continue
		}
		ids := make([]string, 0, len(folders))
		for id := range folders {
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			out[owner] = ids
		}
	}
	return out
}

// ---- small local helpers (kept package-private to avoid cross-package deps) ----

// mapStr extracts a string value from a map by key.
func mapStr(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok && v != nil {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// firstNonBlank returns the first non-blank string.
func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if !IsBlank(v) {
			return v
		}
	}
	return ""
}
