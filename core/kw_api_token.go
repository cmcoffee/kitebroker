package core

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (K *KWAPI) Authenticate(username string) (*KWSession, error) {
	return K.authenticate(username, true, false)
}

func (K *KWAPI) AuthLoop(username string) (*KWSession, error) {
	return K.authenticate(username, true, true)
}

// Set User Credentials for kw_api.
func (K *KWAPI) authenticate(username string, permit_change, auth_loop bool) (*KWSession, error) {
	K.testTokenStore()

	if username != NONE {
		session := K.Session(username)
		token, err := K.TokenStore.Load(username)
		if err != nil {
			return nil, err
		}
		if token != nil {
			if token.Expires < time.Now().Add(time.Duration(5*time.Minute)).Unix() {
				// First attempt to use a refresh token if there is one.
				token, err = K.refreshToken(username, token)
				if err != nil {
					if K.secrets.signature_key == nil {
						Notice("Unable to use refresh token, must reauthenticate for new access token: %s", err.Error())
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

	var report_success bool

	if K.secrets.signature_key == nil {
		Stdout("--- %s authentication ---\n\n", K.Server)
		for {
			if username == NONE {
				username = strings.ToLower(GetInput("-> username: "))
			} else {
				Stdout("-> username: %s", username)
			}
			report_success = true
			password := GetSecret("-> password: ")
			if password == NONE {
				err := fmt.Errorf("Blank password provided.")
				if !auth_loop {
					return nil, err
				}
				Stdout("\n")
				Err("Blank password provided.\n\n")
				if permit_change {
					username = NONE
				}
				continue
			}

			auth, err := K.newToken(username, password)
			if err != nil {
				if permit_change {
					username = NONE
				}
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
				} else {
					if report_success {
						Stdout("\n<- %s reports success!\n\n", K.Server)
					}
				}
				return &session, nil
			}
		}
	} else {
		auth, err := K.newToken(username, NONE)
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

// Add Bearer token to KWAPI requests.
func (s KWSession) setToken(req *http.Request, clear bool) (err error) {
	s.testTokenStore()

	token, err := s.TokenStore.Load(s.Username)
	if err != nil {
		return err
	}

	// If we find a token, check if it's still valid within the next 5 minutes.
	if token != nil && !clear {
		if token.Expires < time.Now().Add(time.Duration(5*time.Minute)).Unix() {
			// First attempt to use a refresh token if there is one.
			token, err = s.refreshToken(s.Username, token)
			if err != nil && s.secrets.signature_key == nil {
				Notice("Unable to use refresh token, must reauthenticate for new access token: %s", err.Error())
			}
		}
	}

	if clear {
		token = nil
		s.TokenStore.Delete(s.Username)
	}

	if token == nil {
		if s.secrets.signature_key != nil {
			token, err = s.newToken(s.Username, NONE)
			if err != nil {
				return err
			}
		} else {
			user, err := s.authenticate(s.Username, false, false)
			if err != nil {
				return err
			}
			token, err = s.TokenStore.Load(user.Username)
			if err != nil {
				return err
			}
		}
	}

	if token != nil {
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		if err := s.TokenStore.Save(s.Username, token); err != nil {
			return err
		}
	}
	return nil
}

// Get a new token from a refresh token.
func (K *KWAPI) refreshToken(username string, auth *KWAuth) (*KWAuth, error) {
	if auth == nil {
		return nil, fmt.Errorf("No refresh token found for %s.", username)
	}
	path := fmt.Sprintf("https://%s/oauth/token", K.Server)

	req, err := http.NewRequest(http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}

	http_header := make(http.Header)
	http_header.Set("Content-Type", "application/x-www-form-urlencoded")
	if K.AgentString == NONE {
		K.AgentString = "SnugLib/1.0"
	}
	http_header.Set("User-Agent", K.AgentString)

	req.Header = http_header

	client_id := K.ApplicationID

	postform := &url.Values{
		"client_id":     {client_id},
		"client_secret": {K.secrets.decrypt(K.secrets.client_secret_key)},
		"grant_type":    {"refresh_token"},
		"refresh_token": {auth.RefreshToken},
	}

	if K.Debug {
		Debug("\n[kiteworks]: %s\n--> ACTION: \"POST\" PATH: \"%s\"", username, path)
		for k, v := range *postform {
			if k == "grant_type" || k == "RedirectURI" || k == "scope" {
				Debug("\\-> POST PARAM: %s VALUE: %s", k, v)
			} else {
				Debug("\\-> POST PARAM: %s VALUE: [HIDDEN]", k)
			}
		}
	}

	req.Body = ioutil.NopCloser(bytes.NewReader([]byte(postform.Encode())))

	client := K.Session(username).NewClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if err := K.decodeJSON(resp, &auth); err != nil {
		return nil, err
	}

	auth.Expires = auth.Expires + time.Now().Unix()
	return auth, nil
}

// Generate a new Bearer token from kiteworks.
func (K *KWAPI) newToken(username, password string) (auth *KWAuth, err error) {

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
		"client_secret": {K.secrets.decrypt(K.secrets.client_secret_key)},
		"redirect_uri":  {K.RedirectURI},
	}

	if password != NONE {
		postform.Add("grant_type", "password")
		postform.Add("username", username)
		postform.Add("password", password)
	} else {
		signature := K.secrets.decrypt(K.secrets.signature_key)
		randomizer := rand.New(rand.NewSource(int64(time.Now().Unix())))
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

	if K.Debug {
		Debug("\n[kiteworks]: %s\n--> ACTION: \"POST\" PATH: \"%s\"", username, path)
		for k, v := range *postform {
			if k == "grant_type" || k == "redirect_uri" || k == "scope" {
				Debug("\\-> POST PARAM: %s VALUE: %s", k, v)
			} else {
				Debug("\\-> POST PARAM: %s VALUE: [HIDDEN]", k)
			}
		}
	}

	req.Body = ioutil.NopCloser(bytes.NewReader([]byte(postform.Encode())))

	client := K.Session(username).NewClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if err := K.decodeJSON(resp, &auth); err != nil {
		return nil, err
	}

	auth.Expires = auth.Expires + time.Now().Unix()
	return
}
