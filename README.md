## Kitebroker: Automation Assistant for Kiteworks - User Guide

**Introduction**

Kitebroker is a command-line tool and framework designed for automating flows for files and administrative tasks via the Kiteworks API. This guide provides instructions on setting up and using Kitebroker to streamline your Kiteworks operations. Kitebroker supports both OAuth 2.0 with Signature Authorization and JSON Web Token (JWT) based authentication, providing flexibility for different security requirements.

**Configuration**

Kitebroker utilizes a configuration file, `kitebroker.ini`, to store persistent settings. This file allows you to pre-configure your Kiteworks connection details, eliminating the need to repeatedly enter them during each invocation. The `kitebroker.ini` file will be located in the same directory as the Kitebroker executable.

**Example `kitebroker.ini`:**

```ini
[configuration]
auth_flow = signature # (or jwt or password)
redirect_uri = https://kitebroker
ssl_verify = true
proxy_uri = 
server = kiteworks.example.com

[do_not_modify]
api_cfg_0 = 
api_cfg_1 = 
```

**Configuration Options:**

*   **`auth_flow`**:  Specifies the authentication flow. Valid values are `signature`, `jwt`, and `password`.
*   **`redirect_uri`**: The redirect URI used during the OAuth authorization flow (required for Signature Authorization).
*   **`ssl_verify`**:  A boolean value (true/false) indicating whether to verify the SSL certificate of the Kiteworks server.
*   **`proxy_uri`**:  The URI of a proxy server to use for connecting to Kiteworks.
*   **`server`**:  The hostname or IP address of your Kiteworks server.

**Prerequisites**

Before using Kitebroker, ensure your Kiteworks system is configured for API use, which typically requires a license that enables API and automation features.

**Installation and Setup**

1.  **License Verification:** Verify your Kiteworks license is enabled for API usage. Log in to the Kiteworks admin UI and navigate to _Application Setup -> Licenses_.
2.  **Kitebroker Application Creation:** Add Kitebroker to the system by navigating to _Application Setup -> Client and Plugins_, selecting the _API_ tab, and clicking "+ Create Custom Application". Configure the following settings:
    *   **Name:** kitebroker
    *   **Description:** kitebroker: API Assistant for Kiteworks
    *   **Flows:** Select either _Signature Authorization_, _JWT_ or _User Credential_.
    *   **Enable Refresh Token:** Enabled (for Signature Authorization and User Credential only.  Not applicable for JWT)
    *   **Signature Key:** You can generate a random key or use an existing one. (For Signature Authorization only)
    *   **Redirect URI:** The default is `https://kitebroker/`, but this can be modified. (For Signature Authorization only)

**Configuration – Kiteworks API Settings**

Aside from auth_flow, all other configuration items are available via `kitebroker --setup`.

The `kitebroker --setup` command can still be used to initially populate or update settings in the `kitebroker.ini` file. This command will prompt you for the necessary information, and then write it to the configuration file.

**Command-Line Usage**

`kitebroker [options]... <command> [parameters]...`

**Available Options:**

*   `--task="task_file.tsk"`: Loads a task file.
*   `--new_task`: Creates a task file template.
*   `--repeat=0s`: Specifies how often to repeat a task (0s = single run).
*   `--setup`: Configures Kiteworks API settings.
*   `--quiet`: Minimal output for non-interactive processes.
*   `--pause`: Pauses after execution.
*   `--auth_token_only`: Returns the generated auth token and exits.
*   `--run_as="user@domain.com"`: Runs the command as a specific user.
*   `--update`: Checks for newer versions of Kitebroker.
*   `--help`: Displays usage information.

**Available Commands:**

*   **User Commands:**
    *   `ls`: Lists folders and/or files in Kiteworks.
    *   `upload`: Uploads folders and/or files to Kiteworks.
    *   `download`: Downloads folders and/or files from Kiteworks.
    *   `push_files`: Pushes files within folders to mobile devices.

*   **Admin Commands:**
    *   `demote_permissions`: Demotes folder permissions from a profile or user.
    *   `csv_onboard`: Adds users to a folder.
    *   `folder_file_expiry`: Modifies the folder and file expiry.
    *   `user_reprofiler`: Changes user profiles.
    *   `file_cleanup`: Removes files from the system older than a specified date.
    *   `user_remover`: Deletes and reassigns inactive accounts.
    *   `kw_to_kw_copy`: Migrates users and files from a remote Kiteworks server.
    *   `move_my_folder`: Relocates folders under _My Folder_.
    *   `add_user_to_folder`: Adds a downloader to top-level folders.
    *   `folder_metadata`: Retrieves folder metadata from a user's folders.
    *   `folder_report`: Provides permission details of folders in Kiteworks.
    *   `update_user`: Updates user information.
    *   `user_renamer`: Renames email accounts with a CSV file.

For detailed help on any command, type `kitebroker <command> --help`.