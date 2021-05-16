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
	token, err := F.GetToken(F.Username)
	if err != nil {
		return err
	}

	api_req.Params = SetParams(api_req.Params, PostForm{"oauth_token": token.AccessToken})
	api_req.Username = NONE
	
	return F.APIClient.Call(api_req)
}

func (F FTASession) GetToken(username string) (*Auth, error) {
	token, err := F.TokenStore.Load(F.Username)
	if err != nil {
		return nil, err
	}

	// If we find a token, check if it's still valid.
	if token != nil {
		if token.Expires <= time.Now().Unix() {
			token = nil
		} else {
			return token, nil
		}
	} 

	if token == nil {
		F.TokenStore.Delete(username)
		token, err = F.NewToken(username)
		if err != nil {
			return nil, err
		}
	}

	if err := F.TokenStore.Save(username, token); err != nil {
		return token, err
	}

	return token, nil
}

// Reads FTA rest errors and interprets them.
func (F *FTAClient) ftaError(body []byte) (e APIError) {
	// kiteworks API Error
	var FTAErr struct {
		Error           string `json:"error"`
		ErrorDesc       string `json:"error_description"`
		ResultCode      int    `json:"result_code"`
		ResultMsg       string `json:"result_msg"`
		ResultMsgDetail string `json:"result_msg_detail"`
	}

	json.Unmarshal(body, &FTAErr)

	if FTAErr.Error != NONE {
		e.Register(FTAErr.Error, FTAErr.ErrorDesc)
	}
	if FTAErr.ResultCode != 0 {
		if !IsBlank(FTAErr.ResultMsgDetail) {
			FTAErr.ResultMsg = fmt.Sprintf("%s: %s", FTAErr.ResultMsg, FTAErr.ResultMsgDetail)
		}
		e.Register(fmt.Sprintf("%d", FTAErr.ResultCode), FTAErr.ResultMsg)
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

	Trace("[FTA]: %s", username)
	Trace("--> ACTION: \"POST\" PATH: \"%s\"", path)
	for k, v := range *postform {
		if k == "grant_type" || k == "redirect_uri" || k == "scope" {
			Trace("\\-> POST PARAM: %s VALUE: %s", k, v)
		} else {
			Trace("\\-> POST PARAM: %s VALUE: [HIDDEN]", k)
		}
	}

	var fta_auth struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Expires      string `json:"expires_in"`
	}

	req.GetBody = GetBodyBytes([]byte(postform.Encode()))

	err = T.Fulfill(NONE, req, &fta_auth)
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
