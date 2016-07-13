package main

import (
	"bufio"
	"fmt"
	"github.com/cmcoffee/go-cfg"
	"github.com/cmcoffee/go-eflag"
	"github.com/cmcoffee/go-kvlite"
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
	KWAPI_VERSION = "4.1"
	NONE          = ""
)

var (
	Config     *cfg.Store
	DB         *kvlite.Store
	resp_snoop bool
	call_snoop bool
	server     string
	wg         sync.WaitGroup
)

// Removes newline characters
func cleanInput(input string) (output string) {
	var output_bytes []rune
	for _, v := range input {
		if v == '\n' || v == '\r' {
			continue
		}
		output_bytes = append(output_bytes, v)
	}
	return strings.TrimSpace(string(output_bytes))
}

// Opens local database
func open_database(db_file string) {

	// Provides us the mac address of the first interface.
	get_mac_addr := func() []byte {
		ifaces, err := net.Interfaces()

		if err != nil {
			return nil
		}

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
			errChk(fmt.Errorf("Integrity check failed, please use --reset to revalidate settings."))
		}
		errChk(err)
	}
}

// Performs configuration.
func setup(db_filename string) {

	db, err := kvlite.FastOpen(db_filename, NONE)
	errChk(err)
	errChk(db.CryptReset())
	db.Close()
	open_database(db_filename)

	reader := bufio.NewReader(os.Stdin)

	get_input := func(question string) string {
		for {
			fmt.Printf(question)
			response, _ := reader.ReadString('\n')
			response = cleanInput(response)
			if len(response) > 0 {
				return response
			}
		}
	}

	fmt.Println(`# The following section must be filled in with information obtained by the appliance.
#
# 1. Login to the Admin Web UI.
#
# 2. Go to Application->Client Management->Custom Applications.
#
# 2. Click the Add('+') button, add the following information:
#    Name: kitebroker
#    Description: kitebroker automation tool
#    Flows: Signature Authorization
#    Enable Refresh Token
#    Signature Key: Generate a random key
#    Redirect URI: https://kitebroker
#
# 3. Click Add Application.
#
# 4. Copy the information provided from the appliance to the variables below.
`)

RedoSetup:
	fmt.Println("[ kiteworks Secure API Configuration ]\n")
	server := get_input("Server: ")
	client_id := get_input("Client Application ID: ")
	client_secret := get_input("Client Secret Key: ")
	signature_secret := get_input("Signature Secret: ")

ReConfirm:
	fmt.Printf("Confirm settings [y/n]?: ")
	confirm, _ := reader.ReadString('\n')
	confirm = cleanInput(strings.ToLower(confirm))

	if confirm == "y" || confirm == "yes" {
		errChk(DB.CryptSet("config", "server", server))
		errChk(DB.CryptSet("config", "client_id", client_id))
		errChk(DB.CryptSet("config", "client_secret", client_secret))
		errChk(DB.CryptSet("config", "signature_secret", signature_secret))
	} else if confirm == "n" || confirm == "no" {
		fmt.Println(NONE)
		goto RedoSetup
	} else {
		fmt.Println("Err: Unrecognized response, please try again.")
		goto ReConfirm
	}
}

func main() {
	var err error

	// Initial modifier flags and flag aliases.
	flags := eflag.NewFlagSet(os.Args[0], eflag.ExitOnError)

	config_file := flags.String("file", DEFAULT_CONF, "Specify kitebroker configuration file.")
	flags.Alias(&config_file, "file", "f")

	reset := flags.Bool("reset", false, "Reconfigure client API paramters.")

	flags.BoolVar(&resp_snoop, "resp_snoop", false, NONE)
	flags.BoolVar(&call_snoop, "call_snoop", false, NONE)

	flags.Parse(os.Args[1:])

	// Read configuration file.
	Config, err = cfg.ReadOnly(*config_file)
	errChk(err)

	// Sets configuration defaults
	Config.SetDefaults("kitebroker", map[string][]string{
		"ssl_verify":      {"yes"},
		"continuous_mode": {"yes"},
		"continuous_rate": {"1h"},
		"max_connections": {"6"},
		"max_transfers":   {"3"},
		"redirect_uri":    {"https://kitebroker"},
		"temp_path":       {"temp"},
	})

	errChk(MkDir(cleanPath(Config.SGet("kitebroker", "temp_path"))))

	// Spin up limiters for API calls and file transfers.
	max_connections, err := strconv.Atoi(Config.SGet("kitebroker", "max_connections"))
	if err != nil {
		max_connections = 6
	}

	max_transfers, err := strconv.Atoi(Config.SGet("kitebroker", "max_transfers"))
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

	_, db_filename := filepath.Split(*config_file)

	db_filename = strings.Split(db_filename, ".")[0] + ".db"

	// if this is a --reset, run through setup process.
	if *reset {
		setup(db_filename)
	} else {
		open_database(db_filename)
	}

	// if we can't find any kets in the config table, run first-time setup.
	keys, err := DB.ListKeys("config")
	errChk(err)
	if len(keys) == 0 {
		DB.Close()
		setup(db_filename)
	}

	if _, err := DB.Get("config", "server", &server); err != nil {
		errChk(err)
	}

	var (
		ival       time.Duration
		continuous bool
	)

	if strings.ToLower(Config.SGet("kitebroker", "continuous_mode")) == "yes" {
		continuous = true
		ival, err = time.ParseDuration(Config.SGet("kitebroker", "continuous_rate"))
		errChk(err, *config_file, "continuous_rate")
	}

	// Scan cycle.
	for {
		start := time.Now().Round(time.Second)
		JobHandler()
		if continuous {
			for time.Now().Sub(start) < ival {
				fmt.Printf("\r* Rescan will occur in %s  ", time.Duration(ival-time.Now().Round(time.Second).Sub(start)).String())
				time.Sleep(time.Duration(2 * time.Second))
			}
			fmt.Printf("\r[ Restarting Scan Cycle ... (%s has elapsed since last run.) ]\n", time.Now().Round(time.Second).Sub(start).String())
			continue
		} else {
			fmt.Printf("\n[ Non-continuous tasks total process time: %s ]\n", time.Now().Round(time.Second).Sub(start).String())
			break
		}
	}
}
