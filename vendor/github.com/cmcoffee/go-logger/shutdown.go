package logger

import (
	"os"
	"os/signal"
	"time"
	"runtime/debug"
	"fmt"
)

var (
	sig_handler	= make(chan os.Signal, 1)
	lastErrorCode = make(chan int, 1)
	globalDefer	[]func() error
	IgnoreInterrupt = false
)

// if err is not nil, proceed with fatal shutdown.
func FatalChk(err error) {
	if err != nil {
		Fatal(err.Error())
		TheEnd(1)
	}
}

// Adds a func to the global defer.
func InTheEnd(closer interface{}) {
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

// Intended to be a defer statement at the begining of main.
// Tries to catch a panic if possible and log it as a fatal error,
// then proceeds to send a signal to the global defer/shutdown handler
func TheEnd(errorCode int) {
	if r := recover(); r != nil {
		debug.PrintStack()
		Fatal("%v", r)
	}
	lastErrorCode <- errorCode
	sig_handler <- os.Kill
	time.Sleep(time.Minute)
}

func init() {
	signal.Notify(sig_handler, os.Kill, os.Interrupt)
	go func() {
		var err error
		for range sig_handler {
			if IgnoreInterrupt == true { continue }
			// Run through all globalDefer functions.
			for _, x := range globalDefer {
				if err = x(); err != nil { Err(err) }
			}
			select {
			case errorCode := <-lastErrorCode:
				os.Exit(errorCode)
			default:
				fmt.Println("")
				os.Exit(0)
			}
		}
	}()
	Trace("Shutdown handler initilized.")
}
