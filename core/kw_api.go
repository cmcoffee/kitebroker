package core

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/cmcoffee/snugforge/iotimeout"
	"github.com/cmcoffee/snugforge/nfo"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DEFAULT_KWAPI_VERSION defines the default KiteWorks API version.
const DEFAULT_KWAPI_VERSION = 28

// kwapiError parses a Kiteworks API error response body and
// registers the errors in an APIError object.
func kwapiError(body []byte) (e APIError) {
	// kiteworks API Error
	type KiteErr struct {
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
		Errors    []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}

	var kite_err *KiteErr
	json.Unmarshal(body, &kite_err)

	if kite_err != nil {
		for _, v := range kite_err.Errors {
			if strings.Contains(v.Code, "ERR_INTERNAL_") {
				e.Register("ERR_INTERNAL_SERVER_ERROR", "")
			}
			e.Register(v.Code, v.Message)
		}
		if kite_err.ErrorDesc != NONE {
			e.Register(kite_err.Error, kite_err.ErrorDesc)
		}
	}

	return
}

// KWAPI represents the Kiteworks API client.
// It encapsulates the API client and provides methods for interacting with the Kiteworks API.
type KWAPI struct {
	*APIClient
}

// KWSession represents a user session with access to the KWAPI.
// It encapsulates the username, a scoped database instance, and a KWAPI instance.
type KWSession struct {
	Username string
	db       Database
	*KWAPI
}

// Session creates a new session for the given username.
func (K *KWAPI) Session(username string) KWSession {
	return KWSession{username, K.db.Sub(username), K}
}

// Call / Call invokes the API client with the given request.
// It sets default values for version and header if not provided.
// It also sets the username for the request.
func (K KWSession) Call(api_req APIRequest) (err error) {
	if api_req.Version <= 0 {
		api_req.Version = DEFAULT_KWAPI_VERSION
	}
	if api_req.Header == nil {
		api_req.Header = make(map[string][]string)
	}

	api_req.Header.Set("X-Accellion-Version", fmt.Sprintf("%v", api_req.Version))
	api_req.Username = K.Username

	return K.APIClient.Call(api_req)
}

