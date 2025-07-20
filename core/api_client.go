package core

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/cmcoffee/snugforge/iotimeout"
	"github.com/cmcoffee/snugforge/mimebody"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// APIClient represents a client for interacting with the Kiteworks API.
// It encapsulates configuration and handles authentication, request sending,
// and error handling for API calls.
type APIClient struct {
	Server          string                               // kiteworks host name.
	ApplicationID   string                               // Application ID set for kiteworks custom app.
	RedirectURI     string                               // Redirect URI for kiteworks custom app.
	AgentString     string                               // Agent-String header for calls to kiteworks.
	VerifySSL       bool                                 // Verify certificate for connections.
	ProxyURI        string                               // Proxy for outgoing https requests.
	RequestTimeout  time.Duration                        // Timeout for request to be answered from kiteworks server.
	ConnectTimeout  time.Duration                        // Timeout for TLS connection to kiteworks server.
	MaxChunkSize    int64                                // Max Upload chunk size in bytes, min = 1M, max = 68M
	Retries         uint                                 // Max retries on a failed call
	TokenStore      TokenStore                           // TokenStore for reading and writing auth tokens securely.
	db              Database                             // Database for APIClient.
	secrets         api_secrets                          // Encrypted config options such as signature token, client secret key.
	limiter         chan struct{}                        // Implements a limiter for API calls/transfers to/from appliance.
	trans_limiter   chan struct{}                        // Implements a file transfer limiter.
	NewToken        func(username string) (*Auth, error) // Provides new access_token.
	ErrorScanner    func(body []byte) APIError           // Reads body of response and interprets any errors.
	RetryErrorCodes []string                             // Error codes ("ERR_INTERNAL_SERVER_ERROR"), that should induce a retry. (will automatically try TokenErrorCodes as well)
	TokenErrorCodes []string                             // Error codes ("ERR_INVALID_GRANT"), that should indicate a problem with the current access token.
	token_lock      sync.Mutex                           // Mutex for dealing with token expiry.
}

// _is_retry_error is a bitmask for retryable errors.
// _is_token_error is a bitmask for token-related errors.
const (
	_is_retry_error = 1 << iota
	_is_token_error
)

// APIRetryEngine manages retries for API calls.
// It encapsulates an API client, retry attempt count,
// unique ID, user information, task context, and
// additional error codes to consider for retries.
type APIRetryEngine struct {
	api                     APIClient
	attempt                 uint
	uid                     string
	user                    string
	task                    string
	addtl_retry_error_codes []string
}

// InitRetry / InitRetry initializes and returns a new APIRetryEngine.
// / It takes a username, task description, and optional additional
// / error codes for retry logic.
func (s *APIClient) InitRetry(username string, task_description string, addtl_retry_error_codes ...string) *APIRetryEngine {
	return &APIRetryEngine{
		*s,
		0,
		string(RandBytes(8)),
		username,
		task_description,
		addtl_retry_error_codes,
	}
}

// CheckForRetry determines if a retry should be attempted based on the error.
// It considers retry policies, error types, and retry limits.
func (a *APIRetryEngine) CheckForRetry(err error) bool {
	var flag BitFlag

	if err == nil {
		if a.attempt > 0 {
			Debug("[#%s]: %s -> %v: success!! (retry %d/%d)", a.uid, a.user, a.task, a.attempt, a.api.Retries)
		}
		return false
	}

	if a.attempt > a.api.Retries {
		Debug("[#%s] %s -> %v: %s (exhausted retries)", a.uid, a.user, a.task, err.Error())
		return false
	}

	if !IsBlank(a.user) && a.api.isTokenError(a.user, err) {
		flag.Set(_is_token_error)
		flag.Set(_is_retry_error)
	} else {
		if a.api.isRetryError(err) || !IsAPIError(err) || (len(a.addtl_retry_error_codes) > 0 && IsAPIError(err, a.addtl_retry_error_codes[0:]...)) {
			flag.Set(_is_retry_error)
		}
	}

	if flag.Has(_is_token_error | _is_retry_error) {
		if a.attempt == 0 {
			if a.api.Retries > 0 {
				Debug("[#%s] %s -> %s: %s (will retry)", a.uid, a.user, a.task, err.Error())
			}
		} else {
			Debug("[#%s] %s -> %s: %s (retry %d/%d)", a.uid, a.user, a.task, err.Error(), a.attempt, a.api.Retries)
		}
	}

	if flag.Has(_is_retry_error) {
		a.api.BackoffTimer(uint(a.attempt))
		a.attempt++
		return true
	}

	return false
}

