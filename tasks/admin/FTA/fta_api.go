package FTA

import (
	//"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	//"github.com/cmcoffee/go-snuglib/iotimeout"
	. "github.com/cmcoffee/kitebroker/core"
	//"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type FTASession struct {
	Username string
	*FTAClient
}

type FTAClient struct {
	*APIClient
}

func (F FTAClient) Session(user string) *FTASession {
	return &FTASession{user, &F}
}

func (F FTASession) Call(api_req APIRequest) (err error) {
	api_req.Header = map[string][]string{
		"OAUTH_PARAM": []string{"oauth_token"},
	}
	api_req.Username = F.Username
	
	return F.APIClient.Call(api_req)
}

// Reads FTA rest errors and interprets them.
func (F *FTAClient) ftaError(body []byte) APIError {
	// kiteworks API Error
	var FTAErr struct {
		Error           string `json:"error"`
		ErrorDesc       string `json:"error_description"`
		ResultCode      int    `json:"result_code"`
		ResultMsg       string `json:"result_msg"`
		ResultMsgDetail string `json:"result_msg_detail"`
	}

	json.Unmarshal(body, &FTAErr)

	e := F.NewAPIError()
	if FTAErr.Error != NONE {
		e.Register(FTAErr.Error, FTAErr.ErrorDesc)
	}
	if FTAErr.ResultCode != 0 {
		if !IsBlank(FTAErr.ResultMsgDetail) {
			FTAErr.ResultMsg = fmt.Sprintf("%s: %s", FTAErr.ResultMsg, FTAErr.ResultMsgDetail)
		}
		e.Register(fmt.Sprintf("%d", FTAErr.ResultCode), FTAErr.ResultMsg)
	}

	if e.NoErrors() {
		return nil
	}

	return e
}

// Get new access_token.
func (T FTAClient) newFTAToken(username string) (auth *Auth, err error) {
	path := fmt.Sprintf("https://%s/seos/oauth/token", T.Server)

	req, err := http.NewRequest(http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}

	http_header := make(http.Header)
	http_header.Set("Content-Type", "application/x-www-form-urlencoded")
	http_header.Set("User-Agent", T.AgentString)

	req.Header = http_header

	client_id := T.ApplicationID

	postform := &url.Values{
		"client_id":     {T.ApplicationID},
		"client_secret": {T.GetClientSecret()},
		"redirect_uri":  {T.RedirectURI},
	}

	signature := T.GetSignature()
	randomizer := rand.New(rand.NewSource(int64(time.Now().Unix())))
	nonce := randomizer.Int() % 999999
	timestamp := int64(time.Now().Unix())
	base_string := fmt.Sprintf("%s|@@|%s|@@||@@|%d|@@|%d", client_id, username, timestamp, nonce)

	mac := hmac.New(sha1.New, []byte(signature))
	mac.Write([]byte(base_string))
	signature = hex.EncodeToString(mac.Sum(nil))

	auth_code := fmt.Sprintf("%s|@@|%s|@@||@@|%d|@@|%d|@@|%s",
		base64.StdEncoding.EncodeToString([]byte(client_id)),
		base64.StdEncoding.EncodeToString([]byte(username)),
		timestamp, nonce, signature)

	postform.Add("grant_type", "authorization_code")
	postform.Add("code", auth_code)
	postform.Add("scope", "account/* myfiles/* workspaces/* wsusers/* wscomments/* wsfiles/* twshub/* twsfiles/")

	if T.Snoop {
		Debug("[FTA]: %s", username)
		Debug("--> ACTION: \"POST\" PATH: \"%s\"", path)
		for k, v := range *postform {
			if k == "grant_type" || k == "redirect_uri" || k == "scope" {
				Debug("\\-> POST PARAM: %s VALUE: %s", k, v)
			} else {
				Debug("\\-> POST PARAM: %s VALUE: [HIDDEN]", k)
			}
		}
	}

	var fta_auth struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Expires      string `json:"expires_in"`
	}

	err = T.Fulfill(&APISession{NONE, req}, []byte(postform.Encode()), &fta_auth)
	if err != nil {
		return nil, err
	}

	expiry, _ := strconv.ParseInt(fta_auth.Expires, 0, 64)

	auth = new(Auth)
	auth.AccessToken = fta_auth.AccessToken
	auth.RefreshToken = fta_auth.RefreshToken
	auth.Expires = expiry + time.Now().Unix()

	return
}
