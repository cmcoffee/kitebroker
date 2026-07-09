package mirror

import (
	"fmt"
	"strings"
	"time"

	. "github.com/cmcoffee/kitebroker/core"
)

// SyncState persists src→dst mappings for the standby mirror.
// These tables are the system of record for "what we synced and where" —
// without them, deletions cannot be propagated because the source object
// may already be gone by the time the delete event arrives.
type SyncState struct {
	db       Database
	users    Table
	folders  Table
	files    Table
	perms    Table
	ssh_keys Table
}

type user_mapping struct {
	Src_id       string `json:"src_id"`
	Dst_id       string `json:"dst_id"`
	Email        string `json:"email"`
	Profile_id   int    `json:"profile_id"`
	Last_seen_ts int64  `json:"last_seen_ts"`
}

type folder_mapping struct {
	Src_id        string `json:"src_id"`
	Dst_id        string `json:"dst_id"`
	Owner_email   string `json:"owner_email"`
	Path          string `json:"path"`
	Parent_src_id string `json:"parent_src_id"`
	Last_seen_ts  int64  `json:"last_seen_ts"`
}

type file_mapping struct {
	Src_id        string `json:"src_id"`
	Dst_id        string `json:"dst_id"`
	Owner_email   string `json:"owner_email"`
	Name          string `json:"name"`
	Parent_src_id string `json:"parent_src_id"`
	Size          int64  `json:"size"`
	Client_mod    string `json:"client_modified"`
	Fingerprint   string `json:"fingerprint"`
	Last_seen_ts  int64  `json:"last_seen_ts"`
}

type perm_mapping struct {
	Src_folder_id string `json:"src_folder_id"`
	Owner_email   string `json:"owner_email"`
	Member_email  string `json:"member_email"`
	Role_id       int    `json:"role_id"`
	Last_seen_ts  int64  `json:"last_seen_ts"`
}

type ssh_key_mapping struct {
	Owner_email  string `json:"owner_email"`
	Src_id       int    `json:"src_id"`
	Dst_id       int    `json:"dst_id"`
	Name         string `json:"name"`
	Fingerprint  string `json:"fingerprint"`
	Last_seen_ts int64  `json:"last_seen_ts"`
}

// NewSyncState attaches a SyncState to the given sub-database.
func NewSyncState(db Database) *SyncState {
	return &SyncState{
		db:       db,
		users:    db.Table("users"),
		folders:  db.Table("folders"),
		files:    db.Table("files"),
		perms:    db.Table("perms"),
		ssh_keys: db.Table("ssh_keys"),
	}
}

func (s *SyncState) SetUser(m user_mapping) {
	m.Last_seen_ts = time.Now().Unix()
	s.users.Set(m.Src_id, &m)
}

func (s *SyncState) GetUser(src_id string) (m user_mapping, ok bool) {
	ok = s.users.Get(src_id, &m)
	return
}

func (s *SyncState) DeleteUser(src_id string) {
	s.users.Unset(src_id)
}

// UserByEmail returns the mapping for a user by email. The users table is keyed
// by source id, so this scans; user counts are modest and reconcile only calls
// it for candidates surfaced by the activity log.
func (s *SyncState) UserByEmail(email string) (m user_mapping, ok bool) {
	for _, key := range s.users.Keys() {
		var candidate user_mapping
		if s.users.Get(key, &candidate) && candidate.Email == email {
			return candidate, true
		}
	}
	return
}

// SshKeysForOwner returns every persisted SSH-key mapping for an owner email.
// Keys are keyed "owner_email|src_id" (see ssh_key_key), so this filters by the
// "owner_email|" prefix.
func (s *SyncState) SshKeysForOwner(owner_email string) (out []ssh_key_mapping) {
	prefix := owner_email + "|"
	for _, key := range s.ssh_keys.Keys() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		var m ssh_key_mapping
		if s.ssh_keys.Get(key, &m) {
			out = append(out, m)
		}
	}
	return
}

func (s *SyncState) SetFolder(m folder_mapping) {
	m.Last_seen_ts = time.Now().Unix()
	s.folders.Set(m.Src_id, &m)
}

func (s *SyncState) GetFolder(src_id string) (m folder_mapping, ok bool) {
	ok = s.folders.Get(src_id, &m)
	return
}

func (s *SyncState) DeleteFolder(src_id string) {
	s.folders.Unset(src_id)
}

