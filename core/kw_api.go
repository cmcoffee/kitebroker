package core

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/cmcoffee/kitebroker/core/snuglib/iotimeout"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type KWAPI struct {
	Server         string        // kiteworks host name.
	ApplicationID  string        // Application ID set for kiteworks custom app.
	RedirectURI    string        // Redirect URI for kiteworks custom app.
	AgentString    string        // Agent-String header for calls to kiteworks.
	VerifySSL      bool          // Verify certificate for connections.
	ProxyURI       string        // Proxy for outgoing https requests.
	Snoop          bool          // Flag to snoop API calls
	RequestTimeout time.Duration // Timeout for request to be answered from kiteworks server.
	ConnectTimeout time.Duration // Timeout for TLS connection to kiteworks server.
	MaxChunkSize   int64         // Max Upload Chunksize in bytes, min = 1M, max = 68M
	Retries        uint          // Max retries on a failed call
	TokenStore     TokenStore    // TokenStore for reading and writing auth tokens securely.
	secrets        kwapi_secrets // Encrypted config options such as signature token, client secret key.
	limiter        chan struct{} // Implements a limiter for API calls to appliance.
	trans_limiter  chan struct{} // Implements a file transfer limiter.
}

// Configures maximum number of simultaneous api calls.
func (K *KWAPI) SetLimiter(max_calls int) {
	if max_calls <= 0 {
		max_calls = 1
	}
	if K.limiter == nil {
		K.limiter = make(chan struct{}, max_calls)
	}
}

// Configures maximum number of simultaneous file transfers.
func (K *KWAPI) SetTransferLimiter(max_transfers int) {
	if max_transfers <= 0 {
		max_transfers = 1
	}
	if K.trans_limiter == nil {
		K.trans_limiter = make(chan struct{}, max_transfers)
	}
}

func (K *KWAPI) GetTransferLimit() int {
	if K.trans_limiter != nil {
		return cap(K.trans_limiter)
	}
	return 1
}

// Tests TokenStore, creates one if missing.
func (K *KWAPI) testTokenStore() {
	if K.TokenStore == nil {
		K.TokenStore = KVLiteStore(OpenCache())
	}
}

type kwapi_secrets struct {
	key               []byte
	signature_key     []byte
	client_secret_key []byte
}

// TokenStore interface for saving and retrieving auth tokens.
// Errors should only be underlying issues reading/writing to the store itself.
type TokenStore interface {
	Save(username string, auth *KWAuth) error
	Load(username string) (*KWAuth, error)
	Delete(username string) error
}

type kvLiteStore struct {
	Table
}

// Wraps KVLite Databse as a auth token store.
func KVLiteStore(input *Database) *kvLiteStore {
	return &kvLiteStore{input.Table("KWAPI.tokens")}
}

// Save token to TokenStore
func (T kvLiteStore) Save(username string, auth *KWAuth) error {
	T.Table.CryptSet(username, &auth)
	return nil
}

// Retrieve token from TokenStore
func (T *kvLiteStore) Load(username string) (*KWAuth, error) {
	var auth *KWAuth
	T.Table.Get(username, &auth)
	return auth, nil
}

// Remove token from TokenStore
func (T *kvLiteStore) Delete(username string) error {
	T.Table.Unset(username)
	return nil
}

// Encryption function for storing signature and client secrets.
func (k *kwapi_secrets) encrypt(input string) []byte {

	if k.key == nil {
		k.key = RandBytes(32)
	}

	block, err := aes.NewCipher(k.key)
	Critical(err)
	in_bytes := []byte(input)

	buff := make([]byte, len(in_bytes))
	copy(buff, in_bytes)

	cipher.NewCFBEncrypter(block, k.key[0:block.BlockSize()]).XORKeyStream(buff, buff)

	return buff
}

// Retrieves encrypted signature and client secrets.
func (k *kwapi_secrets) decrypt(input []byte) string {
	if k.key == nil {
		return NONE
	}

	output := make([]byte, len(input))

	block, _ := aes.NewCipher(k.key)
	cipher.NewCFBDecrypter(block, k.key[0:block.BlockSize()]).XORKeyStream(output, input)

	return string(output)
}

