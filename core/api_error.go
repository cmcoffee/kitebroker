package core

import (
	"fmt"
	"strings"
)

// Specific kiteworks error object.
type apiError struct {
	message []string
	codes   []string
	err     map[string]struct{}
}

type APIError interface {
	Register(code, message string)
	NoErrors() bool
	Error() string
}

func (C APIClient) isTokenError(err error) bool {
	if C.TokenErrorCodes != nil {
		return IsAPIError(err, C.TokenErrorCodes[0:]...)
	} else {
		return IsAPIError(err, "ERR_AUTH_PROFILE_CHANGED", "ERR_INVALID_GRANT", "INVALID_GRANT", "ERR_AUTH_UNAUTHORIZED")
	}
	return false
}

func (C APIClient) isRetryError(err error) bool {
	if C.RetryErrorCodes != nil {
		return IsAPIError(err, C.TokenErrorCodes[0:]...)
	} else {
		return IsAPIError(err, "ERR_INTERNAL_SERVER_ERROR")
	}
	return false
}

func (C APIClient) NewAPIError() *apiError {
	return new(apiError)
}

func (e *apiError) NoErrors() bool {
	if e.err == nil {
		return true
	}
	return false
}

// Add a kiteworks error to APIError
func (e *apiError) Register(code, message string) {
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
func (e apiError) Error() string {
	str := make([]string, 0)

	e_len := len(e.message)
	for i := 0; i < e_len; i++ {
		if e_len == 1 {
			return fmt.Sprintf("%s (%s)", e.message[i], e.codes[i])
		} else {
			str = append(str, fmt.Sprintf("[%d] %s (%s)\n", i, e.message[i], e.codes[i]))
		}
	}
	return strings.Join(str, "\n")
}

// Search for KWAPIErrorCode, if multiple codes are given return true if we find one.
func IsAPIError(err error, code ...string) bool {
	if e, ok := err.(*apiError); !ok {
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
