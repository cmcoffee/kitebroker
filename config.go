package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"github.com/cmcoffee/snugforge/nfo"
	"github.com/cmcoffee/snugforge/options"
	. "kitebroker/core"
	"net"
	"os"
	"strings"
	"time"
)

// Wrapper for config items stored in database
type dbCFG struct{}

// Holds database configuration settings.
var dbConfig dbCFG

// user retrieves the configured username.
func (d dbCFG) user() (account string) {
	global.db.Get("kitebroker", "account", &account)
	return
}

// Sets the user account in the database.
//
// account: The user account to set.
func (d dbCFG) set_user(account string) {
	global.db.Set("kitebroker", "account", &account)
}

// max_api_calls returns the maximum allowed API calls.
// Returns 3 if the value is not found in the database.
func (d dbCFG) max_api_calls() (max int) {
	found := global.db.Get("kitebroker", "max_api_calls", &max)
	if !found {
		return 3
	}
	return
}

// Sets the maximum number of API calls allowed.
// max: The maximum number of API calls to set.
func (d dbCFG) set_max_api_calls(max int) {
	global.db.Set("kitebroker", "max_api_calls", &max)
}

// max_file_transfer retrieves the maximum file transfer size in bytes.
// If not found in the database, it returns a default value of 3.
func (d dbCFG) max_file_transfer() (max int) {
	found := global.db.Get("kitebroker", "max_file_transfer", &max)
	if !found {
		return 3
	}
	return
}

// Sets the maximum file transfer size in bytes.
// max: The maximum file transfer size to set.
func (d dbCFG) set_max_file_transfer(max int) {
	global.db.Set("kitebroker", "max_file_transfer", &max)
}

// Sets the connection timeout in seconds.
// max: The maximum connection timeout in seconds.
func (d dbCFG) set_connect_timeout_secs(max int) {
	global.db.Set("kitebroker", "connect_timeout_secs", &max)
}

// connect_timeout_secs returns the connection timeout in seconds.
// If not found in the database, it returns a default value of 12.
func (d dbCFG) connect_timeout_secs() (max int) {
	found := global.db.Get("kitebroker", "connect_timeout_secs", &max)
	if !found {
		return 12
	}
	return
}

// Sets the request timeout in seconds.
// max: The maximum request timeout in seconds.
func (d dbCFG) set_request_timeout_secs(max int) {
	global.db.Set("kitebroker", "request_timeout_secs", &max)
}

// request_timeout_secs returns the request timeout in seconds.
// Returns 60 if the value is not found in the database.
func (d dbCFG) request_timeout_secs() (max int) {
	found := global.db.Get("kitebroker", "request_timeout_secs", &max)
	if !found {
		return 60
	}
	return
}

// Sets the chunk size in megabytes for file transfers.
// max: The maximum chunk size in megabytes to set.
func (d dbCFG) set_chunk_size_mb(max int) {
	global.db.Set("kitebroker", "chunk_size_mb", &max)
}

// chunk_size_mb returns the configured chunk size in MB.
// Returns 65 if not found in the database.
func (d dbCFG) chunk_size_mb() (max int) {
	found := global.db.Get("kitebroker", "chunk_size_mb", &max)
	if !found {
		return 65
	}
	return
}

// init_kw_api initializes the Kite Connect API.
// It authenticates with the server using configured credentials.
func init_kw_api() {
	if global.kw != nil {
		return
	}

	if !global.cfg.Exists("do_not_modify") {
		Fatal("Outdated configuration file, please obtain a new config file via https://github.com/cmcoffee/kitebroker/kitebroker.cfg")
	}
	config_api(false, false)
	Flash("[%s]: Authenticating, please wait...", global.kw.Server)
	username := dbConfig.user()
	if global.as_user != NONE {
		username = global.as_user
	}
	user, err := global.kw.Login(username)
	if err != nil {
		if !IsAPIError(err) {
			Fatal(err)
		} else {
			if global.as_user != NONE {
				Fatal(err)
			}
		}
		Err(err)
		Log("\n")
		config_api(true, true)
		os.Exit(1)
	}
	global.user = *user
	if global.auth_mode != SIGNATURE_AUTH {
		dbConfig.set_user(string(user.Username))
	} else if global.as_user != NONE {
		global.kw.AgentString = fmt.Sprintf("%s/%s (%s)", APPNAME, VERSION, dbConfig.user())
	}
	init_logging()
}

