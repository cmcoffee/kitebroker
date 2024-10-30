/*
	This is for registering the task modules to the kitebroker menu.
*/

package main

import (
	"kitebroker/tasks/admin"
	"kitebroker/tasks/user"
)

func init() {
	// Register Universal Tasks:
	command.Register(new(user.ListTask))
	command.Register(new(user.FolderUploadTask))
	command.Register(new(user.FolderDownloadTask))

	// Register Signature Only Tasks:
	command.RegisterAdmin(new(admin.CSVOnboardTask))
	command.RegisterAdmin(new(admin.FolderFileExpiryTask))
	command.RegisterAdmin(new(admin.UserProfilerTask))
	command.RegisterAdmin(new(admin.FileCleanerTask))
	command.RegisterAdmin(new(admin.UserRemoverTask))
	command.RegisterAdmin(new(admin.KW_TO_KWTask))
	command.RegisterAdmin(new(admin.MoveMyFolder))
	command.RegisterAdmin(new(admin.AddUserTask))
	command.RegisterAdmin(new(admin.MetadataTask))
	command.RegisterAdmin(new(admin.FolderReportTask))
	command.RegisterAdmin(new(admin.UserInfoTask))
}
