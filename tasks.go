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
	// WIP // command.Register("send_file", "Send files/folders in kiteworks.", new(user.SendFileTask))
	command.Register("ls", "List folders and/or files in kiteworks.", new(user.ListTask))
	command.Register("upload", "Upload folders and/or files to kiteworks.", new(user.FolderUploadTask))
	command.Register("download", "Download folders and/or files from kiteworks.", new(user.FolderDownloadTask))

	// Register Signature Only Tasks:
	command.RegisterAdmin("folder_file_expiry", "Modifies the folder and file expiry.", new(admin.FolderFileExpiryTask))
	command.RegisterAdmin("user_reprofiler", "Change user profiles.", new(admin.UserProfilerTask))
	command.RegisterAdmin("mail_cleanup", "Expire email drafts and attachments older than specified date.", new(admin.EmailDraftExpiryTask))
	command.RegisterAdmin("ftabroker", "Transfer files/repair permissions on kiteworks folders based on FTA CSV export.", new(FTA.Broker))
}
