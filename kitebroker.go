package main

import (
	"fmt"
	"github.com/cmcoffee/go-cfg"
	"github.com/cmcoffee/go-eflag"
	"github.com/cmcoffee/go-kvlite"
	"github.com/cmcoffee/go-nfo"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	DEFAULT_CONF = "kitebroker.cfg"
	NAME         = "kitebroker"
	VERSION      = "0.1.9"
	MAX_RETRY    = 3
	MAX_CALLS    = 5
)

var (
	Config          cfg.Store
	DB              *kvlite.Store
	chunk_size      int
	timeout         time.Duration
	snoop           bool
	server          string
	client_id       string
	client_secret   string
	auth_flow       uint
	retry_count     int
	local_path      string
	first_token_set bool
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

	var server string

	fmt.Println("- kiteworks Secure API Configuration -\n")

RedoSetup:
	if Config.Get("configuration", "server") == NONE {
		server = strings.TrimPrefix(strings.ToLower(get_input("kiteworks Server: ")), "https://")
	}
	client_id := get_input("Client Application ID: ")
	client_secret := get_input("Client Secret Key: ")
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

	errChk(Config.Set("do_not_modify", "api_cfg_0", api_cfg_0))
	errChk(Config.Set("do_not_modify", "api_cfg_1", api_cfg_1))

	errChk(Config.Save("do_not_modify"))
	errChk(Config.Save("configuration"))
}

// Loads kiteworks API client id and secret from config file.
func loadAPIConfig(config_filename string) {

	if !Config.Exists("do_not_modify") {
		nfo.Fatal("Outdated configuration file, please obtain a new config file via https://github.com/cmcoffee/kitebroker/kitebroker.cfg")
	}

	api_cfg_0 := Config.Get("do_not_modify", "api_cfg_0")
	api_cfg_1 := Config.Get("do_not_modify", "api_cfg_1")

	if len(api_cfg_0) < 37 {
		api_setup()
		// Read configuration file.
		errChk(Config.File(config_filename))
		api_cfg_0 = Config.Get("do_not_modify", "api_cfg_0")
		api_cfg_1 = Config.Get("do_not_modify", "api_cfg_1")
	}

	r_key := []byte(api_cfg_0[0:37])
	cs_e := []byte(api_cfg_0[37:])
	s_key := []byte(api_cfg_1 + api_cfg_0[0:37])

	client_id = string(decrypt([]byte(api_cfg_1), r_key))
	client_secret = string(decrypt(cs_e, s_key))
	server = Config.Get("configuration", "server")
}

