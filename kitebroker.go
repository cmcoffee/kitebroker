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
	"sync"
	"time"
)

const (
	DEFAULT_CONF  = "kitebroker.cfg"
	NAME          = "kitebroker"
	VERSION       = "0.0.6a"
	KWAPI_VERSION = "5"
	NONE          = ""
)

var (
	Config        *cfg.Store
	DB            *kvlite.Store
	resp_snoop    bool
	call_snoop    bool
	server        string
	client_id     string
	client_secret string
	auth_flow     uint
	wg            sync.WaitGroup
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
func api_setup(config_filename string) {

	var server string
	
	fmt.Println("- kiteworks Secure API Configuration -\n")

RedoSetup:
	if Config.Get("configuration", "server")[0] == NONE {
		server = get_input("kiteworks Server: ")
	}
	client_id := get_input("Client Application ID: ")
	client_secret := get_input("Client Secret Key: ")
	fmt.Println(NONE)

	for confirm := getConfirm("Confirm API settings"); confirm == false; {
		goto RedoSetup
	}

	fmt.Println(NONE)

	cfg_writer, err := cfg.Load(config_filename)
	errChk(err)

	if server != NONE {
		cfg_writer.Set("configuration", "server", server)
	}

	api_cfg_0 := string(randBytes(37))
	api_cfg_1 := string(encrypt([]byte(client_id), []byte(api_cfg_0)))
	api_cfg_0 = api_cfg_0 + string(encrypt([]byte(client_secret), []byte(api_cfg_1+api_cfg_0)))

	errChk(cfg_writer.Set("configuration", "api_cfg_0", api_cfg_0))
	errChk(cfg_writer.Set("configuration", "api_cfg_1", api_cfg_1))

}

// Loads kiteworks API client id and secret from config file.
func loadAPIConfig(config_filename string) {

	var err error

	api_cfg_0 := Config.SGet("configuration", "api_cfg_0")
	api_cfg_1 := Config.SGet("configuration", "api_cfg_1")

	if len(api_cfg_0) == 0 || len(api_cfg_1) == 0 {
		api_setup(config_filename)
		// Read configuration file.
		Config, err = cfg.ReadOnly(config_filename)
		errChk(err)
		api_cfg_0 = Config.SGet("configuration", "api_cfg_0")
		api_cfg_1 = Config.SGet("configuration", "api_cfg_1")
	}

	r_key := []byte(api_cfg_0[0:37])
	cs_e := []byte(api_cfg_0[37:])
	s_key := []byte(api_cfg_1+api_cfg_0[0:37])

	client_id = string(decrypt([]byte(api_cfg_1), r_key))
	client_secret = string(decrypt(cs_e, s_key))
	server = Config.SGet("configuration", "server")
}

func main() {
	var err error

	// Initial modifier flags and flag aliases.
	flags := eflag.NewFlagSet(os.Args[0], eflag.ExitOnError)

	config_file := flags.String("file", DEFAULT_CONF, "Specify a configuration file.")
	flags.Alias(&config_file, "file", "f")

	reset := flags.Bool("reset", false, "Reconfigure client credentials.")

	flags.BoolVar(&resp_snoop, "resp_snoop", false, NONE)
	flags.BoolVar(&call_snoop, "call_snoop", false, NONE)

	flags.Parse(os.Args[1:])

	// Read configuration file.
	Config, err = cfg.ReadOnly(*config_file)
	errChk(err)

	// Sets configuration defaults
	Config.SetDefaults("configuration", map[string][]string{
		"ssl_verify":      {"yes"},
		"continuous_mode": {"yes"},
		"continuous_rate": {"1h"},
		"max_connections": {"6"},
		"max_transfers":   {"3"},
		"redirect_uri":    {"https://kitebroker"},
		"temp_path":       {"temp"},
		"auth":			   {"password"},
		"api_cfg_0": 	   {NONE},
		"api_cfg_1": 	   {NONE},
	})

	// Make our paths if they don't exist.
	errChk(MkDir(cleanPath(Config.SGet("configuration", "temp_path"))))
	errChk(MkDir(cleanPath(Config.SGet("configuration", "local_path"))))


	// Spin up limiters for API calls and file transfers.
	max_connections, err := strconv.Atoi(Config.SGet("configuration", "max_connections"))
	if err != nil {
		max_connections = 6
	}

	max_transfers, err := strconv.Atoi(Config.SGet("configuration", "max_transfers"))
	if err != nil {
		max_transfers = 3
	}

	transfer_call_bank = make(chan struct{}, max_transfers)
	for i := 0; i < max_transfers; i++ {
		transfer_call_bank <- call_done
	}

	api_call_bank = make(chan struct{}, max_connections)
	for i := 0; i < max_connections; i++ {
		api_call_bank <- call_done
	}

	fmt.Printf("[ Accellion %s(v%s) ]\n\n", NAME, VERSION)

	// Generate database based on config file name.
	_, db_filename := filepath.Split(*config_file)
	db_filename = strings.Split(db_filename, ".")[0] + ".db"

	// Load API Settings
	loadAPIConfig(*config_file)

	// Open datastore
	open_database(db_filename)
	logger.InTheEnd(DB.Close)
	logger.InTheEnd(func() { fmt.Println("") })

	// Reset credentials, if requested.
	if *reset {
		errChk(DB.Truncate("tokens"))
	}

	var (
		ival       time.Duration
		continuous bool
		ctime time.Duration
	)

	if strings.ToLower(Config.SGet("configuration", "continuous_mode")) == "yes" {
		continuous = true
		ival, err = time.ParseDuration(Config.SGet("configuration", "continuous_rate"))
		errChk(err, *config_file, "continuous_rate")
	}

	// Set Global Auth Mode
	switch strings.ToLower(Config.SGet("configuration", "auth_mode")) {
		case "signature":
			auth_flow = SIGNATURE_AUTH
			if len(flags.Args()) == 0 {
				errChk(fmt.Errorf("When using %s with 'auth_mode = signature', you must specify a user or list of users to run tasks as.. (ie.. %s usera@domain.com, userb@domain.com)", os.Args[0], os.Args[0]))
			}
		case "password":
			auth_flow = PASSWORD_AUTH
			if len(DB.SGet("tokens", "whoami")) == 0 {
				DB.Truncate("tokens")
			}
		default:
			errChk(fmt.Errorf("Unknown auth setting: %s", Config.SGet("configuration", "auth_mode")))
	}

	// Begin scan loop.
	for {
		start := time.Now().Round(time.Second)
		JobHandler(flags.Args())
		if continuous {
			for time.Now().Sub(start) < ival {
				ctime = time.Duration(ival-time.Now().Round(time.Second).Sub(start))
				fmt.Printf("\r* Rescan will occur in %s  ", ctime.String())
				if ctime > time.Second {
					time.Sleep(time.Duration(time.Second))
				} else { 
					time.Sleep(ctime)
					break
				}
			}
			fmt.Printf("\r")
			logger.Log("Restarting Scan Cycle ... (%s has elapsed since last run.)\n", time.Now().Round(time.Second).Sub(start).String())
			continue
		} else {
			logger.Log("\nNon-continuous tasks total process time: %s.\n", time.Now().Round(time.Second).Sub(start).String())
			break
		}
	}
}