// APIRequest model
type APIRequest struct {
	Version int
	Method  string
	Path    string
	Params  []interface{}
	Output  interface{}
}

// SetPath shortcut.
var SetPath = fmt.Sprintf

// Creates Param for KWAPI post
func SetParams(vars ...interface{}) (output []interface{}) {
	if len(vars) == 0 {
		return nil
	}
	var (
		post_json PostJSON
		query     Query
		form      PostForm
	)

	process_vars := func(vars interface{}) {
		switch x := vars.(type) {
		case Query:
			if query == nil {
				query = x
			} else {
				for key, val := range x {
					query[key] = val
				}
			}
		case PostJSON:
			if post_json == nil {
				post_json = x
			} else {
				for key, val := range x {
					post_json[key] = val
				}
			}
		case PostForm:
			if form == nil {
				form = x
			} else {
				for key, val := range x {
					form[key] = val
				}
			}
		}
	}

	for {
		tmp := vars[0:0]
		for _, v := range vars {
			switch val := v.(type) {
			case []interface{}:
				for _, elem := range val {
					tmp = append(tmp[0:], elem)
				}
			case nil:
				continue
			default:
				process_vars(val)

			}
		}
		if len(tmp) == 0 {
			break
		}
		vars = tmp
	}

	if post_json != nil {
		output = append(output, post_json)
	}
	if query != nil {
		output = append(output, query)
	}
	if form != nil {
		output = append(output, form)
	}
	return
}

// Post JSON to KWAPI.
type PostJSON map[string]interface{}

// Form POST to KWAPI.
type PostForm map[string]interface{}

// Add Query params to KWAPI request.
type Query map[string]interface{}

// KWAPI Client
type KWAPIClient struct {
	session *KWSession
	*http.Client
}

func (c *KWAPIClient) Do(req *http.Request) (resp *http.Response, err error) {
	resp, err = c.Client.Do(req)
	if err != nil {
		return nil, err
	}

	err = c.session.respError(resp)
	return
}

// Sets signature key.
func (K *KWAPI) Signature(signature_key string) {
	K.secrets.signature_key = K.secrets.encrypt(signature_key)
}

// Sets client secret key.
func (K *KWAPI) ClientSecret(client_secret_key string) {
	K.secrets.client_secret_key = K.secrets.encrypt(client_secret_key)
}

