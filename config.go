package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	. "github.com/cmcoffee/go-kwlib"
	"github.com/cmcoffee/go-snuglib/nfo"
	"strings"
	"time"
)

// Loads kiteworks API client id and secret from config file.
func init_kw_api() {
	if global.kw != nil {
		return
	}
	global.kw = new(KWAPI)

	// Initilize database
	init_database()

	global.kw.AgentString = fmt.Sprintf("%s/%s", NAME, VERSION)
	global.kw.Snoop = global.snoop
	if global.snoop {
		global.kw.SetLimiter(1)
	} else {
		global.kw.SetLimiter(5)
	}
	global.kw.TokenStore = KVLiteStore(global.db)
	global.kw.RedirectURI = global.cfg.Get("configuration", "redirect_uri")
	global.kw.ProxyURI = global.cfg.Get("configuration", "proxy_uri")
	global.kw.VerifySSL = global.cfg.GetBool("configuration", "ssl_verify")
	global.kw.ConnectTimeout = time.Second * time.Duration(global.cfg.GetInt("configuration", "connect_timeout_secs"))
	global.kw.RequestTimeout = time.Second * time.Duration(global.cfg.GetInt("configuration", "request_timeout_secs"))
	global.kw.MaxChunkSize = (global.cfg.GetInt("configuration", "chunk_size_mb") * 1024) * 1024
	global.kw.Retries = 3

	if !global.cfg.Exists("do_not_modify") {
		Fatal("Outdated configuration file, please obtain a new config file via https://github.com/cmcoffee/kitebroker/kitebroker.cfg")
	}

	api_cfg_0 := global.cfg.Get("do_not_modify", "api_cfg_0")
	api_cfg_1 := global.cfg.Get("do_not_modify", "api_cfg_1")

	if len(api_cfg_0) < 34 {
		config_api()
		return
	}

	r_key := []byte(api_cfg_0[0:34])
	cs_e := []byte(api_cfg_0[34:])
	s_key := []byte(api_cfg_1 + api_cfg_0[0:34])

	global.kw.Server = global.cfg.Get("configuration", "server")
	global.kw.ApplicationID = string(decrypt([]byte(api_cfg_1), r_key))
	global.kw.ClientSecret(string(decrypt(cs_e, s_key)))

	init_kw_auth()
	init_logging()
	global.user = global.kw.Session(global.cfg.Get("configuration", "account"))
}

// Configure API settings
func config_api() {
	var (
		client_id     string
		client_secret string
	)

	Stdout("--- kiteworks API Configuration ---\n\n")

	for {
		global.kw.Server = strings.TrimPrefix(strings.ToLower(GetInput("kiteworks Server: ")), "https://")
		global.cfg.Set("configuration", "server", global.kw.Server)
		client_id = GetInput("Client Application ID: ")
		client_secret = GetInput("Client Secret Key: ")

		if global.auth_mode == SIGNATURE_AUTH {
			set_signature()
		}

		Stdout(NONE)

		if confirm := GetConfirm("Confirm API Settings"); confirm {
			break
		}
		Stdout(NONE)
	}

	Stdout(NONE)

	api_cfg_0 := string(RandBytes(34))
	api_cfg_1 := string(encrypt([]byte(client_id), []byte(api_cfg_0)))
	api_cfg_0 = api_cfg_0 + string(encrypt([]byte(client_secret), []byte(api_cfg_1+api_cfg_0)))

	Critical(global.cfg.Set("do_not_modify", "api_cfg_0", api_cfg_0))
	Critical(global.cfg.Set("do_not_modify", "api_cfg_1", api_cfg_1))

	Critical(global.cfg.Save("do_not_modify"))
	Critical(global.cfg.Save("configuration"))

	global.kw.ApplicationID = client_id
	global.kw.ClientSecret(client_secret)

	init_kw_auth()
	init_logging()
}

// Load defaults
func load_config_defaults() (err error) {
	return global.cfg.Defaults(`
[configuration]
server = 
account = 
auth_flow = password
redirect_uri = https://kitebroker/

# Proxy server in URI format. (ie.. https://proxy.com:3128)
proxy_uri =

# Verify SSL Certificate on Appliance. (improves security)
ssl_verify = yes

# Timeout for kiteworks API requests.
connect_timeout_secs = 12
request_timeout_secs = 60

# Upload Chunk Size
chunk_size_mb = 68

#### Autogenerated Config Area Below Here. (Do not modify!) #####
[do_not_modify]
api_cfg_0 =
api_cfg_1 =
		`)
}

// Loads signature for signature authentication
func load_signature() bool {
	if global.auth_mode != SIGNATURE_AUTH {
		return true
	}

	var sig string
	global.db.Get("kitebroker", "signature", &sig)
	if sig == NONE {
		return false
	}
	global.kw.Signature(sig)
	return true
}

// Configure signature if it's not configured as of yet.
func set_signature() {
	sig := GetInput("Signature Secret: ")
	global.db.CryptSet("kitebroker", "signature", &sig)
	global.kw.Signature(sig)
}

// Configure user account for auth token.
func init_kw_auth() {
	sig_status := load_signature()
	if global.setup {
		global.cfg.Unset("configuration", "account")
	}

	if global.kw.Server == NONE || !sig_status {
		Stdout("--- kiteworks API Configuration ---\n\n")
		for global.kw.Server == NONE {
			global.kw.Server = strings.TrimPrefix(strings.ToLower(GetInput("kiteworks Server: ")), "https://")
		}
		if !sig_status {
			set_signature()
		}
	}
	Flash("[%s]: Authenticating, please wait...", global.kw.Server)
	username := global.cfg.Get("configuration", "account")
	user, err := global.kw.Authenticate(username)
	Critical(err)
	global.cfg.Set("configuration", "account", string(user.Username))
	Critical(global.cfg.Save("configuration"))
}

// Opens database where config file is located.
func init_database() {
	var err error

	db_filename := FormatPath(fmt.Sprintf("%s/%s.db", global.root, NAME))
	global.db, err = SecureDatabase(db_filename)
	Critical(err)
}

func init_logging() {
	file, err := nfo.LogFile(FormatPath(fmt.Sprintf("%s/log/%s.log", global.root, NAME)), 10, 10)
	Critical(err)
	nfo.SetFile(nfo.ALL, file)
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