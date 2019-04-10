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

	nfo.Stdout("\n*** %s Authentication ***\n\n", Config.Get("configuration", "server"))

	username = get_input("Account: ")

	Config.Set("configuration", "account", username)

	switch auth_flow {
	case SIGNATURE_AUTH:
		DB.Truncate("tokens")
		for {
			password = get_passw("Signature Secret: ")
			password_vrfy := get_passw("  Confirm Secret: ")
			if password != password_vrfy {
				nfo.Stdout("Signatures don't match.")
				continue
			}
			break
		}
		DB.CryptSet("kitebroker", "s", &password)
		fmt.Println(NONE)
	case PASSWORD_AUTH:
		for {
			password = get_passw("Password: ")
			password_vrfy := get_passw(" Confirm: ")
			if password != password_vrfy {
				nfo.Stdout("Passwords don't match.")
				continue
			}
			break
		}
		fmt.Println(NONE)
	}

	Config.Save("configuration")
	return
}

var NoValidToken = fmt.Errorf("No valid authentication tokens available.")

// RetryToken will attempt to get a new token when there is an error with the current token.
func (s Session) ChkToken(err error) bool {
	if KiteError(err, TOKEN_ERR) {
		var kauth *KiteAuth
		DB.Get("tokens", s, &kauth)
		if kauth != nil {
			kauth.Expiry = 0
			DB.CryptSet("tokens", s, &kauth)
		}

		_, err := s.GetToken()
		if err == nil {
			return true
		}
	}
	return false
}

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

	// Clear this token, we're either getting a new token or a refresh token.
	DB.Unset("tokens", s)

	attempts := 0

RequestToken:

	path := fmt.Sprintf("https://%s/oauth/token", server)

	req, err := http.NewRequest("POST", path, nil)
	if err != nil {
		return NONE, err
	}

	header := make(http.Header)

	header.Set("Content-Type", "application/x-www-form-urlencoded")
	//header.Set("X-Accellion-Version", fmt.Sprintf("%s", KWAPI_VERSION))
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

	if snoop {
		nfo.Stdout("\n--> ACTION: \"POST\" PATH: \"%s\"", path)
		for k, v := range *postform {
			if k == "grant_type" || k == "redirect_uri" || k == "scope" {
				nfo.Stdout("\\-> POST PARAM: %s VALUE: %s", k, v)
			} else {
				nfo.Stdout("\\-> POST PARAM: %s VALUE: [HIDDEN]", k)
			}
		}
	}

	req.Body = ioutil.NopCloser(bytes.NewReader([]byte(postform.Encode())))

	client := s.NewClient()

	resp, err := client.Do(req)
	if err != nil {
		if KiteError(err, TOKEN_ERR) {
			DB.Unset("tokens", s)
			if auth_flow != SIGNATURE_AUTH {
				nfo.Fatal(err)
			} else {
				found = false
				attempts = attempts + 1
				if attempts < 3 {
					goto RequestToken
				} else {
					nfo.Fatal("%s: %s", s, err.Error())
				}
			}
		}
		return NONE, err
	}

	if err := s.DecodeJSON(resp, &auth); err != nil {
		return NONE, err
	}
	if snoop {
		nfo.Stdout("{\"authorization_code\":\"[HIDDEN]\",\"expires_in\":%d,\"token_type\":\"%s\", \"scope\":\"%s\", \"refresh_token\":\"[HIDDEN]\"}\n", auth.Expiry, auth.Type, auth.Scope)
	}

	auth.Expiry = auth.Expiry + time.Now().Unix()
	return auth.AccessToken, DB.CryptSet("tokens", s, auth)
}