func (s *SyncState) SetFile(m file_mapping) {
	m.Last_seen_ts = time.Now().Unix()
	s.files.Set(m.Src_id, &m)
}

func (s *SyncState) GetFile(src_id string) (m file_mapping, ok bool) {
	ok = s.files.Get(src_id, &m)
	return
}

func (s *SyncState) DeleteFile(src_id string) {
	s.files.Unset(src_id)
}

func perm_key(src_folder_id, member_email string) string {
	return src_folder_id + "|" + member_email
}

func (s *SyncState) SetPerm(m perm_mapping) {
	m.Last_seen_ts = time.Now().Unix()
	s.perms.Set(perm_key(m.Src_folder_id, m.Member_email), &m)
}

func (s *SyncState) GetPerm(src_folder_id, member_email string) (m perm_mapping, ok bool) {
	ok = s.perms.Get(perm_key(src_folder_id, member_email), &m)
	return
}

func (s *SyncState) DeletePerm(src_folder_id, member_email string) {
	s.perms.Unset(perm_key(src_folder_id, member_email))
}

// PermsForFolder returns every persisted perm mapping for a source folder.
// Perms are keyed "src_folder_id|member_email" (see perm_key), so this filters
// the perms table by the "src_folder_id|" prefix. Used by the reconcile pass to
// diff a folder's known members against its current source membership.
func (s *SyncState) PermsForFolder(src_folder_id string) (out []perm_mapping) {
	prefix := src_folder_id + "|"
	for _, key := range s.perms.Keys() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		var m perm_mapping
		if s.perms.Get(key, &m) {
			out = append(out, m)
		}
	}
	return
}

func ssh_key_key(owner_email string, src_id int) string {
	return fmt.Sprintf("%s|%d", owner_email, src_id)
}

func (s *SyncState) SetSshKey(m ssh_key_mapping) {
	m.Last_seen_ts = time.Now().Unix()
	s.ssh_keys.Set(ssh_key_key(m.Owner_email, m.Src_id), &m)
}

func (s *SyncState) GetSshKey(owner_email string, src_id int) (m ssh_key_mapping, ok bool) {
	ok = s.ssh_keys.Get(ssh_key_key(owner_email, src_id), &m)
	return
}

func (s *SyncState) DeleteSshKey(owner_email string, src_id int) {
	s.ssh_keys.Unset(ssh_key_key(owner_email, src_id))
}

// OnUserMapped, OnFolderCloned, OnFileUploaded, OnPermissionGranted, and
// OnSshKeyCopied implement the kiteworks.Observer interface. The copier
// calls them as objects are mirrored; each persists a src→dst mapping so
// the reconcile pass can later detect orphans on either side.

func (s *SyncState) OnUserMapped(src KiteUser, dst KiteUser, dst_profile_id int) {
	s.SetUser(user_mapping{
		Src_id:     src.ID,
		Dst_id:     dst.ID,
		Email:      src.Email,
		Profile_id: dst_profile_id,
	})
}

func (s *SyncState) OnFolderCloned(src KiteObject, dst KiteObject, owner_email string) {
	s.SetFolder(folder_mapping{
		Src_id:        src.ID,
		Dst_id:        dst.ID,
		Owner_email:   owner_email,
		Path:          src.Path,
		Parent_src_id: src.ParentID,
	})
}

func (s *SyncState) OnFileUploaded(src KiteObject, dst KiteObject, parent_src_id string, owner_email string) {
	s.SetFile(file_mapping{
		Src_id:        src.ID,
		Dst_id:        dst.ID,
		Owner_email:   owner_email,
		Name:          src.Name,
		Parent_src_id: parent_src_id,
		Size:          src.Size,
		Client_mod:    src.ClientModified,
		Fingerprint:   src.Fingerprint,
	})
}

func (s *SyncState) OnPermissionGranted(src_folder_id string, member_email string, role_id int, owner_email string) {
	s.SetPerm(perm_mapping{
		Src_folder_id: src_folder_id,
		Owner_email:   owner_email,
		Member_email:  member_email,
		Role_id:       role_id,
	})
}

func (s *SyncState) OnSshKeyCopied(owner_email string, src KiteSshPublicKey, dst KiteSshPublicKey) {
	s.SetSshKey(ssh_key_mapping{
		Owner_email: owner_email,
		Src_id:      src.ID,
		Dst_id:      dst.ID,
		Name:        src.Name,
		Fingerprint: src.Fingerprint,
	})
}
