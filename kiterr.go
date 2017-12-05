package main

import (
	"fmt"
	"strings"
)

const (
	ERR_AUTH_UNAUTHORIZED = 1 << iota
	ERR_AUTH_PROFILE_CHANGED
	ERR_ACCESS_USER
	ERR_INVALID_GRANT
	ERR_ENTITY_DELETED_PERMANENTLY
	ERR_ENTITY_NOT_FOUND
)

type KError struct {
	flag    int64
	message []string
}

// Implement error interface.
func (e KError) Error() string {
	str := make([]string, 0)
	e_len := len(e.message)
	for i := 0; i < e_len; i++ {
		if e_len == 1 {
			return e.message[i]
		} else {
			str = append(str, fmt.Sprintf("[%d] %s", i, e.message[i]))
		}
	}
	return strings.Join(str, "\n")
}

func NewKError() *KError {
	e := new(KError)
	e.message = make([]string, 0)
	return e
}

// Add an error to KError
func (e *KError) AddError(code, message string) {
	switch code {
	case "ERR_AUTH_UNAUTHORIZED":
		e.flag |= ERR_AUTH_UNAUTHORIZED
	case "ERR_AUTH_PROFILE_CHANGED":
		e.flag |= ERR_AUTH_PROFILE_CHANGED
	case "ERR_ACCESS_USER":
		e.flag |= ERR_ACCESS_USER
	case "invalid_grant":
		e.flag |= ERR_INVALID_GRANT
	case "ERR_ENTITY_DELETED_PERMANENTLY":
		e.flag |= ERR_ENTITY_DELETED_PERMANENTLY
	case "ERR_ENTITY_NOT_FOUND":
		e.flag |= ERR_ENTITY_NOT_FOUND
	}
	e.message = append(e.message, message)
}

func IsKiteError(err error) bool {
	if _, ok := err.(*KError); ok {
		return true
	}
	return false
}

// Check for specific error code.
func KiteError(err error, input int64) bool {
	if e, ok := err.(*KError); !ok {
		return false
	} else {
		if e.flag&input != 0 {
			return true
		}
	}
	return false
}
