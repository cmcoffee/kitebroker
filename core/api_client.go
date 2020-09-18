package core

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/cmcoffee/go-snuglib/iotimeout"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type APIClient struct {
	Server          string                               // kiteworks host name.
	ApplicationID   string                               // Application ID set for kiteworks custom app.
	RedirectURI     string                               // Redirect URI for kiteworks custom app.
	AgentString     string                               // Agent-String header for calls to kiteworks.
	VerifySSL       bool                                 // Verify certificate for connections.
	ProxyURI        string                               // Proxy for outgoing https requests.
	Snoop           bool                                 // Flag to snoop API calls
	RequestTimeout  time.Duration                        // Timeout for request to be answered from kiteworks server.
	ConnectTimeout  time.Duration                        // Timeout for TLS connection to kiteworks server.
	MaxChunkSize    int64                                // Max Upload Chunksize in bytes, min = 1M, max = 68M
	Retries         uint                                 // Max retries on a failed call
	TokenStore      TokenStore                           // TokenStore for reading and writing auth tokens securely.
	secrets         api_secrets                          // Encrypted config options such as signature token, client secret key.
	limiter         chan struct{}                        // Implements a limiter for API calls/transfers to/from appliance.
	trans_limiter   chan struct{}                        // Implements a file transfer limiter.
	ModifyRequest   func(req *http.Request)              // Modify the upcoming request after it is signed.
	NewToken        func(username string) (*Auth, error) // Provides new access_token.
	ErrorScanner    func(body []byte) APIError           // Reads body of response and interprets any errors.
	RetryErrorCodes []string                             // Error codes ("ERR_INTERNAL_SERVER_ERROR"), that should induce a retry. (will automatically try TokenErrorCodes as well)
	TokenErrorCodes []string                             // Error codes ("ERR_INVALID_GRANT"), that should indicate a problem with the current access token.
}

// Configures maximum number of simultaneous api calls.
func (K *APIClient) SetLimiter(max_calls int) {
	if max_calls <= 0 {
		max_calls = 1
	}
	if K.limiter == nil {
		K.limiter = make(chan struct{}, max_calls)
	}
}

// Configures maximum number of simultaneous file transfers.
func (K *APIClient) SetTransferLimiter(max_transfers int) {
	if max_transfers <= 0 {
		max_transfers = 1
	}
	if K.trans_limiter == nil {
		K.trans_limiter = make(chan struct{}, max_transfers)
	}
}

func (K *APIClient) GetTransferLimit() int {
	if K.trans_limiter != nil {
		return cap(K.trans_limiter)
	}
	return 1
}

// TokenStore interface for saving and retrieving auth tokens.
// Errors should only be underlying issues reading/writing to the store itself.
type TokenStore interface {
	Save(username string, auth *Auth) error
	Load(username string) (*Auth, error)
	Delete(username string) error
}

type kvLiteStore struct {
	Table
}

// Wraps KVLite Databse as a auth token store.
func KVLiteStore(input Database) *kvLiteStore {
	return &kvLiteStore{input.Table("KWAPI.tokens")}
}

// Save token to TokenStore
func (T *kvLiteStore) Save(username string, auth *Auth) error {
	T.Table.CryptSet(username, &auth)
	return nil
}

// Retrieve token from TokenStore
func (T *kvLiteStore) Load(username string) (*Auth, error) {
	var auth *Auth
	T.Table.Get(username, &auth)
	return auth, nil
}

// Remove token from TokenStore
func (T *kvLiteStore) Delete(username string) error {
	T.Table.Unset(username)
	return nil
}

type api_secrets struct {
	key               []byte
	signature_key     []byte
	client_secret_key []byte
}

