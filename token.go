package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/cmcoffee/go-nfo"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"time"
)

// Authorization Information
type KiteAuth struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Expiry       int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Type         string `json:"token_type"`
}

func LoadCredentials() (username, password string, err error) {

	username = Config.Get("configuration", "account")
	password = DB.SGet("kitebroker", "s")

	if password != NONE && username != NONE {
		return username, password, nil
	}

	HideLoader()
	defer ShowLoader()

	if first_token_set {
		return username, password, NoValidToken
	}

	nfo.Print("\n*** %s Authentication ***\n\n", Config.Get("configuration", "server"))

	username = get_input("Account: ")

	Config.Set("configuration", "account", username)

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
	return
}

var NoValidToken = fmt.Errorf("No valid authentication tokens available.")

// Call to appliance for Bearer token.
func (s Session) GetToken() (access_token string, err error) {
	var auth *KiteAuth

	found, err := DB.Get("tokens", s, &auth)
	if err != nil {
		return NONE, err
	}

	// If token is still valid, just return current token.
	if auth != nil && auth.AccessToken != NONE && auth.Expiry > time.Now().Unix() {
		return auth.AccessToken, nil
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("https://%s/oauth/token", server), nil)
	if err != nil {
		return NONE, err
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

	// If refresh token exists, use it. Otherwise request new access token.
	if found && auth.RefreshToken != NONE {
		postform.Add("grant_type", "refresh_token")
		postform.Add("refresh_token", auth.RefreshToken)
	} else {
		postform.Add("redirect_uri", Config.Get("configuration", "redirect_uri"))
		postform.Add("scope", "*/*/*")

		switch auth_flow {
		case SIGNATURE_AUTH:
			username, signature, err := LoadCredentials()
			if s == NONE {
				s = Session(username)
			}
			if err != nil {
				return NONE, err
			}
			randomizer := rand.New(rand.NewSource(int64(time.Now().Unix())))
			nonce := randomizer.Int() % 999999
			timestamp := int64(time.Now().Unix())

			base_string := fmt.Sprintf("%s|@@|%s|@@|%d|@@|%d", client_id, string(s), timestamp, nonce)

			mac := hmac.New(sha1.New, []byte(signature))
			mac.Write([]byte(base_string))
			signature = hex.EncodeToString(mac.Sum(nil))

			auth_code := fmt.Sprintf("%s|@@|%s|@@|%d|@@|%d|@@|%s",
				base64.StdEncoding.EncodeToString([]byte(client_id)),
				base64.StdEncoding.EncodeToString([]byte(s)),
				timestamp, nonce, signature)

			postform.Add("grant_type", "authorization_code")
			postform.Add("code", auth_code)

		case PASSWORD_AUTH:
			username, password, err := LoadCredentials()
			if err != nil {
				return NONE, err
			}
			postform.Add("grant_type", "password")
			postform.Add("username", username)
			postform.Add("password", password)
			s = Session(username)
		}
	}

	req.Body = ioutil.NopCloser(bytes.NewReader([]byte(postform.Encode())))

	client := s.NewClient()

	resp, err := client.Do(req)
	if err != nil {
		return NONE, err
	}

	if err := s.DecodeJSON(resp, &auth); err != nil {
		return NONE, err
	}

	if err != nil {
		if err := DB.Unset("tokens", s); err != nil {
			return NONE, err
		}
		return NONE, err
	}

	auth.Expiry = auth.Expiry + time.Now().Unix()
	return auth.AccessToken, DB.CryptSet("tokens", s, auth)
}