func main() {
	defer nfo.Exit(0)
	nfo.HideTS()

	var err error

	//nfo.HideTS()
	nfo.Stdout("[ Accellion %s %s ]\n\n", NAME, VERSION)
	nfo.SignalCallback(syscall.SIGINT, func() bool { nfo.Log("Application exit requested."); return true })

	// Initial modifier flags and flag aliases.
	flags := eflag.NewFlagSet(os.Args[0], eflag.ExitOnError)

	config_file := flags.String("config", DEFAULT_CONF, "Specify a configuration file.")
	flags.Alias(&config_file, "config", "f")

	reset := flags.Bool("reset", false, "Clear client credentials.")
	reset_api := flags.Bool("reset_api", false, "Clear server API configuraiton.")

	flags.BoolVar(&snoop, "rest_snoop", false, "Snoop on API calls to the kiteworks appliance.")

	flags.DurationVar(&timeout, "https_timeout", time.Duration(time.Minute), "Timeout for HTTP/S requests to kiteworks server.")

	debug := flags.Bool("debug", false, NONE)

	flags.Parse(os.Args[1:])

	// Enable debug logging if --debug flag given.
	if *debug {
		nfo.SetLoggers(nfo.STD | nfo.DEBUG)
	}

	// Read configuration file.
	errChk(Config.File(*config_file))

	switch strings.ToLower(Config.Get("configuration", "auth_mode")) {
	case "signature":
		auth_flow = SIGNATURE_AUTH
	case "password":
		auth_flow = PASSWORD_AUTH
	default:
		errChk(fmt.Errorf("Unknown auth setting: %s", Config.Get("configuration", "auth_mode")))
	}

	// Prepare our local base path.
	local_path = filepath.Clean(Config.Get("configuration", "local_path"))
	local_path = strings.TrimSuffix(local_path, ".")
	local_path = strings.TrimSuffix(local_path, SLASH)

	// Make our paths if they don't exist.
	errChk(MkDir(Config.Get("configuration", "local_path")))
	errChk(MkDir(Config.Get("configuration", "temp_path")))
	errChk(MkDir(Config.Get("configuration", "log_path")))

	max_connections := MAX_CALLS

	if snoop {
		max_connections = 1
	}

	api_call_bank = make(chan struct{}, max_connections)
	for i := 0; i < max_connections; i++ {
		api_call_bank <- call_done
	}

	log_rotate := uint(Config.GetUint("tweaks", "log_rotate"))
	if log_rotate == 0 {
		log_rotate = 5
	}

	log_size := uint(Config.GetUint("tweaks", "log_size_mb"))
	if log_size == 0 {
		log_size = 20
	}

	_, b := filepath.Split(*config_file)
	basename := strings.Split(b, ".")[0]

	err = nfo.File(nfo.STD, filepath.Clean(fmt.Sprintf("%s/%s.log", Config.Get("configuration", "log_path"), basename)), log_size, log_rotate)
	if err != nil {
		nfo.Fatal(err)
	}

	// Open datastore
	open_database(basename + ".db")

	nfo.Defer(DB.Close)

	// Reset credentials, if requested.
	if *reset {
		nfo.Stdout("This will remove any and all access tokens, credentials will need to be re-entered on next run of kitebroker.")
		nfo.Stdout("\n")
		if confirm := getConfirm("Are you sure you want do this"); confirm {
			errChk(DB.Truncate("tokens"))
			errChk(DB.Unset("kitebroker", "s"))
			Config.Unset("configuration", "account")
			Config.Save("configuration")
			nfo.Notice("Access tokens truncated, including access credentials, please run kitebroker without --reset to set server credentials.")
			nfo.Exit(0)
		}
		nfo.Exit(0)
	}

	if *reset_api {
		nfo.Stdout("This will reset all API credentials and settings:")
		nfo.Stdout("- All access tokens")
		nfo.Stdout("- All credentials")
		nfo.Stdout("- Client Application ID")
		nfo.Stdout("- Client Secret Key")
		nfo.Stdout("\n")
		if confirm := getConfirm("Are you sure you want to do this"); confirm {
			errChk(DB.Truncate("tokens"))
			errChk(DB.Unset("kitebroker", "s"))
			Config.Unset("configuration", "account")
			Config.Unset("do_not_modify", "api_cfg_0")
			Config.Unset("do_not_modify", "api_cfg_1")
			Config.Unset("configuration", "server")
			Config.Save("configuration")
			Config.Save("do_not_modify")
			nfo.Notice("API configuration cleared, account settings cleared, access tokens truncated, please run kitebroker without --reset to configure.")
			nfo.Exit(0)
		}
		nfo.Exit(0)
	}

	// Load API Settings
	loadAPIConfig(*config_file)

	ShowLoader()

	// Get first token.
	user := Session(Config.Get("configuration", "account"))

	_, err = user.MyUser()
	if err == nil {
		first_token_set = true
	} else {
		if KiteError(err, ERR_INVALID_GRANT) {
			if auth_flow != SIGNATURE_AUTH {
				nfo.Err(err)
			}
			DB.Unset("tokens", Config.Get("configuration", "account"))
		}
		if user.ChkToken(err) {
			_, err := user.MyUser()
			if err == nil {
				first_token_set = true
			}
		}
	}

	if !first_token_set {
		nfo.Fatal(err)
	}

	HideLoader()

	var (
		ival       time.Duration
		continuous bool
		ctime      time.Duration
	)

	// Setup continuous scan loop.
	if Config.GetBool("configuration", "continuous_mode") {
		continuous = true
		t := Config.GetUint("configuration", "continuous_rate_secs")
		ival = time.Duration(t) * time.Second
	}

	// start backup cleanup process.
	backgroundCleanup()

	// Begin scan loop.
	for {
		start := time.Now().Round(time.Second)
		TaskHandler()
		if continuous {
			for time.Now().Sub(start) < ival {
				ctime = time.Duration(ival - time.Now().Round(time.Second).Sub(start))
				nfo.Flash("* Rescan will occur in %s", ctime.String())
				if ctime > time.Second {
					time.Sleep(time.Duration(time.Second))
				} else {
					time.Sleep(ctime)
					break
				}
			}
			nfo.Log("Restarting scan cycle ... (%s has elapsed since last run.)\n", time.Now().Round(time.Second).Sub(start).String())
			nfo.Log("\n")
			continue
		} else {
			if atomic.LoadInt32(&cleanup_working) == 1 {
				nfo.Log("Waiting on cleanup process to complete...")
				ShowLoader()
				for {
					time.Sleep(100 * time.Millisecond)
					if atomic.LoadInt32(&cleanup_working) == 0 {
						nfo.Log("Cleanup process complete!")
						break
					}
				}
				HideLoader()
			}
			nfo.Log("Non-continuous total task time: %s.\n", time.Now().Round(time.Second).Sub(start).String())
			break
		}
	}
}
