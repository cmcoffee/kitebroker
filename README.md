## Kitebroker: Automation Assistant for Kiteworks – User Guide

**Introduction**

Kitebroker is a command-line tool and framework designed for automating flows for files and administrative tasks via the Kiteworks API. This guide provides instructions on setting up and using Kitebroker to streamline your Kiteworks operations.

**Prerequisites**

Before using Kitebroker, ensure your Kiteworks system is configured for API use, which typically requires a license that enables API and automation features.

**Installation and Setup**

1.  **License Verification:** Verify your Kiteworks license is enabled for API usage. Log in to the Kiteworks admin UI and navigate to *Application Setup -> Licenses*.

2.  **Kitebroker Application Creation:** Add Kitebroker to the system by navigating to *Application Setup -> Client and Plugins*, selecting the *API* tab, and clicking "+ Create Custom Application". Configure the following settings:
    *   **Name:** kitebroker
    *   **Description:** kitebroker: API Assistant for Kiteworks
    *   **Flows:** Select *Signature Authorization*
    *   **Enable Refresh Token:** Enabled
    *   **Signature Key:** You can generate a random key or use an existing one.
    *   **Redirect URI:** The default is `https://kitebroker/`, but this can be modified.

**Configuration – Kiteworks API Settings**

After creating the application, you must configure Kitebroker with your Kiteworks API credentials. Run the following command in your terminal:

```bash
kitebroker --setup
```

This will prompt you to enter the following information:

1.  **User Account:** `user123@example.com` (or your designated Kiteworks user)
2.  **Kiteworks Host:** `kiteworks.example.com` (or your Kiteworks instance URL)
3.  **Client Application ID:** `a1b2c3d4-e5f6-7890-1234-567890abcdef` (the ID generated when creating the Kitebroker application in Kiteworks)
4.  **Client Secret Key:** (the secret key generated when creating the Kitebroker application in Kiteworks)
5.  **Signature Secret:** (the signature secret associated with your Kitebroker application)
6.  **Redirect URI:** The default is `https://kitebroker/`, but this can be modified.

**Command-Line Usage**

Kitebroker is used via the command line. The basic syntax is:

```bash
kitebroker [options]... <command> [parameters]...
```

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

**User Commands:**

*   `ls`: Lists folders and/or files in Kiteworks.
*   `upload`: Uploads folders and/or files to Kiteworks.
*   `download`: Downloads folders and/or files from Kiteworks.
*   `push_files`: Pushes files within folders to mobile devices.

**Admin Commands:**

*   `demote_permissions`: Demotes folder permissions from a profile or user.
*   `csv_onboard`: Adds users to a folder.
*   `folder_file_expiry`: Modifies the folder and file expiry.
*   `user_reprofiler`: Changes user profiles.
*   `file_cleanup`: Removes files from the system older than a specified date.
*   `user_remover`: Deletes and reassigns inactive accounts.
*   `kw_to_kw_copy`: Migrates users and files from a remote Kiteworks server.
*   `move_my_folder`: Relocates folders under *My Folder*.
*   `add_user_to_folder`: Adds a downloader to top-level folders.
*   `folder_metadata`: Retrieves folder metadata from a user's folders.
*   `folder_report`: Provides permission details of folders in Kiteworks.
*   `update_user`: Updates user information.
*   `user_renamer`: Renames email accounts with a CSV file.

For detailed help on any command, type `kitebroker <command> --help`.
