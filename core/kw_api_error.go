package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/cmcoffee/go-snuglib/iotimeout"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
)

// Auth token related errors.
var _TOKEN_ERR = []string{"ERR_AUTH_PROFILE_CHANGED", "ERR_INVALID_GRANT", "ERR_AUTH_UNAUTHORIZED"}

// Specific kiteworks error object.
type kwError struct {
	flag    uint8
	message []string
	codes   []string
	err     map[string]struct{}
}

// Add a kiteworks error to APIError
func (e *kwError) AddError(code, message string) {
	code = strings.ToUpper(code)

	if strings.Contains(code, "ERR_INTERNAL_") {
		e.err["ERR_INTERNAL_SERVER_ERROR"] = struct{}{}
	}

	e.err[code] = struct{}{}
	e.codes = append(e.codes, code)
	e.message = append(e.message, message)
}

// Returns Error String.
func (e kwError) Error() string {
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
func IsKWError(err error, code...string) bool {
	if e, ok := err.(*kwError); !ok {
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

// Create a new REST error.
func new_kwError() *kwError {
	e := new(kwError)
	e.message = make([]string, 0)
	e.err = make(map[string]struct{})
	return e
}

// convert responses from kiteworks APIs to errors to return to callers.
func (K *KWAPI) respError(resp *http.Response) (err error) {
	if resp == nil {
		return
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	var (
		snoop_buffer bytes.Buffer
		body         io.Reader
	)

	resp.Body = iotimeout.NewReadCloser(resp.Body, K.RequestTimeout)
	defer resp.Body.Close()

	if K.Snoop {
		Debug("<-- RESPONSE STATUS: %s", resp.Status)
		body = io.TeeReader(resp.Body, &snoop_buffer)
	} else {
		body = resp.Body
	}

	// kiteworks API Error
	type KiteErr struct {
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
		Errors    []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}

	output, err := ioutil.ReadAll(body)

	if K.Snoop {
		snoop_request(&snoop_buffer)
	}

	if err != nil {
		return err
	}

	var kite_err *KiteErr
	json.Unmarshal(output, &kite_err)
	if kite_err != nil {
		e := new_kwError()
		for _, v := range kite_err.Errors {
			e.AddError(v.Code, v.Message)
		}
		if kite_err.ErrorDesc != NONE {
			e.AddError(kite_err.Error, kite_err.ErrorDesc)
		}
		return e
	}

	if resp.Status == "401 Unathorized" {
		e := new_kwError()
		e.AddError("ERR_AUTH_UNAUTHORIZED", "Unathorized Access Token")
		return e
	}

	return fmt.Errorf("%s says \"%s.\"", resp.Request.Host, resp.Status)
}
