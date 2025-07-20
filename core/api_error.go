package core

import (
	"fmt"
	"strings"
)

// APIError represents an error returned by the API.
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

// clear_token removes the access token for a given username.
// It either refreshes the token if a signature key exists or
// deletes it from the store.
func (s *APIClient) clear_token(username string) {
	if s.secrets.signature_key == nil {
		token, err := s.TokenStore.Load(username)
		if err != nil {
			Critical(err)
		}
		if err := s.refreshToken(username, token); err == nil {
			if err := s.TokenStore.Save(username, token); err != nil {
				Critical(err)
			}
		} else {
			s.TokenStore.Delete(username)
			Critical(fmt.Errorf("Access token is no longer valid."))
		}
	} else {
		s.TokenStore.Delete(username)
	}
}

// isTokenError checks if the error is a token-related error and clears
// the token if it is.
func (s *APIClient) isTokenError(username string, err error) bool {
	if s.TokenErrorCodes != nil {
		if IsAPIError(err, s.TokenErrorCodes[0:]...) {
			s.clear_token(username)
			return true
		}
	} else {
		if IsAPIError(err, "ERR_AUTH_PROFILE_CHANGED", "ERR_INVALID_GRANT", "INVALID_GRANT", "ERR_AUTH_UNAUTHORIZED") {
			// Don't retry on suspended accounts.
			if strings.Contains(strings.ToLower(err.Error()), "suspended") {
				s.clear_token(username)
				return false
			}
			s.clear_token(username)
			return true
		}
	}
	return false
}

// isRetryError checks if the given error should be retried.
// It uses predefined or custom retry error codes to determine retry eligibility.
func (s *APIClient) isRetryError(err error) bool {
	if s.RetryErrorCodes != nil {
		return IsAPIError(err, s.RetryErrorCodes[0:]...)
	} else {
		return IsAPIError(err, "ERR_INTERNAL_SERVER_ERROR", "HTTP_STATUS_503", "HTTP_STATUS_502", "HTTP_STATUS_500")
	}
}

/*
func (C APIClient) NewAPIError() *apiError {
	return new(apiError)
}*/

// noError returns true if the error map is nil, false otherwise.
func (e APIError) noError() bool {
	if e.err == nil {
		return true
	}
	return false
}

// Register registers an error code and message.
// It stores the code in an internal map and appends both the code
// and message to slices if a message is provided. The code is
// converted to uppercase before storage.
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

// Error returns the error message as a string.
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

// PrefixAPIError prefixes the APIError with the given prefix string.
// It returns the original error if it is not an APIError.
func PrefixAPIError(prefix string, err error) error {
	if e, ok := err.(APIError); !ok {
		return err
	} else {
		e.prefix = prefix
		return e
	}
}

// IsAPIError checks if err is an APIError and if it matches any of the provided codes.
// If no codes are provided, it returns true if err is an APIError.
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
