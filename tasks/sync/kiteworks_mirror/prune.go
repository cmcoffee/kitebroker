package mirror

import (
	. "github.com/cmcoffee/kitebroker/core"
)

// prune walks the state tables and deletes anything on destination whose
// Last_seen_ts is older than run_start_ts — i.e. objects that were present
// on a prior run but weren't refreshed by the run that just completed.
//
// Folders are pruned first; their delete cascades on the server, so most
// child files/perms are gone by the time we get to those tables (we tolerate
// the resulting 404s). Users are NEVER auto-deleted — stale users are
// reported so an operator can decide whether to deactivate or remove them.
func (T *KW_MirrorTask) prune(run_start_ts int64) {
	Log("\n=== Pruning orphans (objects on destination no longer on source) ===\n")

	pruned_folders := T.Report.Tally("Pruned Folders")
	pruned_files := T.Report.Tally("Pruned Files")
	pruned_perms := T.Report.Tally("Pruned Permissions")
	pruned_ssh_keys := T.Report.Tally("Pruned SSH Keys")
	users_deactivated := T.Report.Tally("Pruned Users Deactivated")
	users_deleted := T.Report.Tally("Pruned Users Deleted")

	sessions := make(map[string]KWSession)
	dst_session := func(email string) KWSession {
		if s, ok := sessions[email]; ok {
			return s
		}
		s := T.KW.Session(email)
		sessions[email] = s
		return s
	}

	T.pruneFolders(run_start_ts, dst_session, pruned_folders)
	T.pruneFiles(run_start_ts, dst_session, pruned_files)
	T.prunePerms(run_start_ts, dst_session, pruned_perms)
	T.pruneSshKeys(run_start_ts, dst_session, pruned_ssh_keys)
	T.pruneStaleUsers(run_start_ts, users_deactivated, users_deleted)

	Log("\n=== Prune Complete ===")
}

func (T *KW_MirrorTask) pruneFolders(run_start_ts int64, dst_session func(string) KWSession, tally Tally) {
	for _, key := range T.state.folders.Keys() {
		var m folder_mapping
		if !T.state.folders.Get(key, &m) {
			continue
		}
		if m.Last_seen_ts >= run_start_ts {
			continue
		}
		Log("Prune folder: [%s] /%s (dst_id=%s)", m.Owner_email, m.Path, m.Dst_id)
		if err := dst_session(m.Owner_email).Folder(m.Dst_id).Delete(); err != nil {
			if !isAlreadyGone(err) {
				Err("Failed to delete folder /%s: %v", m.Path, err)
				continue
			}
		}
		T.state.DeleteFolder(m.Src_id)
		tally.Add(1)
	}
}

func (T *KW_MirrorTask) pruneFiles(run_start_ts int64, dst_session func(string) KWSession, tally Tally) {
	for _, key := range T.state.files.Keys() {
		var m file_mapping
		if !T.state.files.Get(key, &m) {
			continue
		}
		if m.Last_seen_ts >= run_start_ts {
			continue
		}
		Log("Prune file: [%s] %s (dst_id=%s)", m.Owner_email, m.Name, m.Dst_id)
		if err := dst_session(m.Owner_email).File(m.Dst_id).Delete(); err != nil {
			if !isAlreadyGone(err) {
				Err("Failed to delete file %s: %v", m.Name, err)
				continue
			}
		}
		T.state.DeleteFile(m.Src_id)
		tally.Add(1)
	}
}

func (T *KW_MirrorTask) prunePerms(run_start_ts int64, dst_session func(string) KWSession, tally Tally) {
	for _, key := range T.state.perms.Keys() {
		var m perm_mapping
		if !T.state.perms.Get(key, &m) {
			continue
		}
		if m.Last_seen_ts >= run_start_ts {
			continue
		}

		var folder_m folder_mapping
		if !T.state.folders.Get(m.Src_folder_id, &folder_m) {
			// Folder is gone (we just pruned it, or it never existed) —
			// the perm is implicitly removed. Just drop the state row.
			T.state.DeletePerm(m.Src_folder_id, m.Member_email)
			continue
		}

		dst_user, err := T.KW.Admin().FindUser(m.Member_email)
		if err != nil || dst_user == nil {
			Err("Failed to find dst user for perm cleanup %s: %v", m.Member_email, err)
			continue
		}

		Log("Prune perm: folder=/%s member=%s", folder_m.Path, m.Member_email)
		if err := dst_session(m.Owner_email).Folder(folder_m.Dst_id).RemoveUserFromFolder(dst_user.ID); err != nil {
			if !isAlreadyGone(err) {
				Err("Failed to remove perm: %v", err)
				continue
			}
		}
		T.state.DeletePerm(m.Src_folder_id, m.Member_email)
		tally.Add(1)
	}
}

func (T *KW_MirrorTask) pruneSshKeys(run_start_ts int64, dst_session func(string) KWSession, tally Tally) {
	for _, key := range T.state.ssh_keys.Keys() {
		var m ssh_key_mapping
		if !T.state.ssh_keys.Get(key, &m) {
			continue
		}
		if m.Last_seen_ts >= run_start_ts {
			continue
		}
		// The recorded destination id may be missing (0) if the create response
		// carried no id — but the key can still be present on the destination.
		// Resolve the real id from the live key list by fingerprint/name so the
		// orphan is actually deleted rather than left behind.
		dst_id := m.Dst_id
		if dst_id <= 0 {
			if resolved, ok := resolveDstSshKeyID(dst_session(m.Owner_email), m.Fingerprint, m.Name); ok {
				dst_id = resolved
			} else {
				Debug("Prune SSH key: [%s] '%s' not found on destination; dropping stale mapping.", m.Owner_email, m.Name)
				T.state.DeleteSshKey(m.Owner_email, m.Src_id)
				continue
			}
		}
		Log("Prune SSH key: [%s] '%s' (dst_id=%d)", m.Owner_email, m.Name, dst_id)
		if err := dst_session(m.Owner_email).DeleteMySshPublicKey(dst_id); err != nil {
			if !isAlreadyGone(err) {
				Err("Failed to delete SSH key '%s': %v", m.Name, err)
				continue
			}
		}
		T.state.DeleteSshKey(m.Owner_email, m.Src_id)
		tally.Add(1)
	}
}

// pruneStaleUsers applies source-state mirroring to users that were present on
// a prior run but not refreshed by this (full) scan: if the source user is gone
// the standby user is deleted; if suspended/deactivated on source it is
// deactivated on the standby. This reuses reconcileUser so the full-scan prune
// and the differential path handle user removal identically. Only reached from
// the --prune path, which is always a full authoritative scan.
func (T *KW_MirrorTask) pruneStaleUsers(run_start_ts int64, deactivated, deleted Tally) {
	for _, key := range T.state.users.Keys() {
		var m user_mapping
		if !T.state.users.Get(key, &m) {
			continue
		}
		if m.Last_seen_ts >= run_start_ts {
			continue
		}
		T.reconcileUser(m.Email, deactivated, deleted)
	}
}

// isAlreadyGone reports whether the error is the API's way of saying the
// object we tried to delete had already been removed (typically because a
// parent folder's delete cascaded). Such errors aren't real failures during
// a prune sweep.
func isAlreadyGone(err error) bool {
	return IsAPIError(err, "ERR_ENTITY_DELETED", "ERR_ENTITY_NOT_FOUND", "ERR_ENTITY_PARENT_FOLDER_DELETED")
}
