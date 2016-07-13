# kitebroker: Automation Assistant for kiteworks.

### WIP
    * Uploads are not yet implemented.    

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
