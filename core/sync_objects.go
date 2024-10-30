package core

import (
	"time"
)

type SyncFile struct {
	Name            string        `json:"name"`
	Description     string        `json:"description,omitempty"`
	SrcID           string        `json:"source_id"`
	DestID          string        `json:"dest_id,omitempty"`
	Created         time.Time     `json:"created"`
	Modified        time.Time     `json:"modified"`
	SrcFolderID     string        `json:"parent_id,omitempty"`
	DestFolderID    string        `json:"kw_folder_id,omitempty"`
	ParentOwner     string        `json:"parent_owner"`
	SyncedVersionID string        `json:"synced_version_id,omitempty"`
	SyncedCommentID string        `json:"synced_comment_id,omitempty"`
	SyncedTaskID    string        `json:"synced_task_id,omitempty"`
	Versions        []SyncVersion `json:"versions,omitempty"`
	Comments        []SyncComment `json:"comments,omitempty"`
	Tasks           []SyncTask    `json:"tasks,omitempty"`
}

type SyncFolder struct {
	Name              string           `json:"name"`
	Description       string           `json:"description,omitempty"`
	SrcID             string           `json:"source_id"`
	DestID            string           `json:"dest_id,omitempty"`
	Created           time.Time        `json:"created"`
	Modified          time.Time        `json:"modified"`
	FullPath          string           `json:"full_path"`
	Owner             string           `json:"owner"`
	SyncedPermissions bool             `json:"synced_permissions"`
	Permissions       []SyncPermission `json:"permissions"`
}

type SyncPermission struct {
	User string `json:"username"`
	Role int    `json:"role"`
}

type SyncTask struct {
	ID          string    `json:"id"`
	Disposition int       `json:"disposition"`
	Action      string    `json:"action"`
	Created     time.Time `json:"created_at"`
	Creator     string    `json:"creator"`
	Due         time.Time `json:"dueby"`
	AssignedTo  []string  `json:"users"`
	Completed   bool      `json:"is_complete"`
	CompletedOn time.Time `json:"completed_date"`
	Message     string    `json:"message"`
}

type SyncVersion struct {
	SrcID    string    `json:"src_id"`
	DestID   string    `json:"dest_id"`
	Ver      int       `json:"version_num`
	Uploader string    `json:"Uploader"`
	Name     string    `json:"name"`
	Created  time.Time `json:"utc_created"`
	Modified time.Time `json"utc_modified"`
	Size     int64     `json:"size"`
}

type SyncComment struct {
	ID       string    `json:"id"`
	Created  time.Time `json:"created_at"`
	Creator  string    `json:"creator"`
	Message  string    `json:"message"`
	Original string    `json:"original_message"`
	Flag     int       `json:"flag,omitempty"`
}
