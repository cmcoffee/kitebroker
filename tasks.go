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
	command.Register(new(user.ListTask))
	command.Register(new(user.FolderUploadTask))
	command.Register(new(user.FolderDownloadTask))

	// Register Signature Only Tasks:
	command.RegisterAdmin(new(FTA.Broker))
	command.RegisterAdmin(new(admin.FolderFileExpiryTask))
	command.RegisterAdmin(new(admin.UserProfilerTask))
	command.RegisterAdmin(new(admin.EmailDraftExpiryTask))
}