// set_api_configs sets API configurations using client ID and secret.
// It encrypts the client ID and secret and stores them in global config.
func set_api_configs(client_id, client_secret string) {
	api_cfg_0 := string(RandBytes(34))
	api_cfg_1 := string(encrypt([]byte(client_id), []byte(api_cfg_0)))
	api_cfg_0 = api_cfg_0 + string(encrypt([]byte(client_secret), []byte(api_cfg_1+api_cfg_0)))

	Critical(global.cfg.Set("do_not_modify", "api_cfg_0", api_cfg_0))
	Critical(global.cfg.Set("do_not_modify", "api_cfg_1", api_cfg_1))
}

// load_api_configs loads the application ID and secret from configuration.
// It decrypts the values using AES encryption with keys derived from config.
// Returns the application ID and secret as strings; returns empty strings
// if configuration is invalid or decryption fails.
func load_api_configs() (app_id, app_secret string) {
	api_cfg_0 := global.cfg.Get("do_not_modify", "api_cfg_0")
	api_cfg_1 := global.cfg.Get("do_not_modify", "api_cfg_1")

	if len(api_cfg_0) < 34 {
		return NONE, NONE
	}

	r_key := []byte(api_cfg_0[0:34])
	cs_e := []byte(api_cfg_0[34:])
	s_key := []byte(api_cfg_1 + api_cfg_0[0:34])

	return string(decrypt([]byte(api_cfg_1), r_key)), string(decrypt(cs_e, s_key))
}

// default_config_file is the default configuration file content.
const default_config_file = `
[configuration]
user_login = 
server =
auth_flow = signature
redirect_uri = https://kitebroker/

# Proxy server in URI format. (ie.. https://proxy.com:3128)
proxy_uri =

# Verify SSL Certificate on Appliance. (improves security)
ssl_verify = true

#### Autogenerated Config Area Below Here. (Do not modify!) #####
[do_not_modify]
api_cfg_0 = 
api_cfg_1 = 
`

// load_config_defaults loads the default configuration values.
// It uses the global configuration store to load from a default file.
// Returns an error if loading fails.
func load_config_defaults() (err error) {
	return global.cfg.Defaults(default_config_file)
}

// init_database initializes the application database.
// It creates the data directory, opens the database file,
// and sets up error and cache tables.
func init_database() {
	var err error

	MkDir(fmt.Sprintf("%s/data/", global.root))

	db_filename := FormatPath(fmt.Sprintf("%s/data/%s.db", global.root, APPNAME))
	global.db, err = SecureDatabase(db_filename)
	Critical(err)
	SetErrTable(global.db.Table("kitebroker_errors"))
	global.cache = global.db.Sub("cache")
	//Defer(global.db.Close)
}

// get_ get_mac_addr returns the MAC address of the first network interface.
// It returns nil if no MAC address is found.```go
// get_mac_addr retrieves the MAC address of the network interface.
// It returns the MAC address as a byte slice, or nil if not found.
func get_mac_addr() []byte {
	ifaces, err := net.Interfaces()
	Critical(err)

	for _, v := range ifaces {
		if len(v.HardwareAddr) == 0 {
			continue
		}
		return v.HardwareAddr
	}
	return nil
}

