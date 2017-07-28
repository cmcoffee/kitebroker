// Package fin implements a global defer for the application which is useful for making a clean exit.
// Functions such as shutting down databases, closing files, etc..
package fin

import (
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"log"
)

var (
	// Signal Notification Channel. (ie..fin.Signal<-os.Kill will initiate a shutdown.)
	Collector      = make(chan os.Signal)
	globalDefer []func() error
	errorLogger func(...interface{})
	errCode     = 0
)

// Sets function with regard to handling errors generated by the global defer.
func SetErrorLogger(input func(v ...interface{})) {
	errorLogger = input
}

// Adds a function to the global defer, function must take no arguments and either return nothing or return an error.
func Defer(closer interface{}) {
	errorWrapper := func(closerFunc func()) func() error {
		return func() error {
			closerFunc()
			return nil
		}
	}

	switch closer := closer.(type) {
	case func():
		globalDefer = append(globalDefer, errorWrapper(closer))
	case func() error:
		globalDefer = append(globalDefer, closer)
	}
}

// Intended to be a defer statement at the begining of main, but can be called at anytime with an exit code.
// Tries to catch a panic if possible and log it as a fatal error,
// then proceeds to send a signal to the global defer/shutdown handler
func Exit(exit_code int) {
	if r := recover(); r != nil {
		errorLogger(fmt.Errorf("panic: %v\n\n%s", r, string(debug.Stack())))
		errCode = 1
	} else {
		errCode = exit_code
	}
	Collector <- os.Kill
	wait_forever := make(chan struct{})
	<-wait_forever
}

// Sets the signals that we listen for to initiate a global shutdown
func ExitOn(sig ...os.Signal) {
	signal.Stop(Collector)
	signal.Notify(Collector, append(sig[0:], os.Kill)...)
}

func init() {
	ExitOn(syscall.SIGINT, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGHUP)
	errorLogger = log.Print
	go func() {
		var err error
		s := <-Collector

		switch s {
		case syscall.SIGINT:
			errCode = 130
		case syscall.SIGHUP:
			errCode = 129
		case syscall.SIGTERM:
			errCode = 143
		}

		// Run through all globalDefer functions.
		for _, x := range globalDefer {
			if err = x(); err != nil {
				errorLogger(err)
			}
		}

		// Finally exit the application
		os.Exit(errCode)
	}()
}
