/*
	This is for registering the task modules to the kitebroker menu.
*/

package main

import (
	. "github.com/cmcoffee/kitebroker/tasks"
)

func init() {
	// Register Universal Tasks:
	jobs.Register("upload", "Upload folders and/or files to kiteworks.", new(FolderUploadTask))
	jobs.Register("download", "Download folders and/or files from kiteworks.", new(FolderDownloadTask))

	// Register Signature Only Tasks:
	jobs.RegisterAdmin("folder_file_expiry", "Modifies the folder and file expiry.", new(FolderFileExpiryTask))
	jobs.RegisterAdmin("user_reprofiler", "Change user profile based on last activity date.", new(UserProfilerTask))
	jobs.RegisterAdmin("mail_cleanup", "Expire email drafts and attachments older than specified date.", new(EmailDraftExpiryTask))
}
