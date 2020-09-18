package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"github.com/cmcoffee/go-snuglib/kvlite"
	"github.com/cmcoffee/go-snuglib/nfo"
	"github.com/cmcoffee/go-snuglib/options"
	. "github.com/cmcoffee/kitebroker/core"
	"net"
	"os"
	"strings"
	"time"
)

// Wrapper for config items stored in database
type dbCFG struct{}

var dbConfig dbCFG

func (d dbCFG) user() (account string) {
	global.db.Get("kitebroker", "account", &account)
	return
}

func (d dbCFG) set_user(account string) {
	global.db.Set("kitebroker", "account", &account)
}

func (d dbCFG) max_api_calls() (max int) {
	found := global.db.Get("kitebroker", "max_api_calls", &max)
	if !found {
		return 3
	}
	return
}

func (d dbCFG) set_max_api_calls(max int) {
	global.db.Set("kitebroker", "max_api_calls", &max)
}

func (d dbCFG) max_file_transfer() (max int) {
	found := global.db.Get("kitebroker", "max_file_transfer", &max)
	if !found {
		return 3
	}
	return
}

func (d dbCFG) set_max_file_transfer(max int) {
	global.db.Set("kitebroker", "max_file_transfer", &max)
}

func (d dbCFG) set_connect_timeout_secs(max int) {
	global.db.Set("kitebroker", "connect_timeout_secs", &max)
}

func (d dbCFG) connect_timeout_secs() (max int) {
	found := global.db.Get("kitebroker", "connect_timeout_secs", &max)
	if !found {
		return 12
	}
	return
}

func (d dbCFG) set_request_timeout_secs(max int) {
	global.db.Set("kitebroker", "request_timeout_secs", &max)
}

func (d dbCFG) request_timeout_secs() (max int) {
	found := global.db.Get("kitebroker", "request_timeout_secs", &max)
	if !found {
		return 60
	}
	return
}

func (d dbCFG) set_chunk_size_mb(max int) {
	global.db.Set("kitebroker", "chunk_size_mb", &max)
}

func (d dbCFG) chunk_size_mb() (max int) {
	found := global.db.Get("kitebroker", "chunk_size_mb", &max)
	if !found {
		return 68
	}
	return
}

// Loads kiteworks API client id and secret from config file.
func init_kw_api() {
	if global.kw != nil {
		return
	}

	// Initilize database
	init_database()

	if !global.cfg.Exists("do_not_modify") {
		Fatal("Outdated configuration file, please obtain a new config file via https://github.com/cmcoffee/kitebroker/kitebroker.cfg")
	}

	config_api(false)

	Flash("[%s]: Authenticating, please wait...", global.kw.Server)
	username := dbConfig.user()
	user, err := global.kw.Login(username)
	if err != nil {
		Err(err)
		Log("\n")
		config_api(true)
		return
	}
	global.user = *user
	dbConfig.set_user(string(user.Username))

	init_logging()
}

// Sets app_id and app_secret.
func set_api_configs(client_id, client_secret string) {
	api_cfg_0 := string(RandBytes(34))
	api_cfg_1 := string(encrypt([]byte(client_id), []byte(api_cfg_0)))
	api_cfg_0 = api_cfg_0 + string(encrypt([]byte(client_secret), []byte(api_cfg_1+api_cfg_0)))

	Critical(global.cfg.Set("do_not_modify", "api_cfg_0", api_cfg_0))
	Critical(global.cfg.Set("do_not_modify", "api_cfg_1", api_cfg_1))
}

// Loads app_id and app_secret
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

// Load defaults
func load_config_defaults() (err error) {
	return global.cfg.Defaults(`
[configuration]
server = 
auth_flow = password
redirect_uri = https://kitebroker/

# Proxy server in URI format. (ie.. https://proxy.com:3128)
proxy_uri =

# Verify SSL Certificate on Appliance. (improves security)
ssl_verify = yes

#### Autogenerated Config Area Below Here. (Do not modify!) #####
[do_not_modify]
api_cfg_0 =
api_cfg_1 =
		`)
}

// Opens database where config file is located.
func init_database() {
	var err error

	MkDir(fmt.Sprintf("%s/data/", global.root))

	db_filename := FormatPath(fmt.Sprintf("%s/data/%s.db", global.root, APPNAME))
	global.db, err = SecureDatabase(db_filename)
	Critical(err)
}

