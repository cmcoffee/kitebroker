package main

func init() {
	// Sets configuration defaults
	Config.Defaults(`
[configuration]
# kiteworks API Configuration ##############################
server = 
account = 
auth_mode = signature
redirect_uri = https://kitebroker/

# Proxy server in URI format. (ie.. https://proxy.com:3128)
proxy_uri =

# Verify SSL Certificate on Appliance. (improves security)
ssl_verify = yes

# Continuous Mode, run indefinetly.
continuous_mode = yes
continuous_rate_secs = 30

# Local base path for monitoring, downloading and uploading of files and folders. 
# "My Folder" should be located under kiteworks\My Folder when using folder_upload or folder_download.
# "user@domain.com" should be located under kiteworks\user@domain.com when using send_file or recv_file.
# (Default path is ./kiteworks)
local_path = kiteworks

# Path to store logs in. (Default is ./log)
log_path = log

# Task Types:
# send_file       :Emails files to user or users.
# recv_file       :Downloads files sent to user.
# folder_download :Download a specific remote folder.
# folder_upload   :Upload files to a specific folder.

task = folder_upload

############ Task Specific Options & Settings ###############

[send_file:opts]
# Following addresses will be appended to email addresses gathered under the local_path, 
# ie.. user@domain.com directory (under the local_path) will send to user@domain.com as well as emails specified below.
to = 
cc =
bcc =
subject = Kitebroker File Upload
delete_source_files_on_complete = no

[recv_file:opts]
sender =
email_age_days = 7
download_full_email = yes
download_file_manifest = yes
download_seperate_email_body = no
supplemental_metadata_info_file = no

[folder_download:opts]
# Specify which folders to download, none for all.
select_folders =
skip_empty_files = no
supplemental_metadata_info_file = no
delete_source_files_on_complete = no

[folder_upload:opts]
# Specify which folders to upload, none for all.
select_folders =
skip_empty_files = no
create_empty_folders = yes
delete_source_files_on_complete = no

# [tweaks]
# db_cleanup_time_secs = 86400
# chunk_size_mb = 68
# log_size_mb = 20
# log_rotate = 5

`)
}
