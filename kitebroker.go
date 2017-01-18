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
	VERSION       = "0.0.8d"
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

	var (
		server           string
		signature_secret string
	)

	fmt.Println("- kiteworks Secure API Configuration -\n")

RedoSetup:
	if Config.Get("configuration", "server")[0] == NONE {
		server = get_input("kiteworks Server: ")
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

	if auth_flow == SIGNATURE_AUTH {
		errChk(DB.CryptSet("tokens", "s", &signature_secret))
	}

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
	s_key := []byte(api_cfg_1 + api_cfg_0[0:37])

	client_id = string(decrypt([]byte(api_cfg_1), r_key))
	client_secret = string(decrypt(cs_e, s_key))
	server = Config.SGet("configuration", "server")
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

	flags.BoolVar(&resp_snoop, "resp_snoop", false, NONE)
	flags.BoolVar(&call_snoop, "call_snoop", false, NONE)

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
	Config, err = cfg.ReadOnly(*config_file)
	errChk(err)

	// Sets configuration defaults
	Config.SetDefaults("configuration", map[string][]string{
		"ssl_verify":        {"yes"},
		"continuous_mode":   {"yes"},
		"continuous_rate":   {"1h"},
		"max_connections":   {"6"},
		"upload_chunk_size": {"64"},
		"redirect_uri":      {"https://kitebroker"},
		"temp_path":         {"temp"},
		"auth":              {"password"},
		"api_cfg_0":         {NONE},
		"api_cfg_1":         {NONE},
		"log_path":          {"logs"},
		"log_max_size":      {"10"},
		"log_max_rotation":  {"5"},
	})

	// Make our paths if they don't exist.
	errChk(MkDir(cleanPath(Config.SGet("configuration", "temp_path"))))
	errChk(MkDir(cleanPath(Config.SGet("configuration", "local_path"))))
	errChk(MkDir(cleanPath(Config.SGet("configuration", "log_path"))))

	// Spin up limiters for API calls and file transfers.
	max_connections, err := strconv.Atoi(Config.SGet("configuration", "max_connections"))
	if err != nil {
		max_connections = 6
	}

	if resp_snoop || call_snoop {
		max_connections = 1
	}

	api_call_bank = make(chan struct{}, max_connections)
	for i := 0; i < max_connections; i++ {
		api_call_bank <- call_done
	}

	err = logger.File(logger.ALL, cleanPath(fmt.Sprintf("%s/%s.log", Config.SGet("configuration", "log_path"), os.Args[0])), 10*1024*1024, 5)
	if err != nil {
		logger.Fatal(err)
	}
	err = logger.File(logger.ERR|logger.FATAL, cleanPath(fmt.Sprintf("%s/%s.err", Config.SGet("configuration", "log_path"), os.Args[0])), 10*1024*1024, 5)
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

	// Reset credentials, if requested.
	if *reset {
		logger.Notice("Reset server credentials, please re-run without --reset to configure new credentials.")
		errChk(DB.Truncate("tokens"))
		logger.TheEnd(0)
	}

	var (
		ival       time.Duration
		continuous bool
		ctime      time.Duration
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
		if len(users) == 0 {
			logger.InTheEnd(func() { fmt.Println(NONE) })
			logger.InTheEnd(flags.Usage)
			errChk(fmt.Errorf("When using %s with 'auth_mode = signature', you must specify a user or list of users to run tasks as.", os.Args[0]))
		}
		Session(users[0]).GetToken()
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
		errChk(fmt.Errorf("Unknown auth setting: %s", Config.SGet("configuration", "auth_mode")))
	}

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