// Fulfill performs an API request and decodes the response.
// It handles retries and timeouts, and closes the response body.
func (s *APIClient) Fulfill(username string, req *http.Request, output interface{}) (err error) {
	var dont_retry bool

	close_resp := func(resp *http.Response) {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}

	var resp *http.Response

	if req.GetBody == nil && req.Body != nil {
		dont_retry = true
	} else {
		orig_body := req.GetBody
		req.GetBody = func() (io.ReadCloser, error) {
			body, err := orig_body()
			if err != nil {
				return nil, err
			}
			return iotimeout.NewReadCloser(body, s.RequestTimeout), nil
		}
	}

	retry := s.InitRetry(username, req.URL.Path)

	for {
		if req.GetBody != nil {
			req.Body, err = req.GetBody()
			if err != nil {
				return err
			}
		}

		resp, err = s.SendRequest(username, req)

		if err == nil && resp != nil {
			err = DecodeJSON(resp, output)
		}

		if retry.CheckForRetry(err) {
			if !dont_retry {
				close_resp(resp)
				continue
			}
		}
		close_resp(resp)
		return err
	}
}

// SetDatabase sets the database for the API client.
// It also initializes the TokenStore using a sub-collection.
func (s *APIClient) SetDatabase(db Database) {
	s.db = db
	s.TokenStore = KVLiteStore(db.Sub("tokens"))
}

// SetLimiter configures the rate limiter for API calls.
// It initializes or resets the limiter channel with the given
// maximum number of allowed calls. If max_calls is invalid,
// it defaults to 1.
func (s *APIClient) SetLimiter(max_calls int) {
	if max_calls <= 0 {
		max_calls = 1
	}
	if s.limiter == nil {
		s.limiter = make(chan struct{}, max_calls)
	}
}

// GetLimit returns the current rate limit capacity.
// Returns 1 if the limiter is not initialized.
func (s *APIClient) GetLimit() int {
	if s.limiter != nil {
		return cap(s.limiter)
	}
	return 1
}

// SetTransferLimiter configures the transfer limiter channel.
// It initializes or adjusts the channel capacity to control
// concurrent transfer operations.  A value less than or equal
// to zero defaults the capacity to 1.
func (s *APIClient) SetTransferLimiter(max_transfers int) {
	if max_transfers <= 0 {
		max_transfers = 1
	}
	if s.trans_limiter == nil {
		s.trans_limiter = make(chan struct{}, max_transfers)
	}
}

// GetTransferLimit returns the transfer limit.
func (s *APIClient) GetTransferLimit() int {
	if s.trans_limiter != nil {
		return cap(s.trans_limiter)
	}
	return 1
}

// TokenStore interface for storing and retrieving authentication tokens.
// It provides methods to save, load, and delete tokens associated with a username.
type TokenStore interface {
	Save(username string, auth *Auth) error
	Load(username string) (*Auth, error)
	Delete(username string) error
}

// kvLiteStore provides a simplified interface for key-value storage.
type kvLiteStore struct {
	Table
}

// KVLiteStore returns a new kvLiteStore.
// It takes a Database as input and returns a pointer
// to a kvLiteStore instance initialized with the tokens table.
func KVLiteStore(input Database) *kvLiteStore {
	return &kvLiteStore{input.Table("tokens")}
}

// Save persists the authentication details for a given username.
// It encrypts and stores the Auth struct using the underlying table.
func (T *kvLiteStore) Save(username string, auth *Auth) error {
	T.Table.CryptSet(username, &auth)
	return nil
}

// Load retrieves an Auth object by username.
// It returns the Auth object and nil error if found,
// otherwise returns nil and nil error.
func (T *kvLiteStore) Load(username string) (*Auth, error) {
	var auth *Auth
	T.Table.Get(username, &auth)
	return auth, nil
}

