/*
	This is for registering the task modules to the kitebroker menu.
*/

package main

import (
	. "github.com/cmcoffee/kitebroker/tasks"
)

func init() {
	jobs.RegisterAdmin("folder_file_expiry", "Modifies the folder and file expiry.", new(FolderFileExpiryTask))
	jobs.RegisterAdmin("user_reprofiler", "Change user profile based on last activity date.", new(UserProfilerTask))
	jobs.Register("folder_download", "Download folders from kiteworks.", new(FolderDownloadTask))
	jobs.Register("folder_upload", "Upload folders to kiteworks.", new(FolderUploadTask))
}
