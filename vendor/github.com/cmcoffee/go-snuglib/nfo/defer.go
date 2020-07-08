package nfo

import (
	"crypto/rand"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"syscall"
)

var (
	// Signal Notification Channel. (ie..nfo.Signal<-os.Kill will initiate a shutdown.)
	signalChan  = make(chan os.Signal)
	globalDefer struct {
		mutex sync.Mutex
		ids   []string
		d_map map[string]func() error
	}
	errCode   = 0
	wait      sync.WaitGroup
	exit_lock = make(chan struct{})
)

// Global wait group, allows running processes to finish up tasks before app shutdown
func BlockShutdown() {
	wait.Add(1)
}

// Task completed, carry on with shutdown.
func UnblockShutdown() {
	wait.Done()
}

// Adds a function to the global defer, function must take no arguments and either return nothing or return an error.
// Returns function to be called by local keyword defer if you want to run it now and remove it from global defer.
func Defer(closer interface{}) func() error {
	globalDefer.mutex.Lock()
	defer globalDefer.mutex.Unlock()

	errorWrapper := func(closerFunc func()) func() error {
		return func() error {
			closerFunc()
			return nil
		}
	}

	var id string

	for {
		// Generates a random tag.
		id = func(ch string) string {
			chlen := len(ch)

			rand_string := make([]byte, 32)
			rand.Read(rand_string)

			for i, v := range rand_string {
				rand_string[i] = ch[v%byte(chlen)]
			}
			return string(rand_string)
		}("!@#$%^&*()_+-=][{}|/.,><abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

		// Check if tag is used already
		if _, ok := globalDefer.d_map[id]; !ok {
			break
		}
	}

	var d func() error

	switch closer := closer.(type) {
	case func():
		d = errorWrapper(closer)
	case func() error:
		d = closer
	default:
		return nil
	}

	globalDefer.ids = append(globalDefer.ids, id)
	globalDefer.d_map[id] = d

	return func() error {
		globalDefer.mutex.Lock()
		defer globalDefer.mutex.Unlock()
		delete(globalDefer.d_map, id)
		for i := len(globalDefer.ids) - 1; i > -1; i-- {
			if globalDefer.ids[i] == id {
				globalDefer.ids = append(globalDefer.ids[:i], globalDefer.ids[i+1:]...)
			}
		}
		return d()
	}
}

// Intended to be a defer statement at the begining of main, but can be called at anytime with an exit code.
// Tries to catch a panic if possible and log it as a fatal error,
// then proceeds to send a signal to the global defer/shutdown handler
func Exit(exit_code int) {
	if r := recover(); r != nil {
		Fatal("(panic) %s", string(debug.Stack()))
	} else {
		atomic.StoreInt32(&fatal_triggered, 2) // Ignore any Fatal() calls, we've been told to exit.
		signalChan <- os.Kill
		<-exit_lock
		os.Exit(exit_code)
	}
}

// Sets the signals that we listen for.
func SetSignals(sig ...os.Signal) {
	mutex.Lock()
	defer mutex.Unlock()
	signal.Stop(signalChan)
	signal.Notify(signalChan, sig...)
}

// Set a callback function(no arguments) to run after receiving a specific syscall, function returns true to continue shutdown process.
func SignalCallback(signal os.Signal, callback func() (continue_shutdown bool)) {
	mutex.Lock()
	defer mutex.Unlock()
	callbacks[signal] = callback
}

var callbacks = make(map[os.Signal]func() bool)

func init() {
	globalDefer.d_map = make(map[string]func() error)
	SetSignals(syscall.SIGINT, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for {
			s := <-signalChan

			mutex.Lock()
			cb := callbacks[s]
			mutex.Unlock()

			if cb != nil {
				if !cb() {
					continue
				}
			}

			atomic.CompareAndSwapInt32(&fatal_triggered, 0, 2)

			switch s {
			case syscall.SIGINT:
				errCode = 130
			case syscall.SIGHUP:
				errCode = 129
			case syscall.SIGTERM:
				errCode = 143
			}

			break
		}

		globalDefer.mutex.Lock()
		defer globalDefer.mutex.Unlock()

		// Run through all globalDefer functions.
		for _, id := range globalDefer.ids {
			/*if _, ok := globalDefer.d_map[id]; !ok {
				continue
			}*/
			if err := globalDefer.d_map[id](); err != nil {
				write2log(ERROR|_bypass_lock, err.Error())
			}
		}

		// Wait on any process that have access to wait.
		wait.Wait()

		// Hide Please Wait
		PleaseWait.Hide()

		// Try to flush out any remaining text.
		write2log(_flash_txt|_no_logging|_bypass_lock, "")

		// Finally exit the application
		select {
		case exit_lock <- struct{}{}:
		default:
			os.Exit(errCode)
		}
	}()
}
