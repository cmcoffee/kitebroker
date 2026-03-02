package core

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	rand2 "crypto/rand"

	"github.com/cmcoffee/snugforge/iotimeout"
	"github.com/cmcoffee/snugforge/jwcrypt"
)

const (
	// SIGNATURE_AUTH represents signature-based authentication.
	SIGNATURE_AUTH = 1 << iota
	// PASSWORD_AUTH represents password-based authentication.
	JWT_AUTH
	// AUTHORIZATION_CODE_AUTH represents authorization code-based authentication.
	AUTHORIZATION_CODE_AUTH
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
	errorScannerOnce sync.Once
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
	K.errorScannerOnce.Do(func() {
		K.ErrorScanner = kwapiError
	})
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

// DataCall retrieves data from the Kitebroker API, handling pagination and data aggregation.
// It takes an API request, offset, and limit as input, and returns the aggregated data or an error.
// The function handles pagination by repeatedly calling the API with updated offset and limit parameters
// until all data has been retrieved or the limit is reached. It then aggregates the data and returns it.
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

// KWNewToken generates a new authentication token for the given username.
// It supports signature-based, password-based, and JWT-based authentication methods.
// The function selects the appropriate authentication flow based on the configured flags
// and sends an authentication request to the Kiteworks OAuth token endpoint.
// It returns the authentication token or an error if the request fails.
func (K *KWAPI) KWNewToken(username string) (auth *Auth, err error) {

	var post *url.Values

	switch K.Flags.Switch(SIGNATURE_AUTH, JWT_AUTH) {
	case SIGNATURE_AUTH:
		post = K.signature_auth_flow(username)
	case JWT_AUTH:
		post, err = K.jwt_auth_flow(username)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("Unkown or unsupported authentication flow requested.")
	}

	return K.sendAuth(username, post)
}

// sendAuth sends an authentication request to the Kiteworks OAuth token endpoint using the provided username and form data.
// It constructs the HTTP POST request, sets the necessary headers and form values, sends the request, and decodes the response into an Auth struct.
// Returns the authentication token or an error if the request fails.
func (K *KWAPI) sendAuth(username string, postform *url.Values) (auth *Auth, err error) {
	path := fmt.Sprintf("https://%s/oauth/token", K.Server)

	req, err := http.NewRequest(http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}

	http_header := make(http.Header)
	http_header.Set("Content-Type", "application/x-www-form-urlencoded")
	http_header.Set("User-Agent", K.AgentString)

	req.Header = http_header

	postform.Set("client_id", K.ApplicationID)
	postform.Set("client_secret", K.GetClientSecret())
	postform.Set("redirect_uri", K.RedirectURI)

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

// GetSignature retrieves the API signature.
// It decrypts the signature key if it exists.
func (K *KWAPI) GetSignature() string {
	return K.Config.Get("signature_key")
}

// Signature sets the signature key in the secrets store.
// It takes the signature key as input and stores it securely.
func (K *KWAPI) Signature(sig string) {
	K.Config.Set("signature_key", sig)
}

type jwt_auth struct {
	*KWAPI
}

// JWT returns a pointer to a jwt_auth struct, allowing access to JWT-related functionality.
func (K *KWAPI) JWT() *jwt_auth {
	return &jwt_auth{
		K,
	}
}

// Key sets the JWT RSA private key in the configuration.
// The key should be in PEM format.
func (J *jwt_auth) Key(jwt_key string) {
	J.Config.Set("jwt_key", jwt_key)
}

// GetKey retrieves the JWT RSA private key from the configuration.
// Returns the key as a byte slice.
func (J *jwt_auth) GetKey() []byte {
	return []byte(J.Config.Get("jwt_key"))
}

// Issuer sets the JWT issuer value in the configuration.
// The issuer is used in the JWT claims.
func (J *jwt_auth) Issuer(issuer string) {
	J.Config.Set("jwt_iss", issuer)
}

// GetIssuer retrieves the JWT issuer value from the configuration.
// Returns the issuer as a string.
func (J *jwt_auth) GetIssuer() string {
	return J.Config.Get("jwt_iss")
}

// UIDAttribute sets the JWT UID attribute name in the configuration.
// This attribute is used to identify the user in the JWT claims.
func (J *jwt_auth) UIDAttribute(uid string) {
	J.Config.Set("jwt_uid", uid)
}

// GetUIDAttribute retrieves the JWT UID attribute name from the configuration.
// Returns the attribute name as a string.
func (J *jwt_auth) GetUIDAttribute() string {
	return J.Config.Get("jwt_uid")
}

// signature_auth_flow constructs the form values required for signature-based OAuth authentication.
// It builds a base string from the application ID, the provided username, a timestamp, and a nonce.
// The base string is HMAC-SHA1 signed using the configured signature key, the resulting digest is
// hex-encoded and combined with base64-encoded client_id and username to form the auth code.
// The returned url.Values contains grant_type=authorization_code and the computed code which
// should be posted to the Kiteworks /oauth/token endpoint.
func (K *KWAPI) signature_auth_flow(username string) *url.Values {
	// Retrieve the signature key from our config.
	signature := K.Config.Get("signature_key")

	// Generate cryptographically secure nonce.
	var nonce_bytes [4]byte
	rand2.Read(nonce_bytes[:])
	nonce := int(nonce_bytes[0])<<24 | int(nonce_bytes[1])<<16 | int(nonce_bytes[2])<<8 | int(nonce_bytes[3])
	if nonce < 0 {
		nonce = -nonce
	}
	nonce = nonce % 999999

	// Create timestamp
	timestamp := int64(time.Now().Unix())

	// Create our base string for authentication, including client_id, the email, timestamp on nonce, using |@@| as seperators.
	base_string := fmt.Sprintf("%s|@@|%s|@@|%d|@@|%d", K.ApplicationID, username, timestamp, nonce)

	// Create a new Keyed-Hash Message Authentication Code(HMAC), using an SHA1 Hash, using the signature key for the HMAC.
	mac := hmac.New(sha1.New, []byte(signature))
	// Now write write the base string from above through the hmac interface.
	mac.Write([]byte(base_string))
	// Hex encode the resulting SUM of the HMAC.
	signature = hex.EncodeToString(mac.Sum(nil))

	// Auth code sent to the oauth/token endpoint will consist will be similar to the original base string, but base64 encoded client_id,
	// as well as the addition of the now hashed string tacked on the end, which the server will use to verify.
	auth_code := fmt.Sprintf("%s|@@|%s|@@|%d|@@|%d|@@|%s",
		base64.StdEncoding.EncodeToString([]byte(K.ApplicationID)),
		base64.StdEncoding.EncodeToString([]byte(username)),
		timestamp, nonce, signature)

	postform := &url.Values{
		"grant_type": {"authorization_code"},
		"code":       {auth_code},
	}
	return postform
}

// jwt_auth_flow creates a URL-encoded form data for JWT-based authentication.
// It constructs a JWT assertion using the configured private key and issuer, signs it with RS256,
// and returns the form data required for the Kiteworks OAuth token endpoint.
// The function returns the form data or an error if the JWT cannot be created or signed.
func (K *KWAPI) jwt_auth_flow(username string) (postform *url.Values, err error) {
	postform = &url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
	}

	keyData := K.JWT().GetKey()

	key, err := jwcrypt.ParseRSAPrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("Invalid or Missing JWT RSA Private Key, please use --setup to Update JWT RSA Private Key: %w", err)
	}

	username_attribute := K.JWT().GetUIDAttribute()
	issuer := K.JWT().GetIssuer()
	if len(issuer) == 0 || len(username_attribute) == 0 {
		return nil, fmt.Errorf("Missing or incomplete JWT Configuration, please use --setup to Configure JWT.")
	}

	Claims := make(map[string]interface{})
	Claims["iss"] = K.JWT().GetIssuer()
	Claims["aud"] = fmt.Sprintf("https://%s", K.Server)
	Claims["sub"] = username
	Claims["exp"] = time.Now().Add(time.Duration(time.Second * 300)).Unix()
	Claims["nbf"] = time.Now().Add(time.Duration(time.Second * -60)).Unix()
	Claims["iat"] = time.Now().Unix()
	Claims["jti"] = UUIDv4()
	if strings.ToLower(username_attribute) != "sub" {
		Claims[username_attribute] = username
	}

	Trace("[JWT Claims]:")
	for k, v := range Claims {
		Trace("\t%s: %v", k, v)
	}

	tokenStr, err := jwcrypt.SignRS512(key, Claims, map[string]string{"type": "JWT"})
	if err != nil {
		return nil, err
	}
	postform.Set("assertion", tokenStr)

	return postform, nil
}