// DataCall makes a series of API calls to retrieve data with offset and limit.
func (K KWSession) DataCall(req APIRequest, offset, limit int) (err error) {

	output := req.Output
	params := req.Params

	if limit <= 0 {
		limit = 1000
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
		if err = K.Call(req); err != nil {
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

// Authenticate attempts to authenticate a user and return a session.
// It handles both initial authentication and token refreshing.
func (K *KWAPI) Authenticate(username string) (*KWSession, error) {
	return K.authenticate(username, true, false)
}

// Login attempts to log in a user and return a session.
func (K *KWAPI) Login(username string) (*KWSession, error) {
	if username != NONE {
		session := K.Session(username)
		token, err := K.TokenStore.Load(username)
		if err != nil {
			return nil, err
		}
		if token != nil {
			if token.Expires <= time.Now().Unix() {
				Debug("Access token expired, using refresh token instead.")
				// First attempt to use a refresh token if there is one.
				err = session.refreshToken(username, token)
				if err != nil {
					if K.secrets.signature_key == nil {
						Debug("Unable to use refresh token: %v", err)
					}
					token = nil
				} else {
					Debug("Refresh token success.")
					if err := K.TokenStore.Save(username, token); err != nil {
						return &session, err
					}
					return &session, nil
				}
			} else {
				return &session, nil
			}
		}
	}
	return K.authenticate(username, true, true)
}

// Authenticate attempts to authenticate a user and return a session.
// It handles both initial authentication and token refreshing.
func (K *KWAPI) authenticate(username string, permit_change, auth_loop bool) (*KWSession, error) {
	if K.TokenStore == nil {
		return nil, fmt.Errorf("APIClient: NewToken not initialized.")
	}

	if IsBlank(K.GetSignature()) {
		defer PleaseWait.Hide()
		Stdout("### %s authentication ###\n\n", K.Server)
		for {
			PleaseWait.Hide()
			if username == NONE || permit_change {
				username = strings.ToLower(nfo.GetInput(" -> Account Login(email): "))
			} else {
				Stdout(" -> Account Login(email): %s", username)
			}
			password := nfo.GetSecret(" -> Account Password: ")
			if password == NONE {
				err := fmt.Errorf("Blank password provided.")
				if !auth_loop {
					return nil, err
				}
				Stdout("\n")
				Err("Blank password provided.\n\n")
				continue
			}
			PleaseWait.Show()
			auth, err := K.kwNewToken(username, password)
			if err != nil {
				if !auth_loop {
					return nil, err
				}
				Stdout("\n")
				Err(fmt.Sprintf("%s\n\n", err.Error()))
				continue
			} else {
				session := K.Session(username)
				if err := K.TokenStore.Save(username, auth); err != nil {
					return &session, err
				}
				Stdout("\n")
				return &session, nil
			}
		}
	} else {
		if K.NewToken == nil {
			return nil, fmt.Errorf("APIClient not fully initialized, missing NewToken function.")
		}
		auth, err := K.NewToken(username)
		if err != nil {
			return nil, err
		}
		session := K.Session(username)
		if err := K.TokenStore.Save(username, auth); err != nil {
			return &session, err
		}
		return &session, nil
	}
}

func (K *KWAPI) KWNewToken(username string) (auth *Auth, err error) {
	return K.kwNewToken(username, NONE)
}

// kwNewToken obtains a new authentication token for a given user.
// It handles both password-based and signature-based authentication.
func (K *KWAPI) kwNewToken(username, password string) (auth *Auth, err error) {
	path := fmt.Sprintf("https://%s/oauth/token", K.Server)

	req, err := http.NewRequest(http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}

	http_header := make(http.Header)
	http_header.Set("Content-Type", "application/x-www-form-urlencoded")
	http_header.Set("User-Agent", K.AgentString)

	req.Header = http_header

	client_id := K.ApplicationID

	postform := &url.Values{
		"client_id":     {client_id},
		"client_secret": {K.GetClientSecret()},
		"redirect_uri":  {K.RedirectURI},
	}

	signature := K.GetSignature()

	if IsBlank(signature) {
		if IsBlank(password) {
			_, err := K.authenticate(username, false, false)
			if err != nil {
				return nil, err
			}
			auth, err = K.TokenStore.Load(username)
			if err != nil {
				return nil, err
			}
			return auth, nil
		}
		// For client ccrednetial authentication, grant_type needs to be password.
		postform.Add("grant_type", "password")
		postform.Add("username", username)
		postform.Add("password", password)
	} else {
		// Retrieve the signature key from our config.
		signature := K.GetSignature()

		// Spin up randomizer seed on unix epoch in nano seconds for nonce.
		randomizer := rand.New(rand.NewSource(int64(time.Now().UnixNano())))
		nonce := randomizer.Int() % 999999

		// Create timestamp
		timestamp := int64(time.Now().Unix())

		// Create our base string for authentication, including client_id, the email, timestamp on nonce, using |@@| as seperators.
		base_string := fmt.Sprintf("%s|@@|%s|@@|%d|@@|%d", client_id, username, timestamp, nonce)

		// Create a new Keyed-Hash Message Authentication Code(HMAC), using an SHA1 Hash, using the signature key for the HMAC.
		mac := hmac.New(sha1.New, []byte(signature))
		// Now write write the base string from above through the hmac interface.
		mac.Write([]byte(base_string))
		// Hex encode the resulting SUM of the HMAC.
		signature = hex.EncodeToString(mac.Sum(nil))

		// Auth code sent to the oauth/token endpoint will consist will be similar to the original base string, but base64 encoded client_id,
		// as well as the addition of the now hashed string tacked on the end, which the server will use to verify.
		auth_code := fmt.Sprintf("%s|@@|%s|@@|%d|@@|%d|@@|%s",
			base64.StdEncoding.EncodeToString([]byte(client_id)),
			base64.StdEncoding.EncodeToString([]byte(username)),
			timestamp, nonce, signature)

		postform.Add("grant_type", "authorization_code")
		postform.Add("code", auth_code)
	}

	Trace("[kiteworks]: %s", username)
	Trace("--> ACTION: \"POST\" PATH: \"%s\"", path)
	for k, v := range *postform {
		if k == "grant_type" || k == "redirect_uri" || k == "scope" {
			Trace("\\-> POST PARAM: %s VALUE: %s", k, v)
		} else {
			Trace("\\-> POST PARAM: %s VALUE: [HIDDEN]", k)
		}
	}

	req.Body = io.NopCloser(bytes.NewReader([]byte(postform.Encode())))
	req.Body = iotimeout.NewReadCloser(req.Body, K.RequestTimeout)
	defer req.Body.Close()

	resp, err := K.SendRequest(NONE, req)
	if err != nil {
		return nil, err
	}

	if err := DecodeJSON(resp, &auth); err != nil {
		return nil, err
	}

	auth.Expires = time.Now().Add(time.Duration(auth.Expires) * time.Second).Unix()
	return
}
