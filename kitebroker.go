package main

import (
	"bufio"
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
	VERSION       = "0.0.9"
	KWAPI_VERSION = "5"
	NONE          = ""
)

var (
	Config        cfg.Store
	DB            *kvlite.Store
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
		errChk(DB.CryptSet("tokens", "s", &signature_secret))
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

	// Initial modifier flags and flag aliases.
	flags := eflag.NewFlagSet(os.Args[0], eflag.ExitOnError)

	config_file := flags.String("config", DEFAULT_CONF, "Specify a configuration file.")
	flags.Alias(&config_file, "config", "f")

	default_get_users := "<list of users>"
	default_users_file := "<users list file>"

	get_users := flags.String("users", default_get_users, "[ADMIN ONLY]: Comma seperated user accounts to process kiteworks jobs.")
	flags.Alias(&get_users, "users", "u")

	users_file := flags.String("user_file", default_users_file, "[ADMIN ONLY]: Text file containing user accounts to process kiteworks jobs.")

	reset := flags.Bool("reset", false, "Reconfigure client credentials.")
	reset_all := flags.Bool("reset_all", false, "[ADMIN ONLY]: Reset all kitebroker API settings.")

	flags.BoolVar(&snoop, "snoop", false, "Snoop on API calls to kiteworks appliance.")

	flags.Parse(os.Args[1:])

	// Parse user list.
	var users []string

	if *get_users != default_get_users {
		u := strings.Split(*get_users, ",")
		users = append(users, u[0:]...)
	}

	if *users_file != default_users_file {
		f, err := os.Open(*users_file)
		errChk(err)
		s := bufio.NewScanner(f)
		for s.Scan() {
			users = append(users, s.Text())
		}
	}

	for n, u := range users {
		users[n] = strings.TrimSpace(u)
	}

	// Read configuration file.
	errChk(Config.File(*config_file))

	// Sets configuration defaults
	Config.Defaults(`
[configuration]
auth_mode = signature
ssl_verify = yes
continuous_mode = yes
continuous_rate = 1m
log_path = logs
log_max_size = 10
log_max_rotation = 5
max_connections = 6
temp_path = temp
task = folder_download
api_cfg_0 = 
api_cfg_1 = 
server = 
redirect_uri = https://kitebroker

[folder_download]
local_path = download
kw_folder = My Folder
save_metadata = no
delete_source_files_on_complete = no

[folder_upload]
chunk_megabytes = 32
local_path = upload
kw_folder = My Folder
delete_source_files_on_complete = no

[dli_export]
dli_admin_user = dli_admin_user@domain.com
start_date = 2017-Jan-01
local_path = dli_exports
export_activities = yes
export_emails = yes
export_files = yes
`)

	// Make our paths if they don't exist.
	errChk(MkDir(cleanPath(Config.Get("configuration", "temp_path"))))
	errChk(MkDir(cleanPath(Config.Get("configuration", "log_path"))))

	// Spin up limiters for API calls and file transfers.
	max_connections, err := strconv.Atoi(Config.Get("configuration", "max_connections"))
	if err != nil {
		max_connections = 6
	}

	if snoop {
		max_connections = 1
	}

	api_call_bank = make(chan struct{}, max_connections)
	for i := 0; i < max_connections; i++ {
		api_call_bank <- call_done
	}

	err = logger.File(logger.ALL, cleanPath(fmt.Sprintf("%s/%s.log", Config.Get("configuration", "log_path"), os.Args[0])), 10*1024*1024, 5)
	if err != nil {
		logger.Fatal(err)
	}
	err = logger.File(logger.ERR|logger.FATAL, cleanPath(fmt.Sprintf("%s/%s.err", Config.Get("configuration", "log_path"), os.Args[0])), 10*1024*1024, 5)
	if err != nil {
		logger.Fatal(err)
	}

	logger.Put("[ Accellion %s(v%s) ]\n\n", NAME, VERSION)

	// Generate database based on config file name.
	_, db_filename := filepath.Split(*config_file)
	db_filename = strings.Split(db_filename, ".")[0] + ".db"

	// Open datastore
	open_database(db_filename)
	logger.InTheEnd(DB.Close)

	// Load API Settings
	loadAPIConfig(*config_file)

	if *reset_all {
		logger.Put("The following will be removed:\n")
		logger.Put("* Client Application ID\n")
		logger.Put("* Client Secret Key\n")
		logger.Put("* Any and all existing access tokens\n")
		logger.Put("\nnote: These settings can only be provided via a kiteworks server administrator.\n\n")
		if confirm := getConfirm("Are you sure you want to do this"); confirm {
			Config.Set("configuration", "api_cfg_0", "_")
			Config.Set("configuration", "api_cfg_1", "_")
			errChk(Config.Save("configuration"))
			errChk(DB.Truncate("tokens"))
			logger.Notice("All API Settings have been reset, please run kitebroker without --reset_all to reconfigure kitebroker API settings.\n")
			logger.TheEnd(0)
		} else {
			logger.Put("Aborting, API settings not cleared.\n")
			logger.TheEnd(0)
		}
	}

	// Reset credentials, if requested.
	if *reset {
		logger.Put("This will remove any and all access tokens, credentials will need to be re-entered on next run of kitebroker.\n")
		if confirm := getConfirm("Are you sure you want do this"); confirm {
			errChk(DB.Truncate("tokens"))
			logger.Notice("Access tokens truncated, including access credentials, please run kitebroker without --reset to set server credentials.\n")
			logger.TheEnd(0)	
		}
		logger.Put("Aborting, Access tokens not cleared.\n")
		logger.TheEnd(0)
	}

	var (
		ival       time.Duration
		continuous bool
		ctime      time.Duration
	)

	if strings.ToLower(Config.Get("configuration", "continuous_mode")) == "yes" {
		continuous = true
		ival, err = time.ParseDuration(Config.Get("configuration", "continuous_rate"))
		errChk(err, *config_file, "continuous_rate")
	}

	// Set Global Auth Mode
	switch strings.ToLower(Config.Get("configuration", "auth_mode")) {
	case "signature":
		auth_flow = SIGNATURE_AUTH
		if len(users) == 0 {
			logger.InTheEnd(func() { fmt.Println(NONE) })
			logger.InTheEnd(flags.Usage)
			errChk(fmt.Errorf("When using %s with 'auth_mode = signature', you must specify a user or list of users to run tasks as.", os.Args[0]))
		}
	case "password":
		auth_flow = PASSWORD_AUTH
		user := DB.SGet("tokens", "whoami")
		for {
			if len(user) == 0 {
				DB.Truncate("tokens")
				user = get_input("kiteworks login: ")

			}
			if _, err := Session(user).GetToken(); err != nil {
				fmt.Println(err)
				user = NONE
				continue
			}
			DB.Set("tokens", "whoami", user)
			break
		}
		users = append(users, user)
	default:
		errChk(fmt.Errorf("Unknown auth setting: %s", Config.Get("configuration", "auth_mode")))
	}

	Session(users[0]).GetToken()

	// Begin scan loop.
	for {
		start := time.Now().Round(time.Second)
		TaskHandler(users)
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
