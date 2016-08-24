package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

func LoadCredentials() (username, password string) {
	switch auth_flow {
		case SIGNATURE_AUTH:
			password = get_input("kiteworks Signature Secret: ")
		case PASSWORD_AUTH:
			username = get_input("kiteworks Username: ")
			password = get_passw("kiteworks Password: ")
	}
	return
}

// Returns a valid auth token... or an error.
func (s Session) GetToken() (access_token string, err error) {

	auth, err := s.getAccessToken()

	// If refresh failed, try getting new token.
	if err != nil {
		if found, _ := DB.Get("tokens", s, &auth); found {
			DB.Unset("tokens", s)
			auth, err = s.getAccessToken()
			if err != nil { return NONE, err }
			return auth.AccessToken, nil
		} else {
			return NONE, err
		}
	}
	return auth.AccessToken, nil
}

// Checks to see if access_token is working.
func (s Session) testToken() (err error) {

	req, err := http.NewRequest("GET", fmt.Sprintf("https://%s/rest/users/me", server), nil)
	if err != nil {
		return err
	}

	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%s", KWAPI_VERSION))
	req.Header.Set("User-Agent", fmt.Sprintf("%s(v%s)", NAME, VERSION))

	access_token := DB.SGet("tokens", s)

	req.Header.Set("Authorization", "Bearer " + access_token)

	client := s.NewClient()

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return s.respError(resp)
	}
	return nil
}


// Call to appliance for Bearer token.
func (s Session) getAccessToken() (auth *KiteAuth, err error) {

	req, err := http.NewRequest("POST", fmt.Sprintf("https://%s/oauth/token", server), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%s", KWAPI_VERSION))
	req.Header.Set("User-Agent", fmt.Sprintf("%s(v%s)", NAME, VERSION))

	postform := &url.Values{
		"client_id":     {client_id},
		"client_secret": {client_secret},
	}

	// If refresh token exists, use it. Otherwise request new access token.
	if found, _ := DB.Get("tokens", s, &auth); found && auth.RefreshToken != "" {
		postform.Add("grant_type", "refresh_token")
		postform.Add("refresh_token", auth.RefreshToken)
	} else {
		postform.Add("redirect_uri", Config.SGet("configuration", "redirect_uri"))
		postform.Add("scope", "*/*/*")

		switch auth_flow {

			case SIGNATURE_AUTH:
				_, signature := LoadCredentials()
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
				username, password := LoadCredentials()
				postform.Add("grant_type", "password")
				postform.Add("username", username)
				postform.Add("password", password)
		}
	}

	// If token has more then an hour left, just return the current token.
	if auth != nil && auth.AccessToken != NONE && (auth.Expiry-3600) > time.Now().Unix() {
		return
	}

	req.Body = ioutil.NopCloser(bytes.NewReader([]byte(postform.Encode())))

	client := s.NewClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// Handle errors during request.
	if resp.StatusCode != 200 {
		return nil, s.respError(resp)
	}

	dec := json.NewDecoder(resp.Body)

	auth = new(KiteAuth)
	err = dec.Decode(auth)
	resp.Body.Close()

	if err != nil {
		return nil, err
	}

	auth.Expiry = auth.Expiry + time.Now().Unix()

	DB.CryptSet("tokens", s, &auth)
	return
}
