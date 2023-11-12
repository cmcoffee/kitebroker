package core

import (
	"time"
)

type SyncBase struct {
	SyncID      string    `json:"sync_id"`
	SourceID    string    `json:"sourceID"`
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Creator     string    `json:"creator"`
	Created     time.Time `json:"created"`
	Modified    time.Time `json"modified"`
}

type SyncRecord struct {
	SyncedVersionID string        `json:"synced_version_id,omitempty"`
	SyncedCommentID string        `json:"synced_comment_id,omitempty"`
	SyncedTaskID    string        `json:"synced_task_id,omitempty"`
	Versions        []SyncVersion `json:"versions,omitempty"`
	Comments        []SyncComment `json:"comments,omitempty"`
	Tasks           []SyncTask    `json:"tasks,omitempty"`
}

type SyncFolder struct {
	SyncBase
}

type SyncFile struct {
	SyncBase
	Versions []SyncVersion `json:"versions,omitempty"`
}

type SyncTask struct {
	SyncBase
	Disposition int       `json:"disposition"`
	Action      string    `json:"action"`
	Due         time.Time `json:"dueby"`
	AssignedTo  []string  `json:"users"`
	Completed   bool      `json:"is_complete"`
	CompletedOn time.Time `json:"completed_date"`
	Message     string    `json:"message"`
}

type SyncComment struct {
	SyncBase
	Message  string `json:"message"`
	Original string `json:"original_message"`
	Flag     int    `json:"flag,omitempty"`
}

type SyncVersion struct {
	SyncBase
	Ver      int    `json:"version_num`
	Uploader string `json:"Uploader"`
	Size     int64  `json:"size"`
	URI      string `json:"URI"`
}

type SyncUser struct {
	SyncBase
}

type SyncPermission struct {
	SyncBase
}
