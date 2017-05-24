package main

import (
	"fmt"
	"github.com/cmcoffee/go-cfg"
	"github.com/cmcoffee/go-eflag"
	"github.com/cmcoffee/go-kvlite"
	"github.com/cmcoffee/go-logger"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DEFAULT_CONF  = "kitebroker.cfg"
	NAME          = "kitebroker"
	VERSION       = "0.1.0"
	KWAPI_VERSION = "5"
)

var (
	Config        cfg.Store
	DB            *kvlite.Store
	chunk_size    int
	snoop         bool
	server        string
	client_id     string
	client_secret string
	auth_flow     uint
	retry_count   int
)

// Authentication Mechnism Constants
const (
	SIGNATURE_AUTH = iota
	PASSWORD_AUTH
)

// Opens local database
func open_database(db_file string) {

	// Provides us the mac address of the first interface.
	get_mac_addr := func() []byte {
		ifaces, err := net.Interfaces()
		errChk(err)

		for _, v := range ifaces {
			if len(v.HardwareAddr) == 0 {
				continue
			}
			return v.HardwareAddr
		}
		return nil
	}

	var err error

	// Open kitebroker sqlite database.
	DB, err = kvlite.Open(db_file, get_mac_addr())
	if err != nil {
		if err == kvlite.ErrBadPadlock {
			db, err := kvlite.FastOpen(db_file, NONE)
			errChk(err)
			errChk(db.CryptReset())
			errChk(db.Close())
			DB, err = kvlite.Open(db_file, get_mac_addr())
			errChk(err)
		}
		errChk(err)
	}
}

// Performs configuration.
func api_setup() {

	var (
		server           string
		signature_secret string
	)

	fmt.Println("- kiteworks Secure API Configuration -\n")

RedoSetup:
	if Config.Get("configuration", "server") == NONE {
		server = strings.TrimPrefix(strings.ToLower(get_input("kiteworks Server: ")), "https://")
	}
	client_id := get_input("Client Application ID: ")
	client_secret := get_input("Client Secret Key: ")
	if auth_flow == SIGNATURE_AUTH {
		signature_secret = get_input("Signature Secret: ")
	}
	fmt.Println(NONE)

	for confirm := getConfirm("Confirm API settings"); confirm == false; {
		goto RedoSetup
	}

	fmt.Println(NONE)

	if server != NONE {
		Config.Set("configuration", "server", server)
	}

	api_cfg_0 := string(randBytes(37))
	api_cfg_1 := string(encrypt([]byte(client_id), []byte(api_cfg_0)))
	api_cfg_0 = api_cfg_0 + string(encrypt([]byte(client_secret), []byte(api_cfg_1+api_cfg_0)))

	errChk(Config.Set("configuration", "api_cfg_0", api_cfg_0))
	errChk(Config.Set("configuration", "api_cfg_1", api_cfg_1))

	errChk(Config.Save("configuration"))

	if auth_flow == SIGNATURE_AUTH {
		errChk(DB.CryptSet("kitebroker", "s", &signature_secret))
	}

}

// Loads kiteworks API client id and secret from config file.
func loadAPIConfig(config_filename string) {

	api_cfg_0 := Config.Get("configuration", "api_cfg_0")
	api_cfg_1 := Config.Get("configuration", "api_cfg_1")

	if len(api_cfg_0) < 37 {
		api_setup()
		// Read configuration file.
		errChk(Config.File(config_filename))
		api_cfg_0 = Config.Get("configuration", "api_cfg_0")
		api_cfg_1 = Config.Get("configuration", "api_cfg_1")
	}

	r_key := []byte(api_cfg_0[0:37])
	cs_e := []byte(api_cfg_0[37:])
	s_key := []byte(api_cfg_1 + api_cfg_0[0:37])

	client_id = string(decrypt([]byte(api_cfg_1), r_key))
	client_secret = string(decrypt(cs_e, s_key))
	server = Config.Get("configuration", "server")
}