// kiteworks Auth token.
type KWAuth struct {
	AccessToken  string `json:"access_token"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
	Expires      int64  `json:"expires_in"`
}

// kiteworks Session.
type KWSession struct {
	Username string
	*KWAPI
}

// Wraps a session for specfiied user.
func (K *KWAPI) Session(username string) KWSession {
	return KWSession{username, K}
}

// Prints arrays for string and int arrays, when submitted to Queries or Form post.
func Spanner(input interface{}) string {
	switch v := input.(type) {
	case []string:
		return strings.Join(v, ",")
	case []int:
		var output []string
		for _, i := range v {
			output = append(output, fmt.Sprintf("%v", i))
		}
		return strings.Join(output, ",")
	default:
		return fmt.Sprintf("%v", input)
	}
}

// Decodes JSON response body to provided interface.
func (K *KWAPI) decodeJSON(resp *http.Response, output interface{}) (err error) {
	var (
		snoop_output map[string]interface{}
		snoop_buffer bytes.Buffer
		body         io.Reader
	)

	resp.Body = iotimeout.NewReadCloser(resp.Body, K.RequestTimeout)
	defer resp.Body.Close()

	if K.Snoop {
		if output == nil {
			Debug("<-- RESPONSE STATUS: %s", resp.Status)
			dec := json.NewDecoder(resp.Body)
			dec.Decode(&snoop_output)
			if len(snoop_output) > 0 {
				o, _ := json.MarshalIndent(&snoop_output, "", "  ")
				Debug("<-- RESPONSE BODY: \n%s\n", string(o))
			}
			return nil
		} else {
			Debug("<-- RESPONSE STATUS: %s", resp.Status)
			body = io.TeeReader(resp.Body, &snoop_buffer)
		}
	} else {
		body = resp.Body
	}

	if output == nil {
		return nil
	}

	dec := json.NewDecoder(body)
	err = dec.Decode(output)
	if err == io.EOF {
		return nil
	}

	if err != nil {
		if K.Snoop {
			txt := snoop_buffer.String()
			if err := snoop_request(&snoop_buffer); err != nil {
				Stdout(txt)
			}
			err = fmt.Errorf("I cannot understand what %s is saying: %s", K.Server, err.Error())
			return
		} else {
			err = fmt.Errorf("I cannot understand what %s is saying: %s", K.Server, err.Error())
			return
		}
	}

	if K.Snoop {
		snoop_request(&snoop_buffer)
	}
	return
}

// Provides output of specified request.
func snoop_request(body io.Reader) error {
	var snoop_generic map[string]interface{}
	dec := json.NewDecoder(body)
	if err := dec.Decode(&snoop_generic); err != nil {
		return err
	}
	if snoop_generic != nil {
		for v, _ := range snoop_generic {
			switch v {
			case "refresh_token":
				fallthrough
			case "access_token":
				snoop_generic[v] = "[HIDDEN]"
			}
		}
	}
	o, _ := json.MarshalIndent(&snoop_generic, "", "  ")
	Debug("<-- RESPONSE BODY: \n%s\n", string(o))
	return nil
}

// kiteworks Client
func (s KWSession) NewClient() *KWAPIClient {
	var transport http.Transport

	// Allows invalid certs if set to "no" in config.
	if !s.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	if s.ProxyURI != NONE {
		proxyURL, err := url.Parse(s.ProxyURI)
		Critical(err)
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	transport.Dial = (&net.Dialer{
		Timeout: s.ConnectTimeout,
	}).Dial

	transport.TLSHandshakeTimeout = s.ConnectTimeout

	return &KWAPIClient{&s, &http.Client{Transport: &transport, Timeout: s.RequestTimeout}}
}

// New kiteworks Request.
func (s KWSession) NewRequest(method, path string, api_ver int) (req *http.Request, err error) {

	// Set API Version
	if api_ver == 0 {
		api_ver = 11
	}

	req, err = http.NewRequest(method, fmt.Sprintf("https://%s%s", s.Server, path), nil)
	if err != nil {
		return nil, err
	}

	req.URL.Host = s.Server
	req.URL.Scheme = "https"
	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", api_ver))
	if s.AgentString == NONE {
		s.AgentString = "kwlib/1.0"
	}
	req.Header.Set("User-Agent", s.AgentString)
	req.Header.Set("Referer", "https://"+s.Server+"/")

	if err := s.setToken(req, false); err != nil {
		return nil, err
	}

	return req, nil
}

// kiteworks API Call Wrapper
func (s KWSession) Call(api_req APIRequest) (err error) {
	if s.limiter != nil {
		s.limiter <- struct{}{}
		defer func() { <-s.limiter }()
	}

	if api_req.Version == 0 {
		api_req.Version = 13
	}

	req, err := s.NewRequest(api_req.Method, api_req.Path, api_req.Version)
	if err != nil {
		return err
	}

	if s.Snoop {
		Debug("[kiteworks snoop]: %s", s.Username)
		Debug("--> METHOD: \"%s\" PATH: \"%s\"", strings.ToUpper(api_req.Method), api_req.Path)
	}

	var body []byte

	for _, in := range api_req.Params {
		switch i := in.(type) {
		case PostForm:
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			p := make(url.Values)
			for k, v := range i {
				p.Add(k, Spanner(v))
				if s.Snoop {
					Debug("\\-> POST PARAM: \"%s\" VALUE: \"%s\"", k, p[k])
				}
			}
			body = []byte(p.Encode())
		case PostJSON:
			req.Header.Set("Content-Type", "application/json")
			json, err := json.Marshal(i)
			if err != nil {
				return err
			}
			if s.Snoop {
				Debug("\\-> POST JSON: %s", string(json))
			}
			body = json
		case Query:
			q := req.URL.Query()
			for k, v := range i {
				q.Set(k, Spanner(v))
				if s.Snoop {
					Debug("\\-> QUERY: %s=%s", k, q[k])
				}
			}
			req.URL.RawQuery = q.Encode()
		case nil:
			continue
		default:
			return fmt.Errorf("Unknown request exception.")
		}
	}

	var resp *http.Response

	// Retry calls on failure.
	for i := 0; i <= int(s.Retries); i++ {
		reAuth := func(s *KWSession, req *http.Request, orig_err error) error {
			if s.secrets.signature_key == nil {
				existing, err := s.TokenStore.Load(s.Username)
				if err != nil {
					return err
				}
				if token, err := s.refreshToken(s.Username, existing); err == nil {
					if err := s.TokenStore.Save(s.Username, token); err != nil {
						return err
					}
					if err = s.setToken(req, false); err == nil {
						return nil
					}
				}
				s.TokenStore.Delete(s.Username)
				Critical(fmt.Errorf("Token is no longer valid: %s", orig_err.Error()))
			}
			return s.setToken(req, KWAPIError(err, TOKEN_ERR))
		}

		req.Body = ioutil.NopCloser(bytes.NewReader(body))
		client := s.NewClient()
		resp, err = client.Do(req)

		if err != nil && KWAPIError(err, ERR_INTERNAL_SERVER_ERROR|TOKEN_ERR) {
			if KWAPIError(err, TOKEN_ERR) {
				if err := reAuth(&s, req, err); err != nil {
					return err
				}
			}
			Debug("(CALL ERROR) %s -> %s: %s (%d/%d)", s.Username, api_req.Path, err.Error(), i+1, s.Retries+1)
			time.Sleep((time.Second * time.Duration(i+1)) * time.Duration(i+1))
			continue
		} else if err != nil {
			if !IsKWError(err) {
				Warn("%s -> %s: %s (%d/%d)", s.Username, api_req.Path, err.Error(), i+1, s.Retries+1)
				time.Sleep((time.Second * time.Duration(i+1)) * time.Duration(i+1))
				continue
			}
			break
		}

		err = s.decodeJSON(resp, api_req.Output)
		if err != nil && KWAPIError(err, ERR_INTERNAL_SERVER_ERROR|TOKEN_ERR) {
			Debug("(CALL ERROR) %s -> %s: %s (%d/%d)", s.Username, api_req.Path, err.Error(), i+1, s.Retries+1)
			if err := reAuth(&s, req, err); err != nil {
				return err
			}
			time.Sleep((time.Second * time.Duration(i+1)) * time.Duration(i+1))
			continue
		} else {
			break
		}
	}
	return
}

// Call handler which allows for easier getting of multiple-object arrays.
// An offset of -1 will provide all results, any positive offset will only return the requested results.
func (s KWSession) DataCall(req APIRequest, offset, limit int) (err error) {

	output := req.Output
	params := req.Params

	if limit <= 0 {
		limit = 100
	}

	var managed bool

	// If we're provided a non-negative offset, get only results requested.
	if offset < 0 {
		offset = 0
	} else {
		managed = true
	}

	var o struct {
		Data interface{} `json:"data"`
	}

	o.Data = req.Output

	var tmp []map[string]interface{}

	var enc_buff bytes.Buffer
	enc := json.NewEncoder(&enc_buff)
	dec := json.NewDecoder(&enc_buff)

	// Get response, decode it to a generic array of map[string]interface{}.
	// Stack responses, them, and then encode the stack, then decode to original request.
	for {
		req.Params = SetParams(params, Query{"limit": limit, "offset": offset})
		req.Output = &o
		if err = s.Call(req); err != nil {
			return err
		}
		// Decode the results we get, convert to []map[string]interface{}, and stack results.
		if o.Data != nil {
			enc_buff.Reset()
			err := enc.Encode(o.Data)
			if err != nil {
				return err
			}
			var t []map[string]interface{}
			err = dec.Decode(&t)
			if err != nil {
				return err
			}
			tmp = append(tmp, t[0:]...)
			if len(t) < limit || managed {
				break
			} else {
				offset = offset + limit
			}
		} else {
			return fmt.Errorf("Something unexpected happened, got an empty response.")
		}
	}

	enc_buff.Reset()

	// Take stack of results we recevied and decode it back to the original object.
	if err := enc.Encode(tmp); err != nil {
		return err
	} else {
		tmp = nil
		if err = dec.Decode(output); err != nil {
			return err
		}
	}
	return
}
