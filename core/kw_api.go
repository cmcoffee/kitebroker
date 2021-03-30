package core

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/cmcoffee/go-snuglib/iotimeout"
	"github.com/cmcoffee/go-snuglib/nfo"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Reads KW rest errors and interprets them.
func kwapiError(body []byte) APIError {
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

	e := new(apiError)

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

	if e.NoErrors() {
		return nil
	}

	return e
}

// KWAPI Wrapper for kiteworks.
type KWAPI struct {
	*APIClient
}

// kiteworks Session.
type KWSession struct {
	Username string
	db Database
	*KWAPI
}

// Wraps a session for specfiied user.
func (K *KWAPI) Session(username string) KWSession {
	return KWSession{username, K.db.Sub(username), K}
}

// Wrapper around Call to provide username.
func (K KWSession) Call(api_req APIRequest) (err error) {
	if api_req.Version <= 0 {
		api_req.Version = 13
	}
	if api_req.Header == nil {
		api_req.Header = make(map[string][]string)
	}

	api_req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", api_req.Version))
	api_req.Username = K.Username
	
	return K.APIClient.Call(api_req)
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

// Authenticate with server.
func (K *KWAPI) Authenticate(username string) (*KWSession, error) {
	return K.authenticate(username, true, false)
}

// Login to server
func (K *KWAPI) Login(username string) (*KWSession, error) {
	if username != NONE {
		session := K.Session(username)
		token, err := K.TokenStore.Load(username)
		if err != nil {
			return nil, err
		}
		if token != nil {
			if token.Expires <= time.Now().Unix() {
				// First attempt to use a refresh token if there is one.
				token, err = session.refreshToken(username, token)
				if err != nil {
					if K.secrets.signature_key == nil {
						Debug("Unable to use refresh token: %v", err)
					}
					token = nil
				} else {
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

// Set User Credentials for kw_api.
func (K *KWAPI) authenticate(username string, permit_change, auth_loop bool) (*KWSession, error) {
	if K.TokenStore == nil {
		return nil, fmt.Errorf("APIClient: NewToken not initalized.")
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

	return nil, fmt.Errorf("Unable to obtain a token for specified user.")
}

func (K *KWAPI) KWNewToken(username string) (auth *Auth, err error) {
	return K.kwNewToken(username, NONE)
}

// Generate a new Bearer token from kiteworks.
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
		postform.Add("grant_type", "password")
		postform.Add("username", username)
		postform.Add("password", password)
	} else {
		signature := K.GetSignature()
		randomizer := rand.New(rand.NewSource(int64(time.Now().UnixNano())))
		nonce := randomizer.Int() % 999999
		timestamp := int64(time.Now().Unix())

		base_string := fmt.Sprintf("%s|@@|%s|@@|%d|@@|%d", client_id, username, timestamp, nonce)

		mac := hmac.New(sha1.New, []byte(signature))
		mac.Write([]byte(base_string))
		signature = hex.EncodeToString(mac.Sum(nil))

		auth_code := fmt.Sprintf("%s|@@|%s|@@|%d|@@|%d|@@|%s",
			base64.StdEncoding.EncodeToString([]byte(client_id)),
			base64.StdEncoding.EncodeToString([]byte(username)),
			timestamp, nonce, signature)

		postform.Add("grant_type", "authorization_code")
		postform.Add("code", auth_code)
	}

	if K.Snoop {
		Debug("[kiteworks]: %s", username)
		Debug("--> ACTION: \"POST\" PATH: \"%s\"", path)
		for k, v := range *postform {
			if k == "grant_type" || k == "redirect_uri" || k == "scope" {
				Debug("\\-> POST PARAM: %s VALUE: %s", k, v)
			} else {
				Debug("\\-> POST PARAM: %s VALUE: [HIDDEN]", k)
			}
		}
	}

	req.Body = ioutil.NopCloser(bytes.NewReader([]byte(postform.Encode())))
	req.Body = iotimeout.NewReadCloser(req.Body, K.RequestTimeout)
	defer req.Body.Close()

	err = K.Fulfill(&APISession{NONE, req}, nil, &auth)
	if err != nil {
		return nil, err
	}

	auth.Expires = time.Now().Add(time.Duration(auth.Expires) * time.Second).Unix()
	return
}
