package main

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
	"time"
	"github.com/cmcoffee/go-logger"
)

// Authorization Information
type KiteAuth struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Expiry       int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Type         string `json:"token_type"`
}

func LoadCredentials() (password string, reset bool) {

	username := Config.Get("configuration", "account")
	password = DB.SGet("kitebroker", "s")

	if password != NONE && username != NONE {
		return password, false
	}

	HideLoader()
	defer ShowLoader()

	logger.Put("\n*** %s Authentication ***\n\n", Config.Get("configuration", "server"))

	Config.Set("configuration", "account", get_input("Account: "))

	switch auth_flow {
		case SIGNATURE_AUTH:
			DB.Truncate("tokens")
			password = get_passw("Signature Secret: ")
			DB.CryptSet("kitebroker", "s", &password)
			fmt.Println(NONE)
		case PASSWORD_AUTH:
			password = get_passw("Password: ")
			fmt.Println(NONE)
	}

	Config.Save("configuration")
	reset = true
	return
}

// Returns a valid auth token... or an error.
func (s Session) GetToken() (access_token string, err error) {

	auth, err := s.getAccessToken()

	if err != nil {
		DB.Unset("tokens", s)
		return NONE, err
	}
	return auth.AccessToken, nil
}

// Call to appliance for Bearer token.
func (s Session) getAccessToken() (auth *KiteAuth, err error) {

	found, err := DB.Get("tokens", s, &auth)
	if err != nil { return nil, err }

	// If token is still valid, just return current token.
	if auth != nil && auth.AccessToken != NONE && auth.Expiry > time.Now().Unix() { return }

	req, err := http.NewRequest("POST", fmt.Sprintf("https://%s/oauth/token", server), nil)
	if err != nil {
		return nil, err
	}

	header := make(http.Header)

	header.Set("Content-Type", "application/x-www-form-urlencoded")
	header.Set("X-Accellion-Version", fmt.Sprintf("%s", KWAPI_VERSION))
	header.Set("User-Agent", fmt.Sprintf("%s(v%s)", NAME, VERSION))

	req.Header = header

	postform := &url.Values{
		"client_id":     {client_id},
		"client_secret": {client_secret},
	}

	var is_refresh bool

	// If refresh token exists, use it. Otherwise request new access token.
	if found && auth.RefreshToken != NONE {
		postform.Add("grant_type", "refresh_token")
		postform.Add("refresh_token", auth.RefreshToken)
		is_refresh = true
	} else {
		postform.Add("redirect_uri", Config.Get("configuration", "redirect_uri"))
		postform.Add("scope", "*/*/*")

		switch auth_flow {
		case SIGNATURE_AUTH:
			signature, reset_auth := LoadCredentials()
			randomizer := rand.New(rand.NewSource(int64(time.Now().Unix())))
			nonce := randomizer.Int() % 999999
			timestamp := int64(time.Now().Unix())

			base_string := fmt.Sprintf("%s|@@|%s|@@|%d|@@|%d", client_id, string(s), timestamp, nonce)

			mac := hmac.New(sha1.New, []byte(signature))
			mac.Write([]byte(base_string))
			signature = hex.EncodeToString(mac.Sum(nil))

			if reset_auth {
				s = Session(Config.Get("configuration", "account"))
			}

			auth_code := fmt.Sprintf("%s|@@|%s|@@|%d|@@|%d|@@|%s",
				base64.StdEncoding.EncodeToString([]byte(client_id)),
				base64.StdEncoding.EncodeToString([]byte(s)),
				timestamp, nonce, signature)

			postform.Add("grant_type", "authorization_code")
			postform.Add("code", auth_code)

		case PASSWORD_AUTH:
			password, _ := LoadCredentials()
			postform.Add("grant_type", "password")
			username := Config.Get("configuration", "account")
			postform.Add("username", username)
			postform.Add("password", password)
			s = Session(username)
		}
	}

	req.Body = ioutil.NopCloser(bytes.NewReader([]byte(postform.Encode())))

	client := s.NewClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if err := s.respError(resp); err != nil {
		return nil, err
	}

	if err := s.DecodeJSON(resp, &auth); err != nil { return nil, err }

	if err != nil {
		if is_refresh {
			if err := DB.Unset("tokens", s); err != nil { return nil, err }
			return s.getAccessToken()
		}
		return nil, err
	}

	auth.Expiry = auth.Expiry + time.Now().Unix()

	return auth, DB.CryptSet("tokens", s, auth)
}