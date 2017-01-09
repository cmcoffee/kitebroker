# kitebroker: Automation Assistant for kiteworks.

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