func main() {
	var err error

	logger.Put("[ Accellion %s(v%s) ]\n\n", NAME, VERSION)

	// Initial modifier flags and flag aliases.
	flags := eflag.NewFlagSet(os.Args[0], eflag.ExitOnError)

	config_file := flags.String("config", DEFAULT_CONF, "Specify a configuration file.")
	flags.Alias(&config_file, "config", "f")

	reset := flags.Bool("reset", false, "Reconfigure client credentials.")

	flags.BoolVar(&snoop, "resp_snoop", false, "Snoop API calls to and from kiteworks appliance.")

	flags.Parse(os.Args[1:])

	ShowLoader()

	// Read configuration file.
	errChk(Config.File(*config_file))

	// Sets configuration defaults
	Config.Defaults(`
[configuration]
# kiteworks API Configuration ##############################
server = 
account =
auth_mode = signature
redirect_uri = https://kitebroker

# Auto-Generated Config ####################################
api_cfg_0 =
api_cfg_1 =
############################################################

# Verify SSL Certificate on Appliance. (improves security)
ssl_verify = yes

# Continuous Mode, run indefinetly.
continuous_mode = yes
continuous_rate_secs = 1800

# Logging settings
log_path = log  	# Logging path
log_size = 10240 	# Logging max size in bytes before rotation.
log_rotate = 5		# Logging max rotations to save, -1 = rotate indefinetly.

# DB Cleanup interval.
cleanup_time_secs = 86400

# Temp folder for incomplete file downloads.
temp_path = temp

# Local path for file upload and download.
local_path = kiteworks

# Upload chunk size in bytes.
upload_chunk_size = 32768

# Removed source copy of file from either local machine(when uploading) or kiteworks appliance(when downloading).
delete_source_files_on_complete = no

# Task Types:
# send_file      :Emails files to user or users.
# recv_file      :Downloads files sent to user.
# folder_download :Download a specific remote folder.
# folder_upload   :Upload files to a specific folder.
# dli_export      :Creates accounts based on CSV input. (Requires Signature Auth)
task = folder_upload

[send_file:opts]
mailto =
mailcc =
mailbcc =
get_recipient_from_path = no
include_subdirs = yes
exclude_filter =
email_subject = default

[recv_file:opts]
email_age_days = 0
download_mailbody = no

[folder_download:opts]
kw_folder = My Folder 	# Folder(s) to download, leave blank for all folders.
save_metadata = no		# Saves metadata for downloaded files as <file>-info.

[folder_upload:opts]
kw_folder = My Folder  	# Parent folder to upload files to, leave blank to upload all folders.

[dli_export:opts]
dli_admin_user = dli_admin@domain.com  # DLI Admin account, required for DLI exports.
start_date = 2017-Jan-01   			   # Start date for beginign of DLI export.

# Specify which type of exports to process.
export_activities = yes
export_emails = yes
export_files = yes
`)

	// Make our paths if they don't exist.
	errChk(MkDir(cleanPath(Config.Get("configuration", "temp_path"))))
	errChk(MkDir(cleanPath(Config.Get("configuration", "log_path"))))

	max_connections := 3

	if snoop {
		max_connections = 1
	}

	api_call_bank = make(chan struct{}, max_connections)
	for i := 0; i < max_connections; i++ {
		api_call_bank <- call_done
	}

	log_rotate, err := strconv.Atoi(Config.Get("configuration", "log_rotate"))
	if err != nil {
		logger.Warn("Could not parse log_rotate, defaulting to log_rotate of 5.")
		log_rotate = 10
	}

	log_size, err := strconv.Atoi(Config.Get("configuration", "log_size"))
	if err != nil {
		logger.Warn("Could not parse log_size, defaulting to log_size of 10240 kilobytes.")
		log_size = 10240
	}

	err = logger.File(logger.ALL, cleanPath(fmt.Sprintf("%s/%s.log", Config.Get("configuration", "log_path"), os.Args[0])), int64(log_size)*1024, log_rotate)
	if err != nil {
		logger.Fatal(err)
	}

	// Generate database based on config file name.
	_, db_filename := filepath.Split(*config_file)
	db_filename = strings.Split(db_filename, ".")[0] + ".db"

	// Open datastore
	open_database(db_filename)
	logger.InTheEnd(DB.Close)

	switch strings.ToLower(Config.Get("configuration", "auth_mode")) {
		case "signature":
			auth_flow = SIGNATURE_AUTH
		case "password":
			auth_flow = PASSWORD_AUTH
		default:
			errChk(fmt.Errorf("Unknown auth setting: %s", Config.Get("configuration", "auth_mode")))
	}

	// Load API Settings
	loadAPIConfig(*config_file)

	// Reset credentials, if requested.
	if *reset {
		logger.Put("This will remove any and all access tokens, credentials will need to be re-entered on next run of kitebroker.\n")
		if confirm := getConfirm("Are you sure you want do this"); confirm {
			errChk(DB.Truncate("tokens"))
			errChk(DB.Unset("kitebroker", "s"))
			Config.Unset("configuration", "account")
			Config.Save("configuration")
			logger.Notice("Access tokens truncated, including access credentials, please run kitebroker without --reset to set server credentials.\n")
			logger.TheEnd(0)	
		}
		logger.Put("Aborting, Access tokens not cleared.\n")
		logger.TheEnd(0)
	}

	ShowLoader()
	for {
		if _, err := Session(Config.Get("configuration", "account")).GetToken(); err != nil {
			if _, err := Session(Config.Get("configuration", "account")).GetToken(); err != nil {
				logger.Err(err)
				fmt.Printf("\n")
				DB.Unset("kitebroker", "s")
				continue
			}	
		}
		break
	}
	HideLoader()

	var (
		ival       time.Duration
		continuous bool
		ctime      time.Duration
	)

	// Setup continuous scan loop.
	if strings.ToLower(Config.Get("configuration", "continuous_mode")) == "yes" {
		continuous = true
		t, err := strconv.Atoi(Config.Get("configuration", "continuous_rate_secs"))
		if err != nil {
			logger.Warn("Could not parse continous_rate, defaulting to 1800 seconds.")
			t = 1800
		}

		ival = time.Duration(t) * time.Second
	}

	// Begin scan loop.
	for {
		//backgroundCleanup();
		start := time.Now().Round(time.Second)
		TaskHandler()
		if continuous {
			for time.Now().Sub(start) < ival {
				ctime = time.Duration(ival - time.Now().Round(time.Second).Sub(start))
				logger.Put(fmt.Sprintf("* Rescan will occur in %s", ctime.String()))
				if ctime > time.Second {
					time.Sleep(time.Duration(time.Second))
				} else {
					time.Sleep(ctime)
					break
				}
			}
			logger.Log("Restarting scan cycle ... (%s has elapsed since last run.)\n", time.Now().Round(time.Second).Sub(start).String())
			logger.Log("\n")
			continue
		} else {
			logger.Log("Non-continuous total task time: %s.\n", time.Now().Round(time.Second).Sub(start).String())
			break
		}
	}
}
