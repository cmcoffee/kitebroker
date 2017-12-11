package main

func init() {
	// Sets configuration defaults
	Config.Defaults(`
[configuration]
# kiteworks API Configuration ##############################
server = 
account = 
auth_mode = signature
redirect_uri = https://kitebroker
proxy =

# Auto-Generated Config ####################################
api_cfg_0 = 
api_cfg_1 = 
############################################################

# Verify SSL Certificate on Appliance. (improves security)
ssl_verify = yes

# Continuous Mode, run indefinetly.
continuous_mode = yes
continuous_rate_secs = 30

# Local base path for monitoring, downloading and uploading of files and folders. 
# "My Folder" should be located under kiteworks\My Folder when using folder_upload or folder_download.
# "user@domain.com" should be located under kiteworks\user@domain.com when using send_file, recv_file or dli_export.
local_path = kiteworks

# kiteworks folder filter, allows specific folders to be processed, use commas when specifying multiple folders.
# (ie.. kw_folder_filter = My Folder, Accounting, IT Training Documents)
# If left blank, all sub/folders will be processed under the local_path setting.
kw_folder_filter =

# Upload chunk size in kilobytes.
upload_chunk_size = 68157

# Logging settings
log_path = log
log_size = 10240
log_rotate = 5

# DB Cleanup interval.
cleanup_time_secs = 86400

# Removed source copy of file from either local machine(when uploading) or kiteworks appliance(when downloading).
delete_source_files_on_complete = no

# Task Types:
# send_file       :Emails files to user or users.
# recv_file       :Downloads files sent to user.
# folder_download :Download a specific remote folder.
# folder_upload   :Upload files to a specific folder.
# dli_export      :Creates accounts based on CSV input. (Requires account is a DLI admin.)

task = recv_file

############ Task Specific Options & Settings ###############

[send_file:opts]
# Following addresses will be appended to email addresses gathered under the local_path, 
# ie.. user@domain.com directory (under the local_path) will send to user@domain.com as well as emails specified below.
to = 
cc =
bcc =
subject = Kitebroker File Upload

[recv_file:opts]
sender =
email_age_days = 7
download_full_email = yes
download_file_manifest = yes
download_seperate_email_body = no

[folder_download:opts]
# Saves metadata for downloaded files as <file>-info.
save_metadata = no

[folder_upload:opts]
# Instructs kitebroker to create folder structure regardless of whether there are files or not.
create_empty_folders = yes

[dli_export:opts]
# Start date for beginign of DLI export.
start_date = 2017-Jan-01

# Specify which type of exports to process.
export_activities = yes
export_emails = yes
export_files = no
`)
}
