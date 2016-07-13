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

// Returns a valid auth token... or an error.
func (s Session) GetToken() (auth *KiteAuth, err error) {

	var signature string
	if _, err = DB.Get("config", "signature_secret", &signature); err != nil {
		return nil, err
	}

	auth, err = s.reqToken(signature)
	if err == nil {
		err = s.testToken(auth.AccessToken)
	}

	// If refresh failed, try getting new token.
	if err != nil {
		if found, _ := DB.Get("tokens", s.account, &auth); found {
			DB.Unset("tokens", s.account)
			return s.reqToken(signature)
		}
	}
	return
}

// Converts kiteworks API errors to standard golang error message.
func (s Session) respError(resp *http.Response) (err error) {

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	type KiteErr struct {
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
	}

	var kite_err KiteErr

	dec := json.NewDecoder(resp.Body)

	dec.Decode(&kite_err)
	defer resp.Body.Close()

	if kite_err.ErrorDesc != NONE {
		return fmt.Errorf("%s: %s", kite_err.Error, kite_err.ErrorDesc)
	} else {
		return fmt.Errorf("%s", resp.Status)
	}

	return nil
}

// Checks to see if access_token is working.
func (s Session) testToken(token string) (err error) {

	req, err := http.NewRequest("GET", fmt.Sprintf("https://%s/rest/users/me", server), nil)
	if err != nil {
		return err
	}

	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%s", KWAPI_VERSION))
	req.Header.Set("User-Agent", fmt.Sprintf("%s(v%s)", NAME, VERSION))
	req.Header.Set("Authorization", "Bearer "+token)

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
func (s Session) reqToken(signature string) (auth *KiteAuth, err error) {

	req, err := http.NewRequest("POST", fmt.Sprintf("https://%s/oauth/token", server), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%s", KWAPI_VERSION))
	req.Header.Set("User-Agent", fmt.Sprintf("%s(v%s)", NAME, VERSION))

	var client_id, client_secret string
	if _, err = DB.Get("config", "client_id", &client_id); err != nil {
		return nil, err
	}
	if _, err = DB.Get("config", "client_secret", &client_secret); err != nil {
		return nil, err
	}

	postform := &url.Values{
		"client_id":     {client_id},
		"client_secret": {client_secret},
	}

	// If refresh token exists, use it. Otherwise request new access token.
	if found, _ := DB.Get("tokens", s.account, &auth); found && auth.RefreshToken != "" {
		postform.Add("grant_type", "refresh_token")
		postform.Add("refresh_token", auth.RefreshToken)
	} else {
		randomizer := rand.New(rand.NewSource(int64(time.Now().Unix())))
		nonce := randomizer.Int() % 999999
		timestamp := int64(time.Now().Unix())

		base_string := fmt.Sprintf("%s|@@|%s|@@|%d|@@|%d", client_id, s.account, timestamp, nonce)

		mac := hmac.New(sha1.New, []byte(signature))
		mac.Write([]byte(base_string))
		signature := hex.EncodeToString(mac.Sum(nil))

		auth_code := fmt.Sprintf("%s|@@|%s|@@|%d|@@|%d|@@|%s",
			base64.StdEncoding.EncodeToString([]byte(client_id)),
			base64.StdEncoding.EncodeToString([]byte(s.account)),
			timestamp, nonce, signature)

		postform.Add("grant_type", "authorization_code")
		postform.Add("redirect_uri", Config.SGet(NAME, "redirect_uri"))
		postform.Add("code", auth_code)
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

	DB.CryptSet("tokens", s.account, &auth)
	return
}