// Delete removes the entry associated with the given username.
func (T *kvLiteStore) Delete(username string) error {
	T.Table.Unset(username)
	return nil
}

// api_secrets holds encrypted configuration options such as signature token,
// client secret key, and key for encrypting/decrypting data.
type api_secrets struct {
	key               []byte
	signature_key     []byte
	client_secret_key []byte
}

// Encrypts the given string using AES with CFB encryption.
// It initializes the key if it's nil.
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

// Decrypts the given ciphertext using the stored key.
// Returns an empty string if the key is not set.
func (k *api_secrets) decrypt(input []byte) string {
	if k.key == nil {
		return NONE
	}

	output := make([]byte, len(input))

	block, _ := aes.NewCipher(k.key)
	cipher.NewCFBDecrypter(block, k.key[0:block.BlockSize()]).XORKeyStream(output, input)

	return string(output)
}

// GetSignature retrieves the API signature.
// It decrypts the signature key if it exists.
func (s *APIClient) GetSignature() string {
	var sig string

	if s.secrets.signature_key != nil {
		sig = s.secrets.decrypt(s.secrets.signature_key)
	}

	return sig
}

// GetClientSecret retrieves the client secret.
// It decrypts the stored client secret if available.
func (s *APIClient) GetClientSecret() string {
	var secret string

	if s.secrets.client_secret_key != nil {
		secret = s.secrets.decrypt(s.secrets.client_secret_key)
	}

	return secret
}

// APIRequest represents a request to be made to the API.
type APIRequest struct {
	Username string
	Version  int
	Header   http.Header
	Method   string
	Path     string
	Params   []interface{}
	Output   interface{}
}

// SetPath is a function that formats strings into paths.
var SetPath = fmt.Sprintf

// SetParams accepts variable parameters and organizes them into a slice
// of interfaces for further processing. It handles Query, PostJSON,
// PostForm, and MimeBody types, accumulating them into an output slice.
func SetParams(vars ...interface{}) (output []interface{}) {
	if len(vars) == 0 {
		return nil
	}
	var (
		post_json PostJSON
		query     Query
		form      PostForm
		mb        MimeBody
	)

	mb_set := false

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
		case MimeBody:
			mb = x
			mb_set = true
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
	if mb_set {
		output = append(output, mb)
	}
	return
}

// SetToken sets the authentication token for the given username on the request.
// It retrieves, validates, and updates the token, potentially using a refresh token.
func (s *APIClient) SetToken(username string, req *http.Request) (err error) {
	if s.TokenStore == nil {
		return fmt.Errorf("APIClient: TokenStore not initialized.")
	}

	s.token_lock.Lock()
	defer s.token_lock.Unlock()

	token, err := s.TokenStore.Load(username)
	if err != nil {
		return err
	}

	// If we find a token, check if it's still valid.
	if token != nil {
		if token.Expires <= time.Now().Unix() {
			Debug("Access token expired, using refresh token instead.")
			// First attempt to use a refresh token if there is one.
			err = s.refreshToken(username, token)
			if err != nil && s.secrets.signature_key == nil {
				Debug("Unable to use refresh token: %v", err)
				Fatal("Access token has expired, must reauthenticate for new access token.")
			}
			err = nil
		}
	}

	if token == nil {
		if s.NewToken == nil {
			return fmt.Errorf("APIClient: NewToken not initialized.")
		}
		s.TokenStore.Delete(username)
		token, err = s.NewToken(username)
		if err != nil {
			return err
		}
	}

	if token != nil {
		req.Header.Set("KiteBrokerUser", username)
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		if err := s.TokenStore.Save(username, token); err != nil {
			return err
		}
	}
	return nil
}