// SecureDatabase opens a database file, handling potential decryption
// or reset if hardware changes are detected.
func SecureDatabase(file string) (Database, error) {
	// Provides us the mac address of the first interface.
	db, err := OpenDB(file, _unlock_db()[0:]...)
	if err != nil {
		if err == ErrBadPadlock {
			Notice("Hardware changes detected, you will need to reauthenticate.")
			if err := ResetDB(file); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
		db, err = OpenDB(file, _unlock_db()[0:]...)
		if err != nil {
			return nil, err
		}
	}
	return db, nil
}

// init_logging initializes the logging system with configured settings.
// It sets up log file, output destinations, and levels based on
// global configuration flags like debug, snoop, and sysmode.
func init_logging() {
	file, err := nfo.LogFile(FormatPath(fmt.Sprintf("%s/logs/%s.log", global.root, APPNAME)), 10, 10)
	Critical(err)
	nfo.SetFile(nfo.STD|nfo.AUX, file)
	if global.sysmode {
		nfo.SetOutput(nfo.STD, os.Stderr)
		nfo.SetOutput(nfo.INFO, os.Stdout)
		nfo.SetOutput(nfo.AUX|nfo.WARN|nfo.NOTICE, nfo.None)
	}
	if global.debug {
		nfo.SetOutput(nfo.DEBUG, os.Stderr)
		nfo.SetFile(nfo.DEBUG, nfo.GetFile(nfo.ERROR))
	}
	if global.snoop {
		nfo.SetOutput(nfo.TRACE, os.Stderr)
		nfo.SetFile(nfo.TRACE, nfo.GetFile(nfo.ERROR))
	}
}

// hashBytes computes the SHA256 hash of the input values.
// It accepts a variable number of interface{} arguments and
// returns a []byte representing the SHA256 hash.
func hashBytes(input ...interface{}) []byte {
	var combine []string
	for _, v := range input {
		if x, ok := v.([]byte); ok {
			v = string(x)
		}
		combine = append(combine, fmt.Sprintf("%v", v))
	}
	sum := sha256.Sum256([]byte(strings.Join(combine[0:], NONE)))
	var output []byte
	output = append(output[0:], sum[0:]...)
	return output
}

// encrypt encrypts the input data using AES encryption with CFB mode.
// It takes the input data and a key as byte slices and returns the
// base64 encoded encrypted data.
func encrypt(input []byte, key []byte) []byte {

	var block cipher.Block

	key = hashBytes(key)
	block, _ = aes.NewCipher(key)

	buff := make([]byte, len(input))
	copy(buff, input)

	cipher.NewCFBEncrypter(block, key[0:block.BlockSize()]).XORKeyStream(buff, buff)

	return []byte(base64.RawStdEncoding.EncodeToString(buff))
}

// decrypt decrypts a base64 encoded ciphertext using AES-CFB.
// It takes the input ciphertext and key as byte slices and
// returns the decrypted byte slice.
func decrypt(input []byte, key []byte) (decoded []byte) {

	var block cipher.Block

	key = hashBytes(key)

	decoded, _ = base64.RawStdEncoding.DecodeString(string(input))
	block, _ = aes.NewCipher(key)
	cipher.NewCFBDecrypter(block, key[0:block.BlockSize()]).XORKeyStream(decoded, decoded)

	return
}

// _db_lock_status checks if database locking is enabled.
// It returns false if the 'db_locker' config is set, true otherwise.
func _db_lock_status() bool {
	if v := global.cfg.Get("do_not_modify", "db_locker"); len(v) > 0 {
		return false
	}
	return true
}

// _set_db_locker sets a database locker to prevent multiple instances.
// It retrieves or generates a unique code and stores it in the config.
func _set_db_locker() {
	if v := global.cfg.Get("do_not_modify", "db_locker"); len(v) > 0 {
		global.cfg.Unset("do_not_modify", "db_locker")
		return
	} else {
		mac := get_mac_addr()
		random := RandBytes(40)
		db_lock_code := string(encrypt(mac, random))
		Critical(global.cfg.Set("do_not_modify", "db_locker", fmt.Sprintf("%s%s", string(random), db_lock_code)))
	}
	return
}

// _unlock_db decrypts or generates a database padlock.
// It retrieves the padlock from configuration or generates
// a new one based on the MAC address if not configured.
func _unlock_db() (padlock []byte) {
	if dbs := global.cfg.Get("do_not_modify", "db_locker"); len(dbs) > 0 {
		code := []byte(dbs[0:40])
		db_lock_code := []byte(dbs[40:])
		padlock = decrypt(db_lock_code, code)
	} else {
		padlock = get_mac_addr()
	}
	return
}

// proxyValue holds a description and a value, typically for proxy settings.
type proxyValue struct {
	desc  string
	value string
}

// Set prompts the user for input and updates the value if changed.
func (c *proxyValue) Set() bool {
	v := c.value
	c.value = nfo.GetInput(fmt.Sprintf(`
# Format of proxy server should be: https://proxy.server.com:3127
# Leave blank for direct connection/no proxy.
--> %s: `, c.desc))
	return c.value != v
}

// Get returns the underlying value associated with the proxy setting.
func (c *proxyValue) Get() interface{} {
	return c.value
}

// String returns a string representation of the proxy value.
func (c *proxyValue) String() string {
	if IsBlank(c.value) {
		return fmt.Sprintf("%s:\t(Direct Connection/No Proxy)", c.desc)
	} else {
		return fmt.Sprintf("%s:\t%s", c.desc, c.value)
	}
}

// pause prints a message and waits for user input.
func pause() {
	nfo.PressEnter("\n(press enter to continue)")
}

// config_api configures and tests the KiteWork API connection.
// It allows users to set server details, credentials, and other options.
// It can also test the configuration to ensure successful communication.
func config_api(configure_api, test_required bool) {

	var bad_test bool

	setup := options.NewOptions("--- kiteworks API configuration ---", "(selection or 'q' to save & exit)", 'q')
	client_app_id, client_app_secret := load_api_configs()
	redirect_uri := global.cfg.Get("configuration", "redirect_uri")
	proxy_uri := global.cfg.Get("configuration", "proxy_uri")
	account := dbConfig.user()

	var signature string
	global.db.Get("kitebroker", "signature", &signature)

	if global.auth_mode == SIGNATURE_AUTH {
		setup.StringVar(&account, "User Account", account, "Please provide e-mail address of user account.")
	}

	server := setup.String("kiteworks Host", global.cfg.Get("configuration", "server"), "Please provide the kiteworks appliance hostname. (ie.. kiteworks.domain.com)", false)

	setup.StringVar(&client_app_id, "Client Application ID", client_app_id, NONE)
	setup.SecretVar(&client_app_secret, "Client Secret Key", client_app_secret, NONE)

	if global.auth_mode == SIGNATURE_AUTH {
		setup.SecretVar(&signature, "Signature Secret", signature, NONE)
	} else {
		setup.Func("Reset user credentials", func() bool {
			kw := new(APIClient)
			kw.SetDatabase(global.db.Sub("KWAPI"))
			kw.TokenStore.Delete(account)
			account = NONE
			dbConfig.set_user(account)
			Notice("User account has been reset, you will be prompted for credentials at next run/API test.")
			pause()
			return false
		})
	}

	if IsBlank(redirect_uri) || global.auth_mode == SIGNATURE_AUTH {
		setup.StringVar(&redirect_uri, "Redirect URI", redirect_uri, "Redirect URI should simply match setting in kiteworks admin, default: https://kitebroker")
	}

	ssl_verify := setup.Bool("Verify SSL", global.cfg.GetBool("configuration", "ssl_verify"))
	proxy := proxyValue{"Proxy Server", proxy_uri}
	setup.Register(&proxy)

	advanced := options.NewOptions(NONE, "(selection or 'q' to return to previous)", 'q')
	connect_timeout_secs := advanced.Int("Connection timeout seconds", dbConfig.connect_timeout_secs(), "Default Value: 12", 0, 600)
	request_timeout_secs := advanced.Int("Request timeout seconds", dbConfig.request_timeout_secs(), "Default Value: 60", 0, 600)
	max_api_calls := advanced.Int("Maximum API Calls", dbConfig.max_api_calls(), "Default Value: 3", 1, 5)
	max_file_transfer := advanced.Int("Maximum file transfers", dbConfig.max_file_transfer(), "Default Value: 3", 1, 10)
	chunk_size_mb := advanced.Int("Chunk size in megabytes", dbConfig.chunk_size_mb(), "Default Value: 65", 1, 65)
	lock_db := advanced.Bool("Machine Locked", _db_lock_status())

	setup.Options("Advanced", advanced, false)

	//Saves current coneciguration.
	save_config := func() {
		Critical(global.cfg.Set("configuration", "redirect_uri", redirect_uri))
		Critical(global.cfg.Set("configuration", "proxy_uri", proxy.Get().(string)))
		Critical(global.cfg.Set("configuration", "server", strings.TrimPrefix(strings.ToLower(*server), "https://")))
		Critical(global.cfg.Set("configuration", "ssl_verify", *ssl_verify))
		set_api_configs(client_app_id, client_app_secret)
		if global.auth_mode == SIGNATURE_AUTH && !IsBlank(signature) {
			global.db.CryptSet("kitebroker", "signature", &signature)
		}
		dbConfig.set_user(account)
		dbConfig.set_connect_timeout_secs(*connect_timeout_secs)
		dbConfig.set_request_timeout_secs(*request_timeout_secs)
		dbConfig.set_max_api_calls(*max_api_calls)
		dbConfig.set_max_file_transfer(*max_file_transfer)
		dbConfig.set_chunk_size_mb(*chunk_size_mb)
		if _db_lock_status() != *lock_db {
			_set_db_locker()
		}
		Critical(global.cfg.TrimSave())

	}

	var authenticated bool

	setup_account := func() string {
		if IsBlank(account) {
			auth, err := global.kw.Login(NONE)
			if err != nil {
				Fatal(err)
			}
			global.user = *auth
			dbConfig.set_user(global.user.Username)
		} else {
			global.user = global.kw.Session(account)
		}
		authenticated = true
		return global.user.Username
	}

	// Loads API Configuration
	load_api := func() bool {
		if IsBlank(*server, redirect_uri, client_app_id, client_app_secret) || (global.auth_mode == SIGNATURE_AUTH && IsBlank(signature)) || (global.auth_mode == SIGNATURE_AUTH && IsBlank(account)) {
			return false
		}
		kw := &KWAPI{APIClient: new(APIClient)}
		kw.Server = strings.TrimPrefix(strings.ToLower(*server), "https://")
		if global.auth_mode == SIGNATURE_AUTH {
			kw.Signature(signature)
		}
		if global.auth_mode == SIGNATURE_AUTH {
			kw.AgentString = fmt.Sprintf("%s/%s (%s)", APPNAME, VERSION, account)
		} else {
			kw.AgentString = fmt.Sprintf("%s/%s", APPNAME, VERSION)
		}
		kw.APIClient.NewToken = kw.KWNewToken
		kw.SetDatabase(global.db.Sub("KWAPI"))
		kw.RedirectURI = redirect_uri
		kw.ProxyURI = proxy.Get().(string)
		kw.VerifySSL = *ssl_verify
		kw.ApplicationID = client_app_id
		kw.ClientSecret(client_app_secret)
		kw.ConnectTimeout = time.Second * time.Duration(*connect_timeout_secs)
		kw.RequestTimeout = time.Second * time.Duration(*request_timeout_secs)
		kw.MaxChunkSize = (int64(*chunk_size_mb) * 1024) * 1024
		kw.Retries = 3

		if global.single_thread || global.snoop {
			kw.SetLimiter(1)
			kw.SetTransferLimiter(1)
		} else {
			kw.SetLimiter(*max_api_calls)
			kw.SetTransferLimiter(*max_file_transfer)
		}
		global.kw = kw
		//account = setup_account()
		return true
	}

	var tested bool

	test_api := func() bool {
		if !load_api() {
			Err("API is missing some required configuration, please revisit '*** UNCONFIGURED ***' settings.")
			pause()
			return false
		}

		if global.auth_mode == SIGNATURE_AUTH {
			global.kw.TokenStore.Delete(account)
		}

		if account != NONE {
			token, err := global.kw.TokenStore.Load(account)
			if err != nil {
				global.kw.TokenStore.Delete(account)
			}
			if token != nil {
				if token.Expires <= time.Now().Unix() {
					global.kw.TokenStore.Delete(account)
				}
			}
		}

		var err error
		Flash("[%s]: Authenticating, please wait...", global.kw.Server)

		if !authenticated || account == NONE {
			account = setup_account()
		}

		err = global.kw.Session(account).Call(APIRequest{
			Method: "GET",
			Path:   "/rest/users/me",
			Output: nil,
		})

		if err != nil {
			Stdout("[ERROR] %s", err.Error())
			pause()
			bad_test = true
			return false
		}
		tested = true
		Log("[SUCCESS]: %s reports successful API communications!", global.kw.Server)
		pause()
		bad_test = false
		return true
	}

	setup.Func("Test Current API Configuration.", test_api)

	// Display configuration menu
	enter_setup := func() {
		for {
			tested = false
			if setup.Select(true) {
				*server = strings.TrimPrefix(strings.ToLower(*server), "https://")
				if load_api() && !tested && nfo.GetConfirm("\nWould you like to validate changes with a quick test?") {
					Stdout("\n")
					if test_api() {
						save_config()
						break
					} else {
						continue
					}
				} else {
					save_config()
					break
				}
			} else if bad_test {
				if nfo.GetConfirm("\nCould not successfully validate settings, save anyway?") {
					save_config()
					break
				} else {
					continue
				}
			}
			break
		}
		if test_required && (bad_test || !tested) {
			os.Exit(1)
		}
	}

	if !configure_api {
		if !load_api() {
			enter_setup()
			if !load_api() {
				Exit(1)
			}
		}
	} else {
		enter_setup()
		return
	}
}