// Opens go-kvlite database using mac address for lock.
func SecureDatabase(file string) (*DBase, error) {
	// Provides us the mac address of the first interface.
	get_mac_addr := func() []byte {
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

	db, err := kvlite.Open(file, get_mac_addr()[0:]...)
	if err != nil {
		if err == kvlite.ErrBadPadlock {
			Notice("Hardware changes detected, you will need to reauthenticate.")
			if err := kvlite.CryptReset(file); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
		db, err = kvlite.Open(file, get_mac_addr()[0:]...)
		if err != nil {
			return nil, err
		}
	}
	return &DBase{db}, nil
}

// Initialize Logging.
func init_logging() {
	file, err := nfo.LogFile(FormatPath(fmt.Sprintf("%s/logs/%s.log", global.root, APPNAME)), 10, 10)
	Critical(err)
	nfo.SetFile(nfo.STD, file)
	if global.debug || global.snoop {
		nfo.SetOutput(nfo.DEBUG, os.Stdout)
		nfo.SetFile(nfo.DEBUG, nfo.GetFile(nfo.ERROR))
	}
}

// Perform sha256.Sum256 against input byte string.
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

// Encrypts data using the hash of key provided.
func encrypt(input []byte, key []byte) []byte {

	var block cipher.Block

	key = hashBytes(key)
	block, _ = aes.NewCipher(key)

	buff := make([]byte, len(input))
	copy(buff, input)

	cipher.NewCFBEncrypter(block, key[0:block.BlockSize()]).XORKeyStream(buff, buff)

	return []byte(base64.RawStdEncoding.EncodeToString(buff))
}

// Decrypts data.
func decrypt(input []byte, key []byte) (decoded []byte) {

	var block cipher.Block

	key = hashBytes(key)

	decoded, _ = base64.RawStdEncoding.DecodeString(string(input))
	block, _ = aes.NewCipher(key)
	cipher.NewCFBDecrypter(block, key[0:block.BlockSize()]).XORKeyStream(decoded, decoded)

	return
}

// Proxy Configuration
type proxyValue struct {
	desc  string
	value string
}

func (c *proxyValue) Set() bool {
	v := c.value
	c.value = nfo.GetInput(fmt.Sprintf(`
# Format of proxy server should be: https://proxy.server.com:3127
# Leave blank for direct connection/no proxy.
--> %s: `, c.desc))
	return c.value != v
}

func (c *proxyValue) Get() interface{} {
	return c.value
}

func (c *proxyValue) String() string {
	if IsBlank(c.value) {
		return fmt.Sprintf("%s:\t(Direct Connection/No Proxy)", c.desc)
	} else {
		return fmt.Sprintf("%s:\t%s", c.desc, c.value)
	}
}

func pause() {
	nfo.PressEnter("\n(press enter to continue)")
}

// Configuration Menu for API Settings
func config_api(configure_api bool) {

	setup := options.NewOptions("--- kiteworks API coniguration ---", "(selection or 'q' to save & exit)", 'q')
	client_app_id, client_app_secret := load_api_configs()
	redirect_uri := global.cfg.Get("configuration", "redirect_uri")
	proxy_uri := global.cfg.Get("configuration", "proxy_uri")
	account := dbConfig.user()

	var signature string
	global.db.Get("kitebroker", "signature", &signature)

	if global.auth_mode == SIGNATURE_AUTH {
		setup.StringVar(&account, "User Account", account, "Please provide e-mail address of user account.", false)
	}

	server := setup.String("kiteworks Host", global.cfg.Get("configuration", "server"), "Please provide the kiteworks appliance hostname. (ie.. kiteworks.domain.com)", false)

	if IsBlank(client_app_id) || IsBlank(client_app_secret) || global.auth_mode == SIGNATURE_AUTH {
		setup.StringVar(&client_app_id, "Client Application ID", client_app_id, NONE, false)
		setup.StringVar(&client_app_secret, "Client Secret Key", client_app_secret, NONE, true)
	}

	if global.auth_mode == SIGNATURE_AUTH {
		setup.StringVar(&signature, "Signature Secret", signature, NONE, true)
	} else {
		setup.Func("Reset user credentials", func() bool {
			kw := new(APIClient)
			kw.TokenStore = KVLiteStore(global.db)
			kw.TokenStore.Delete(account)
			account = NONE
			dbConfig.set_user(account)
			Notice("User account has been reset, you will be prompted for credentials at next run/API test.")
			pause()
			return false
		})
	}

	if IsBlank(redirect_uri) || global.auth_mode == SIGNATURE_AUTH {
		setup.StringVar(&redirect_uri, "Redirect URI", redirect_uri, "Redirect URI should simply match setting in kiteworks admin, default: https://kitebroker", false)
	}

	ssl_verify := setup.Bool("Verify SSL", global.cfg.GetBool("configuration", "ssl_verify"))
	proxy := proxyValue{"Proxy Server", proxy_uri}
	setup.Register(&proxy)

	advanced := options.NewOptions(NONE, "(selection or 'q' to return to previous)", 'q')
	connect_timeout_secs := advanced.Int("Connection timeout seconds", dbConfig.connect_timeout_secs(), "Default Value: 12", 0, 600)
	request_timeout_secs := advanced.Int("Request timeout seconds", dbConfig.request_timeout_secs(), "Default Value: 60", 0, 600)
	max_api_calls := advanced.Int("Maximum API Calls", dbConfig.max_api_calls(), "Default Value: 3", 1, 5)
	max_file_transfer := advanced.Int("Maximum file transfers", dbConfig.max_file_transfer(), "Default Value: 3", 1, 5)
	chunk_size_mb := advanced.Int("Chunk size in megabytes", dbConfig.chunk_size_mb(), "Default Value: 68", 1, 68)

	setup.Options("Advanced", advanced, false)

	//Saves current coneciguration.
	save_config := func() {
		Critical(global.cfg.Set("configuration", "redirect_uri", redirect_uri))
		Critical(global.cfg.Set("configuration", "proxy_uri", proxy.Get().(string)))
		Critical(global.cfg.Set("configuration", "server", strings.TrimPrefix(strings.ToLower(*server), "https://")))
		Critical(global.cfg.Set("configuration", "ssl_verify", *ssl_verify))
		set_api_configs(client_app_id, client_app_secret)
		Critical(global.cfg.Save())
		if global.auth_mode == SIGNATURE_AUTH && !IsBlank(signature) {
			global.db.Set("kitebroker", "signature", &signature)
		}
		dbConfig.set_user(account)
		dbConfig.set_connect_timeout_secs(*connect_timeout_secs)
		dbConfig.set_request_timeout_secs(*request_timeout_secs)
		dbConfig.set_max_api_calls(*max_api_calls)
		dbConfig.set_max_file_transfer(*max_file_transfer)
		dbConfig.set_chunk_size_mb(*chunk_size_mb)

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
		if IsBlank(*server) || IsBlank(redirect_uri) || IsBlank(client_app_id) || IsBlank(client_app_secret) || (global.auth_mode == SIGNATURE_AUTH && IsBlank(signature)) || (global.auth_mode == SIGNATURE_AUTH && IsBlank(account)) {
			return false
		}
		kw := &KWAPI{new(APIClient)}
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
		kw.TokenStore = KVLiteStore(global.db)
		kw.RedirectURI = redirect_uri
		kw.ProxyURI = proxy.Get().(string)
		kw.VerifySSL = *ssl_verify
		kw.ApplicationID = client_app_id
		kw.ClientSecret(client_app_secret)
		kw.ConnectTimeout = time.Second * time.Duration(*connect_timeout_secs)
		kw.RequestTimeout = time.Second * time.Duration(*request_timeout_secs)
		kw.MaxChunkSize = (int64(*chunk_size_mb) * 1024) * 1024
		kw.Retries = 3

		if global.snoop {
			kw.Snoop = true
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
			Stdout("")
			Stdout("[ERROR] %s", err.Error())
			pause()
			return false
		}
		tested = true
		Log("[SUCCESS]: %s reports succesful API communications!", global.kw.Server)
		pause()
		return true
	}

	setup.Func("Test Current API Configuration.", test_api)

	// Display configuraiton menu
	enter_setup := func() {
		for {
			tested = false
			if setup.Select(true) {
				*server = strings.TrimPrefix(strings.ToLower(*server), "https://")
				if load_api() && !tested && nfo.GetConfirm("\nWould you like validate changes with a quick test?") {
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
			}
			break
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