// refreshToken obtains a new access token using the refresh token.
// It updates the access token, refresh token, and expiration time
// in the provided auth object. Returns an error if the refresh
// token is invalid or the request fails.
func (s *APIClient) refreshToken(username string, auth *Auth) error {
	if auth == nil || auth.RefreshToken == NONE {
		return fmt.Errorf("No refresh token found for %s.", username)
	}
	Debug("Using refresh token to obtain new token.")

	path := fmt.Sprintf("https://%s/oauth/token", s.Server)

	req, err := http.NewRequest(http.MethodPost, path, nil)
	if err != nil {
		return err
	}

	http_header := make(http.Header)
	http_header.Set("Content-Type", "application/x-www-form-urlencoded")
	if s.AgentString != NONE {
		http_header.Set("User-Agent", s.AgentString)
	}

	req.Header = http_header

	client_id := s.ApplicationID

	postform := &url.Values{
		"client_id":     {client_id},
		"client_secret": {s.secrets.decrypt(s.secrets.client_secret_key)},
		"grant_type":    {"refresh_token"},
		"refresh_token": {auth.RefreshToken},
	}

	Trace("[%s]: %s", s.Server, username)
	Trace("--> ACTION: \"POST\" PATH: \"%s\"", path)
	for k, v := range *postform {
		if k == "grant_type" || k == "RedirectURI" || k == "scope" {
			Trace("\\-> POST PARAM: %s VALUE: %s", k, v)
		} else {
			Trace("\\-> POST PARAM: %s VALUE: [HIDDEN]", k)
		}
	}

	var new_token struct {
		AccessToken  string      `json:"access_token"`
		Scope        string      `json:"scope"`
		RefreshToken string      `json:"refresh_token"`
		Expires      interface{} `json:"expires_in"`
	}

	req.Body = io.NopCloser(bytes.NewReader([]byte(postform.Encode())))
	req.Body = iotimeout.NewReadCloser(req.Body, s.RequestTimeout)
	defer req.Body.Close()

	resp, err := s.SendRequest(NONE, req)
	if err != nil {
		return err
	}

	if err := DecodeJSON(resp, &new_token); err != nil {
		return err
	}

	if new_token.Expires != nil {
		expiry, _ := strconv.ParseInt(fmt.Sprintf("%v", new_token.Expires), 0, 64)
		auth.Expires = time.Now().Unix() + expiry
	}

	auth.AccessToken = new_token.AccessToken
	auth.RefreshToken = new_token.RefreshToken
	auth.Scope = new_token.Scope

	return nil
}

// PostJSON is a map[string]interface{} type used for constructing JSON payloads.
type PostJSON map[string]interface{}

// PostForm is a map of string to interface{}, representing form data.
type PostForm map[string]interface{}

// Query is a map of string keys to interface{} values,
// representing a set of query parameters.
type Query map[string]interface{}

// MimeBody represents a file part for multipart form data.
// It holds metadata and the file source for upload.
type MimeBody struct {
	FieldName string
	FileName  string
	Source    io.ReadCloser
	AddFields map[string]string
	Limit     int64
}

// Signature sets the signature key used for authenticating requests.
// It encrypts the provided key before storing it.
func (s *APIClient) Signature(signature_key string) {
	s.secrets.signature_key = s.secrets.encrypt(signature_key)
}

// ClientSecret sets the client secret key, encrypting it for storage.
func (s *APIClient) ClientSecret(client_secret_key string) {
	s.secrets.client_secret_key = s.secrets.encrypt(client_secret_key)
}

