package core

import (
	"fmt"
	"strings"
)

// Specific kiteworks error object.
type APIError struct {
	prefix  string
	message []string
	codes   []string
	err     map[string]struct{}
}
/*
type APIError interface {
	Register(code, message string)
	NoErrors() bool
	Error() string
}*/

func (C APIClient) clear_token(username string) {
	if C.secrets.signature_key == nil {
		existing, err := C.TokenStore.Load(username)
		if err != nil {
			Critical(err)
		}
		if token, err := C.refreshToken(username, existing); err == nil {
			if err := C.TokenStore.Save(username, token); err != nil {
				Critical(err)
			}
		} else {
			C.TokenStore.Delete(username)
			Critical(fmt.Errorf("Access token is no longer valid."))
		}
	} else {
		C.TokenStore.Delete(username)
	}
}

func (C APIClient) isTokenError(username string, err error) bool {
	if C.TokenErrorCodes != nil {
		if IsAPIError(err, C.TokenErrorCodes[0:]...) {
			C.clear_token(username)
			return true
		}
	} else {
		if IsAPIError(err, "ERR_AUTH_PROFILE_CHANGED", "ERR_INVALID_GRANT", "INVALID_GRANT", "ERR_AUTH_UNAUTHORIZED") {
			C.clear_token(username)
			return true
		}
	}
	return false
}

func (C APIClient) isRetryError(err error) bool {
	if C.RetryErrorCodes != nil {
		return IsAPIError(err, C.RetryErrorCodes[0:]...)
	} else {
		return IsAPIError(err, "ERR_INTERNAL_SERVER_ERROR", "ERR_ACCESS_USER", "HTTP_STATUS_503", "HTTP_STATUS_502")
	}
	return false
}
/*
func (C APIClient) NewAPIError() *apiError {
	return new(apiError)
}*/

func (e APIError) noError() bool {
	if e.err == nil {
		return true
	}
	return false
}

// Add a kiteworks error to APIError
func (e *APIError) Register(code, message string) {
	code = strings.ToUpper(code)

	if e.err == nil {
		e.err = make(map[string]struct{})
	}

	e.err[code] = struct{}{}

	if !IsBlank(message) {
		e.codes = append(e.codes, code)
		e.message = append(e.message, message)
	}
}

// Returns Error String.
func (e APIError) Error() string {
	str := make([]string, 0)
	e_len := len(e.message)
	for i := 0; i < e_len; i++ {
		if e_len == 1 {
			if e.prefix == NONE {
				return fmt.Sprintf("%s (%s)", e.message[i], e.codes[i])
			} else {
				return fmt.Sprintf("%s => %s (%s)", e.prefix, e.message[i], e.codes[i])
			}
		} else {
			if i == 0 && e.prefix != NONE {
				str = append(str, fmt.Sprintf("%s -", e.prefix))
			}
			str = append(str, fmt.Sprintf("[%d] %s (%s)", i, e.message[i], e.codes[i]))
		}
	}
	return strings.Join(str, "\n")
}

func PrefixAPIError(prefix string, err error) error {
	if e, ok := err.(APIError); !ok {
		return err
	} else {
		e.prefix = prefix
		return e
	}
}

// Search for KWAPIErrorCode, if multiple codes are given return true if we find one.
func IsAPIError(err error, code ...string) bool {
	if e, ok := err.(APIError); !ok {
		return false
	} else {
		if len(code) == 0 {
			return true
		}
		for _, v := range code {
			if _, ok := e.err[strings.ToUpper(v)]; ok {
				return true
			}
		}
	}
	return false
}
