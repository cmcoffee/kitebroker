# kitebroker: Automation Assistant for kiteworks.

## (v0.1.4 - 09-10-1028)
    * Added support for 0byte files.

## (V0.1.3 - 12-10-2017)
    * File downloads will now provide parent folder information for file.
    * Expired tokens will now require reauthentication when set to auth = password.
    * Fixed issue with bad token causing fatal exit, now will retry/reprompt for credentials.
    * Added addition logging information and status messages.
    * Updated certain keys in config file.

## (v0.1.2 - 12-04-2017)
    * Updated text within config file.
    * Simplified signature setup (act as single account.)
    * Fixed issues when local_path is a root folder, such as C:\
    * Extended on error messages, including those provided for empty user lists for send_file and dli_export.

## (v0.1.1 - 06-21-2017)
    * Added configurable timeout for requests.
    * Fixed issue of switching from recv_mail to send_mail when using password auth_mode.

## (v0.1.0 - 06-20-2017)
    * Added send_file and recv_file task types for sending files via smtp and downloading files within inbox.
    * Added background database clean up thread.
    * Changed some internal background functions and calls for folder_download, folder_upload and dli_export.
    * Added logic for handling files in process. (ie.. currently being written to.)
    * --snoop option renamed --rest_snoop.

## (v0.0.9 - 05-04-2017)
    * Updated error handling on failed transfers.
    * Exposed and updated --snoop option for snooping on API calls to kiteworks appliance.
    * Modified handling of bandwidth displayed when file reaches 100% yet were waiting for a response from the server.
    * Updated underlying libraries go-cfg and go-logger.

## (v0.0.8d - 01-17-2017)
    * Updated DLI to no longer use last export as it may cause unexpected results, will instead use start_date always.
    * Changed download and upload progress info to run as a go thread instead of having the process check the time and update.
    * Updated go-logger and included it to prevent unwanted text from leaking through.
    * Fixed issue with bad tokens not getting cleared out.

## (v0.0.8c - 01-16-2017)
    * Updated DLI to update start time to provided export time from kiteworks appliance.
    * Added save_metadata to folder_download to store metadata of file with file.

## (v0.0.8b - 01-08-2017)
    * Changed the stream buffers from 2k to 4k.
    * Added better error handling when dli_export doesn't work.
    * Modified first time setup when using signature based authentication.

## (v0.0.8a - 01-08-2017)
    * API Configuration is now stored obfuscated within the config file for easy deployment.
    * Removed add user via csv option for now.
    * Refactored download, no longer renames folders, but lessened reliance on database.
    * Implemented chunked uploads task.
    * Implemented dli_export task
    * Implemented logging and logging options including rotation.
    * DB is now alot safer if application crashes during launch.
    * Added progress display for uploads and downloads.

## (v0.0.6a - 07-13-2016)
    * Appliance API configuration is now stored encrypted in database.
    * Database is now locked to system that launced/configured it.
    * Added ability to add users via CSV.
    * Added task handling.
    * Changed threading, added limits for both api calls and file transfers.
    * Resumeable downloads now implemented.

## (v0.0.2a - 06-18-2016)
	* Initial commit to Github.
	* Cleaned up thread handling for downloading files
	* Created rounding for seconds, easier on the eyes.

## (v0.0.1a - 06-15-2016)
	* Initial project for POC.
	* Recursively digs down subfolders from user's My Folder.
	* Creates local folders, download files and deletes them from appliance.
	* Generates a -info metadata file and saves in same directory as downloaded file.