// Encryption function for storing signature and client secrets.
func (k *api_secrets) encrypt(input string) []byte {

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
func (k *api_secrets) decrypt(input []byte) string {
	if k.key == nil {
		return NONE
	}

	output := make([]byte, len(input))

	block, _ := aes.NewCipher(k.key)
	cipher.NewCFBDecrypter(block, k.key[0:block.BlockSize()]).XORKeyStream(output, input)

	return string(output)
}

func (K APIClient) GetSignature() string {
	var sig string

	if K.secrets.signature_key != nil {
		sig = K.secrets.decrypt(K.secrets.signature_key)
	}

	return sig
}

func (K APIClient) GetClientSecret() string {
	var secret string

	if K.secrets.client_secret_key != nil {
		secret = K.secrets.decrypt(K.secrets.client_secret_key)
	}

	return secret
}

// APIRequest model
type APIRequest struct {
	Version int
	Header  http.Header
	Method  string
	Path    string
	Params  []interface{}
	Output  interface{}
}

// SetPath shortcut.
var SetPath = fmt.Sprintf

// Creates Param for API post
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

// Add Bearer token to APIClient requests.
func (s *APIClient) setToken(username string, req *http.Request) (err error) {
	if s.TokenStore == nil {
		return fmt.Errorf("APIClient: TokenStore not initalized.")
	}

	token, err := s.TokenStore.Load(username)
	if err != nil {
		return err
	}

	// If we find a token, check if it's still valid.
	if token != nil {
		if token.Expires <= time.Now().Unix() {
			// First attempt to use a refresh token if there is one.
			token, err = s.refreshToken(username, token)
			if err != nil && s.secrets.signature_key == nil {
				Debug("Unable to use refresh token: %v", err)
				Fatal("Access token has expired, must reauthenticate for new access token.")
			}
			err = nil
		}
	}

	if token == nil {
		if s.NewToken == nil {
			return fmt.Errorf("APIClient: NewToken not initalized.")
		}
		s.TokenStore.Delete(username)
		token, err = s.NewToken(username)
		if err != nil {
			return err
		}
	}

	if token != nil {
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		if err := s.TokenStore.Save(username, token); err != nil {
			return err
		}
	}
	return nil
}

// Get a new token from a refresh token.
func (K *APIClient) refreshToken(username string, auth *Auth) (*Auth, error) {
	if auth == nil || auth.RefreshToken == NONE {
		return nil, fmt.Errorf("No refresh token found for %s.", username)
	}

	path := fmt.Sprintf("https://%s/oauth/token", K.Server)

	req, err := http.NewRequest(http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}

	http_header := make(http.Header)
	http_header.Set("Content-Type", "application/x-www-form-urlencoded")
	if K.AgentString != NONE {
		http_header.Set("User-Agent", K.AgentString)
	}

	req.Header = http_header

	client_id := K.ApplicationID

	postform := &url.Values{
		"client_id":     {client_id},
		"client_secret": {K.secrets.decrypt(K.secrets.client_secret_key)},
		"grant_type":    {"refresh_token"},
		"refresh_token": {auth.RefreshToken},
	}

	if K.Snoop {
		Debug("[%s]: %s", K.Server, username)
		Debug("--> ACTION: \"POST\" PATH: \"%s\"", path)
		for k, v := range *postform {
			if k == "grant_type" || k == "RedirectURI" || k == "scope" {
				Debug("\\-> POST PARAM: %s VALUE: %s", k, v)
			} else {
				Debug("\\-> POST PARAM: %s VALUE: [HIDDEN]", k)
			}
		}
	}

	req.Body = ioutil.NopCloser(bytes.NewReader([]byte(postform.Encode())))
	req.Body = iotimeout.NewReadCloser(req.Body, K.RequestTimeout)
	defer req.Body.Close()

	resp, err := K.Do(req)
	if err != nil {
		return nil, err
	}

	var new_token struct {
		AccessToken  string      `json:"access_token"`
		Scope        string      `json:"scope"`
		RefreshToken string      `json:"refresh_token"`
		Expires      interface{} `json:"expires_in"`
	}

	if err := K.DecodeJSON(resp, &new_token); err != nil {
		return nil, err
	}

	if new_token.Expires != nil {
		expiry, _ := strconv.ParseInt(fmt.Sprintf("%v", new_token.Expires), 0, 64)
		auth.Expires = time.Now().Unix() + expiry
	}

	auth.AccessToken = new_token.AccessToken
	auth.RefreshToken = new_token.RefreshToken

	return auth, nil
}

// Post JSON to API.
type PostJSON map[string]interface{}

// Form POST to API.
type PostForm map[string]interface{}

// Add Query params to KWAPI request.
type Query map[string]interface{}

// Sets signature key.
func (K *APIClient) Signature(signature_key string) {
	K.secrets.signature_key = K.secrets.encrypt(signature_key)
}

// Sets client secret key.
func (K *APIClient) ClientSecret(client_secret_key string) {
	K.secrets.client_secret_key = K.secrets.encrypt(client_secret_key)
}

// Auth token.
type Auth struct {
	AccessToken  string `json:"access_token"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
	Expires      int64  `json:"expires_in"`
}

// Prints arrays for string and int arrays, when submitted to Queries or Form post.
func (K APIClient) spanner(input interface{}) string {
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
func (K APIClient) DecodeJSON(resp *http.Response, output interface{}) (err error) {
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
			defer snoop_request(&snoop_buffer)
		}
	} else {
		body = resp.Body
	}

	msg, err := ioutil.ReadAll(body)
	if err != nil {
		return err
	}

	// Final Error Check Against Response.
	ErrorCheck := func(resp *http.Response) (err error) {
		if resp.StatusCode < 200 && resp.StatusCode >= 300 {
			var e APIError
			e.Register(fmt.Sprintf("HTTP_STATUS_%d", resp.StatusCode), resp.Status)
			return e
		}
		return nil
	}

	if len(msg) > 0 {
		if K.ErrorScanner == nil {
			K.ErrorScanner = kwapiError
		}

		err = K.ErrorScanner(msg)
		if err != nil {
			return err
		}

		if output == nil {
			return ErrorCheck(resp)
		}

		err = json.Unmarshal(msg, output)
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
	}

	return ErrorCheck(resp)
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

func (s APIClient) Do(req *http.Request) (resp *http.Response, err error) {
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
	transport.ResponseHeaderTimeout = s.RequestTimeout

	client := http.Client{
		Transport: &transport,
		Timeout:   0, // Timeouts will be implemented with iotimeout.
	}

	return client.Do(req)
}

// New API Client Request.
func (s APIClient) NewRequest(username, method, path string) (req *http.Request, err error) {

	req, err = http.NewRequest(method, fmt.Sprintf("https://%s%s", s.Server, path), nil)
	if err != nil {
		return nil, err
	}

	req.URL.Host = s.Server
	req.URL.Scheme = "https"

	if s.AgentString != NONE {
		req.Header.Set("User-Agent", s.AgentString)
	}
	req.Header.Set("Referer", "https://"+s.Server+"/")

	if err := s.setToken(username, req); err != nil {
		return nil, err
	}

	return req, nil
}

// kiteworks API Call Wrapper
func (s APIClient) Call(username string, api_req APIRequest) (err error) {
	if s.limiter != nil {
		s.limiter <- struct{}{}
		defer func() { <-s.limiter }()
	}

	req, err := s.NewRequest(username, api_req.Method, api_req.Path)
	if err != nil {
		return err
	}

	if s.Snoop {
		Debug("[%s]: %s", s.Server, username)
		Debug("--> METHOD: \"%s\" PATH: \"%s\"", strings.ToUpper(api_req.Method), api_req.Path)
	}

	var body []byte

	var oauth_param_name string

	// Handle FTA Oauth which requires oauth_token be posed in a form.
	if api_req.Header != nil {
		if oauth_name, found := api_req.Header["OAUTH_PARAM"]; found {
			oauth_param_name = oauth_name[0]
			delete(api_req.Header, "OAUTH_PARAM")
			api_req.Params = SetParams(api_req.Params, PostForm{oauth_param_name: strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")})
			req.Header.Del("Authorization")
		}
	}

	for k, v := range api_req.Header {
		req.Header[k] = v
	}

	if s.Snoop {
		for k, v := range req.Header {
			if strings.HasPrefix(v[0], "Bearer") {
				v = []string{"Bearer [HIDDEN]"}
			}
			Debug("--> HEADER: %s: %s", k, v)
		}
	}

	for _, in := range api_req.Params {
		switch i := in.(type) {
		case PostForm:
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if s.Snoop {
				Debug("--> HEADER: Content-Type: [application/x-www-form-urlencoded]")
			}
			p := make(url.Values)
			for k, v := range i {
				p.Add(k, s.spanner(v))
				if s.Snoop {
					if k != oauth_param_name {
						Debug("\\-> POST PARAM: \"%s\" VALUE: \"%s\"", k, p[k])
					} else {
						Debug("\\-> POST PARAM: \"%s\" VALUE: [HIDDEN]", k)
					}
				}
			}
			body = []byte(p.Encode())
		case PostJSON:
			req.Header.Set("Content-Type", "application/json")
			if s.Snoop {
				Debug("--> HEADER: Content-Type: [application/json]")
			}
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
				q.Set(k, s.spanner(v))
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

	// reAuth attempts to get a new token on a token related error.
	reAuth := func(s *APIClient, username string, req *http.Request, orig_err error) error {
		if s.secrets.signature_key == nil {
			existing, err := s.TokenStore.Load(username)
			if err != nil {
				return err
			}
			if token, err := s.refreshToken(username, existing); err == nil {
				if err := s.TokenStore.Save(username, token); err != nil {
					return err
				}
				if err = s.setToken(username, req); err == nil {
					return nil
				}
			}
			s.TokenStore.Delete(username)
			Critical(fmt.Errorf("Token is no longer valid: %s", orig_err.Error()))
		}
		s.TokenStore.Delete(username)
		return s.setToken(username, req)
	}

	var (
		resp           *http.Response
		report_success bool
	)

	attempt := string(RandBytes(8))
	first_attempt := true

	// Retry calls on failure.
	for i := 0; i <= int(s.Retries); i++ {
		req.Body = ioutil.NopCloser(bytes.NewReader(body))
		resp, err = s.Do(req)

		if err == nil && resp != nil {
			err = s.DecodeJSON(resp, api_req.Output)
		}
		if err != nil {
			if s.isTokenError(err) {
				err = reAuth(&s, username, req, err)
				if err != nil {
					return
				} else {
					continue
				}
			}
			if s.isRetryError(err) || !IsAPIError(err) {
				report_success = true
				if first_attempt {
					if s.Retries > 0 {
						Debug("[#%s]: %s -> %s: %s (will retry)", attempt, username, api_req.Path, err.Error())
					}
					first_attempt = false
				} else {
					Debug("[#%s]: %s (retry %d/%d)", attempt, err.Error(), i, s.Retries)
				}
				s.BackoffTimer(uint(i))
				continue
			} else {
				return
			}
		} else {
			if report_success {
				Debug("[#%s]: Success!!! (retry %d/%d)", attempt, i, s.Retries)
			}
			return
		}
	}
	return
}

// Backs off subsequent attempts generating a pause between requests.
func (s APIClient) BackoffTimer(retry uint) {
	if retry < s.Retries {
		time.Sleep((time.Second * time.Duration(retry+1)) * time.Duration(retry+1))
	}
}

// Call handler which allows for easier getting of multiple-object arrays.
// An offset of -1 will provide all results, any positive offset will only return the requested results.
func (s APIClient) PageCall(username string, req APIRequest, offset, limit int) (err error) {

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
		if err = s.Call(username, req); err != nil {
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
