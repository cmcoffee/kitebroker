/*
	This is for registering the task modules to the kitebroker menu.
*/

package main

import (
	"github.com/cmcoffee/kitebroker/tasks/admin"
	"github.com/cmcoffee/kitebroker/tasks/admin/FTA"
	"github.com/cmcoffee/kitebroker/tasks/user"
)

func init() {
	// Register Universal Tasks:
	// WIP // jobs.Register("send_file", "Send files/folders in kiteworks.", new(user.SendFileTask))
	jobs.Register("upload", "Upload folders and/or files to kiteworks.", new(user.FolderUploadTask))
	jobs.Register("download", "Download folders and/or files from kiteworks.", new(user.FolderDownloadTask))
	jobs.Register("ls", "List folders and/or files in kiteworks.", new(user.ListTask))

	// Register Signature Only Tasks:
	jobs.RegisterAdmin("folder_file_expiry", "Modifies the folder and file expiry.", new(admin.FolderFileExpiryTask))
	jobs.RegisterAdmin("user_reprofiler", "Change user profile based on last activity date.", new(admin.UserProfilerTask))
	jobs.RegisterAdmin("mail_cleanup", "Expire email drafts and attachments older than specified date.", new(admin.EmailDraftExpiryTask))
	jobs.RegisterAdmin("ftabroker", "Assists with the migration of FTA workspaces.", new(FTA.Broker))
	jobs.RegisterAdmin("migration_permission_fixer", "Fixed permissions on kiteworks based on FTA CSV export.", new(admin.FolderPermissionFixer))
}
