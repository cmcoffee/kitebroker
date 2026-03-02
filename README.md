## Kitebroker: Automation Assistant for Kiteworks - User Guide

**Introduction**

Kitebroker is a command-line tool and framework designed for automating flows for files and administrative tasks via the Kiteworks API. This guide provides instructions on setting up and using Kitebroker to streamline your Kiteworks operations. Kitebroker supports JSON Web Token (JWT) and OAuth 2.0 Signature Authorization based authentication.

> **Note:** User Credential (password) authentication has been deprecated and is no longer supported. Signature Authorization will be phased out by the end of 2026. **JWT is the recommended authentication method.**

**Configuration**

Kitebroker utilizes a configuration file, `kitebroker.ini`, to store persistent settings. This file allows you to pre-configure your Kiteworks connection details, eliminating the need to repeatedly enter them during each invocation. The `kitebroker.ini` file will be located in the same directory as the Kitebroker executable.

**Example `kitebroker.ini`:**

```ini
[configuration]
user_login =
server = kiteworks.example.com
auth_flow = jwt # (or signature)
redirect_uri = https://kitebroker/
proxy_uri =
ssl_verify = true

[do_not_modify]
api_cfg_0 =
api_cfg_1 =
```

**Configuration Options:**

*   **`user_login`**: The email address of the user account to authenticate with.
*   **`auth_flow`**: Specifies the authentication flow. Valid values are `jwt` (recommended) and `signature`. _(User Credential/password auth has been deprecated. Signature Authorization will be phased out by end of 2026.)_
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
    *   **Flows:** Select _JWT_ (recommended) or _Signature Authorization_.
    *   **Enable Refresh Token:** Enabled (for Signature Authorization only. Not applicable for JWT)
    *   **Signature Key:** You can generate a random key or use an existing one. (For Signature Authorization only)
    *   **Redirect URI:** The default is `https://kitebroker/`, but this can be modified. (For Signature Authorization only)

**Configuration – Kiteworks API Settings**

Aside from auth_flow, all other configuration items are available via `kitebroker --setup`.

The `kitebroker --setup` command can be used to initially populate or update settings in the `kitebroker.ini` file. This command will prompt you for the necessary information, and then write it to the configuration file.

**Command-Line Usage**

`kitebroker [options]... <command> [parameters]...`

**Available Options:**

*   `--task="task_file.tsk"`: Loads a task file. (multi: comma-separated)
*   `--new_task`: Creates a task file template for loading with --task.
*   `--repeat=0s`: How often to repeat task, 0s = single run.
*   `--setup`: Kiteworks API Configuration.
*   `--quiet`: Minimal output for non-interactive processes.
*   `--pause`: Pauses after execution.
*   `--auth_token_only`: Returns the generated auth token, then exits.
*   `--run_as="user@domain.com"`: Runs the command as a specific user.
*   `--update`: Checks for newer version of Kitebroker.
*   `--help`: Displays usage information.

**Available Commands:**

*   **Migration Tasks:**
    *   `box`: Migrate users, folders, files, permissions, comments, and tasks from Box.com to Kiteworks.
    *   `kiteworks`: Migrate users, folders, files, permissions from a remote Kiteworks server.
    *   `quatrix`: Migrate users, folders, files, permissions from Quatrix to Kiteworks.

*   **User Tasks:**
    *   `download`: Download folders and/or files from Kiteworks.
    *   `ls`: List folders and/or files in Kiteworks.
    *   `push_files`: Push files within folders to mobile devices.
    *   `upload`: Upload folders and/or files to Kiteworks.

*   **Admin Tasks (Files & Folders):**
    *   `add_user_to_folder`: Add user as downloader to top-level folders.
    *   `file_cleanup`: Remove files from system older than a specified date.
    *   `demote_permissions`: Demote folder permissions for a profile or user.
    *   `folder_file_expiry`: Modifies the folder and file expiry.
    *   `folder_metadata`: Retrieves folder metadata from user's folders.
    *   `folder_report`: Generate CSV report of folder permissions.

*   **Admin Tasks (Users):**
    *   `csv_onboard`: Add users to folders from CSV.
    *   `move_my_folder`: Move folders from My Folder to top level.
    *   `update_user`: Update user information.
    *   `user_remover`: Delete and reassign inactive accounts.
    *   `user_renamer`: Rename email accounts with CSV.
    *   `user_report`: Generate report of users on system.
    *   `user_reprofiler`: Change user profiles.

For detailed help on any command, type `kitebroker <command> --help`.