// Auth represents the authentication token.
type Auth struct {
	AccessToken  string `json:"access_token"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
	Expires      int64  `json:"expires_in"`
}

// spanner converts various input types to a comma-separated string.
func (s *APIClient) spanner(input interface{}) string {
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

// readCloser combines an io.Reader with a function to close resources.
type readCloser struct {
	closer func() error
	io.Reader
}

// Close closes the underlying reader.
func (r readCloser) Close() (err error) {
	return r.closer()
}

// newReadCloser returns a new io.ReadCloser from an io.Reader and
// a close function.
func newReadCloser(src io.Reader, close_func func() error) io.ReadCloser {
	return readCloser{
		close_func,
		src,
	}
}

// snoopReader reads at least min bytes from src, returning a reader
// for the initial bytes and a new ReadCloser that reads from the
// original source after the initial bytes.  If an error occurs
// during the initial read, it returns the error.
func snoopReader(src io.ReadCloser, min int) (snoop_reader io.Reader, output io.ReadCloser, err error) {

	var n int
	buffer := make([]byte, min)

	n, err = io.ReadAtLeast(src, buffer, min)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, src, err
	}

	buffer = buffer[0:n]

	snoop_reader = bytes.NewReader(buffer)

	if n == min {
		output = readCloser{src.Close, io.MultiReader(bytes.NewReader(buffer), src)}
	} else {
		output = readCloser{src.Close, bytes.NewReader(buffer)}
	}

	err = nil

	return
}

// respErrorCheck checks the response for errors and returns an error if found.
// It reads the first 64k of the response body to scan for errors.
func (s *APIClient) respErrorCheck(resp *http.Response) (err error) {

	var (
		snoop_buffer bytes.Buffer
		snoop_reader io.Reader
	)

	if resp == nil {
		return nil
	}

	// Reads the first 64k of the resp body for any errors.
	snoop_reader, resp.Body, err = snoopReader(iotimeout.NewReadCloser(resp.Body, s.RequestTimeout), 65536)
	if err != nil {
		return err
	}

	snoop_reader = io.TeeReader(snoop_reader, &snoop_buffer)

	msg, err := io.ReadAll(snoop_reader)
	if err != nil {
		return err
	}

	if s.ErrorScanner == nil {
		s.ErrorScanner = kwapiError
	}

	e := s.ErrorScanner(msg)
	if !e.noError() {
		snoop_response(resp.Status, &snoop_buffer)
		return e
	}

	if resp.StatusCode >= 200 && resp.StatusCode <= 300 {
		return nil
	}

	snoop_response(resp.Status, &snoop_buffer)

	e.Register(fmt.Sprintf("HTTP_STATUS_%d", resp.StatusCode), resp.Status)
	return e
}

// DecodeJSON decodes a JSON response from an HTTP response into the given output.
// It also snoops the response body for logging purposes.
func DecodeJSON(resp *http.Response, output interface{}) (err error) {
	var (
		snoop_buffer bytes.Buffer
		body         io.Reader
	)

	defer resp.Body.Close()

	body = io.TeeReader(resp.Body, &snoop_buffer)
	defer snoop_response(resp.Status, &snoop_buffer)

	msg, err := io.ReadAll(body)

	if output == nil {
		return nil
	}

	if err != nil {
		return err
	}

	if len(msg) > 0 {
		err = json.Unmarshal(msg, output)
		if err == io.EOF {
			return nil
		}

		if err != nil {
			return fmt.Errorf("I cannot understand what %s is saying: %s", resp.Request.URL.Host, err.Error())
		}
	}

	return
}

// snoop_response logs the response status and body, redacting tokens.
// It decodes the response body as JSON, replaces sensitive values
// like "refresh_token" and "access_token" with "[HIDDEN]", and
// then logs the modified JSON. If the body is not valid JSON,
// it logs the raw body string.
func snoop_response(respStatus string, body *bytes.Buffer) {
	Trace("<-- RESPONSE STATUS: %s", respStatus)

	var snoop_generic map[string]interface{}
	dec := json.NewDecoder(body)
	str := body.String()
	if err := dec.Decode(&snoop_generic); err != nil {
		Trace("<-- RESPONSE BODY: \n%s\n", str)
		return
	}
	if snoop_generic != nil {
		for v := range snoop_generic {
			switch v {
			case "refresh_token":
				fallthrough
			case "access_token":
				snoop_generic[v] = "[HIDDEN]"
			}
		}
	}
	o, _ := json.MarshalIndent(&snoop_generic, "", "  ")
	Trace("<-- RESPONSE BODY: \n%s\n", string(o))
	return
}

// SendRequest sends an HTTP request with configured settings.
// It handles SSL verification, proxy configuration, timeouts,
// and token setting based on the provided username. It also
// wraps the request body with a timeout reader.
func (s *APIClient) SendRequest(username string, req *http.Request) (resp *http.Response, err error) {
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

	transport.DialContext = (&net.Dialer{
		Timeout: s.ConnectTimeout,
	}).DialContext

	transport.TLSHandshakeTimeout = s.ConnectTimeout
	transport.ResponseHeaderTimeout = s.RequestTimeout
	transport.DisableKeepAlives = true

	client := http.Client{
		Transport: &transport,
	}

	// Must check token before sending request.
	if !IsBlank(username) {
		err = s.SetToken(username, req)
		if err != nil {
			return nil, err
		}
	}

	if req.Body != nil {
		req.Body = iotimeout.NewReadCloser(req.Body, s.RequestTimeout)
		client.Timeout = 0
	}

	resp, err = client.Do(req)
	if err == nil {
		err = s.respErrorCheck(resp)
	}

	return
}

// NewRequest creates and configures an HTTP request.
// It sets the server address, scheme, user agent, and referrer.
func (s *APIClient) NewRequest(method, path string) (req *http.Request, err error) {

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
	req.Close = true

	return req, nil
}

// Call performs the API request and returns any error encountered.
func (s *APIClient) Call(api_req APIRequest) (err error) {
	if s.limiter != nil {
		s.limiter <- struct{}{}
		defer func() { <-s.limiter }()
	}

	req, err := s.NewRequest(api_req.Method, api_req.Path)
	if err != nil {
		return err
	}

	Trace("[%s]: %s", s.Server, api_req.Username)
	Trace("--> METHOD: \"%s\" PATH: \"%s\"", strings.ToUpper(api_req.Method), api_req.Path)

	var body []byte

	for k, v := range api_req.Header {
		req.Header[k] = v
	}

	for k, v := range req.Header {
		if strings.HasPrefix(v[0], "Bearer") {
			v = []string{"Bearer [HIDDEN]"}
		}
		Trace("--> HEADER: %s: %s", k, v)
	}

	skip_getBody := false

	for _, in := range api_req.Params {
		switch i := in.(type) {
		case PostForm:
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			Trace("--> HEADER: Content-Type: [application/x-www-form-urlencoded]")
			p := make(url.Values)
			for k, v := range i {
				p.Add(k, s.spanner(v))
				Trace("\\-> POST PARAM: \"%s\" VALUE: \"%s\"", k, p[k])
			}
			body = []byte(p.Encode())
		case PostJSON:
			req.Header.Set("Content-Type", "application/json")
			Trace("--> HEADER: Content-Type: [application/json]")

			json, err := json.Marshal(i)
			if err != nil {
				return err
			}

			Trace("\\-> POST JSON: %s", string(json))
			body = json
		case Query:
			q := req.URL.Query()
			for k, v := range i {
				q.Set(k, s.spanner(v))
				Trace("\\-> QUERY: %s=%s", k, q[k])
			}
			req.URL.RawQuery = q.Encode()
		case MimeBody:
			req.Body = i.Source
			mimebody.ConvertFormFile(req, i.FieldName, i.FileName, i.AddFields, i.Limit)
			skip_getBody = true
			Trace("--> HEADER: Content-Type: [multipart/form-data]")
			for k, v := range i.AddFields {
				Trace("\\-> FORM FIELD: %s=%s", k, v)
			}
			if !IsBlank(i.FileName) {
				Trace("\\-> FORM DATA: name=\"%s\"; filename=\"%s\"", i.FieldName, i.FileName)
			} else {
				Trace("\\-> FORM DATA: name=\"%s\"", i.FieldName)
			}
		case nil:
			continue
		default:
			return fmt.Errorf("Unknown request exception.")
		}
	}

	if !skip_getBody {
		req.GetBody = GetBodyBytes(body)
	}

	return s.Fulfill(api_req.Username, req, api_req.Output)
}

// BackoffTimer pauses execution with an increasing delay on retry.
// The delay is calculated as (retry + 1)^2 seconds.
// No delay occurs if the maximum number of retries has been reached.
func (s *APIClient) BackoffTimer(retry uint) {
	if retry < s.Retries {
		time.Sleep((time.Second * time.Duration(retry+1)) * time.Duration(retry+1))
	}
}

// PageCall paginates through API responses, handling offset and limits.
// It fetches data in chunks based on the provided offset and limit,
// accumulating the results until either the end of the dataset is
// reached or an error occurs. It returns the accumulated data in
// the original output structure.
func (s *APIClient) PageCall(req APIRequest, offset, limit int) (err error) {

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

	// Take stack of results we received and decode it back to the original object.
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
