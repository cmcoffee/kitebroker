package main

func init() {
	// Sets configuration defaults
	Config.Defaults(`
[configuration]
# kiteworks API Configuration ##############################
server = 
account = 
auth_mode = password
redirect_uri = https://kitebroker
timeout_secs = 15
proxy =

# Auto-Generated Config ####################################
api_cfg_0 = 
api_cfg_1 = 
############################################################

# Verify SSL Certificate on Appliance. (improves security)
ssl_verify = yes

# Continuous Mode, run indefinetly.
continuous_mode = no
continuous_rate_secs = 30

# Logging settings
log_path = log
log_size = 10240
log_rotate = 5

# DB Cleanup interval.
cleanup_time_secs = 86400

# Local path for file upload and download.
local_path = kiteworks

# kiteworks Remote Folder for downloading/uploading, leave blank for all.
kw_folder = My Folder

# Upload chunk size in kilobytes.
upload_chunk_size = 32768

# Removed source copy of file from either local machine(when uploading) or kiteworks appliance(when downloading).
delete_source_files_on_complete = no

# Task Types:
# send_file      :Emails files to user or users.
# recv_file      :Downloads files sent to user.
# folder_download :Download a specific remote folder.
# folder_upload   :Upload files to a specific folder.
# dli_export      :Creates accounts based on CSV input. (Requires account is a DLI admin.)
task = folder_download

[send_file:opts]
to =
cc =
bcc =
subject = Upload Summary

[recv_file:opts]
sender =
email_age_days = 7
download_email_body = no
download_full_email = yes
download_file_manifest = no

[folder_download:opts]
# Saves metadata for downloaded files as <file>-info.
save_metadata = no

[folder_upload:opts]
# Instructs kitebroker to create folder structure regardless of whether there are files or not.
create_empty_folders = no

[dli_export:opts]
# Start date for beginign of DLI export.
start_date = 2017-Jan-01

# Specify which type of exports to process.
export_activities = yes
export_emails = yes
export_files = no
`)
}