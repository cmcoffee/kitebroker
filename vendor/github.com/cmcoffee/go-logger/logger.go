package logger

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	ERR = 1 << iota
	WARN
	INFO
	NOTICE
	DEBUG
	TRACE
	FATAL
	ALL
	no_log
)

const none = ""

var internal_sigchan chan os.Signal

var (
	flush_len   int
	flush_line  []rune
	mutex       sync.RWMutex
	log_map     = make(map[int]*log.Logger)
	signal_chan chan os.Signal
)

var out_map = map[int][2]io.Writer{
	ERR:    {os.Stderr, ioutil.Discard},
	WARN:   {os.Stdout, ioutil.Discard},
	INFO:   {os.Stdout, ioutil.Discard},
	NOTICE: {os.Stdout, ioutil.Discard},
	DEBUG:  {os.Stdout, ioutil.Discard},
	TRACE:  {os.Stdout, ioutil.Discard},
	FATAL:  {os.Stderr, ioutil.Discard},
}

// Enables or Disables Debug Logging
var DebugLogging = false

// Enables or Disables Trace Logging
var TraceLogging = false

// Sets channel for notifying when logger.Fatal is used.
func Notify(c chan os.Signal) {
	signal_chan = c
}

// Reloads all loggers.
func resetLoggers() {
	mutex.Lock()
	defer mutex.Unlock()
	for k, v := range out_map {
		log_map[k] = log.New(io.MultiWriter(v[0], v[1]), "", log.LstdFlags)
	}
}

// Change flag settings for logger(s).
func SetFlags(logger int, flag int) {
	mutex.Lock()
	defer mutex.Unlock()
	for n, _ := range log_map {
		if logger&n == n || logger == ALL {
			log_map[n].SetFlags(flag)
		}
	}
}

// Change output for logger(s).
func SetOutput(logger int, w io.Writer) {
	mutex.Lock()
	for n, v := range out_map {
		if logger&n == n || logger == ALL {
			out_map[n] = [2]io.Writer{w, v[1]}
		}
	}
	mutex.Unlock()
	resetLoggers()
}

// Don't log, only display via fmt.Printf.
func Put(vars ...interface{}) {
	write2log(no_log, vars...)
}

// Log as Info.
func Log(vars ...interface{}) {
	write2log(INFO, vars...)
}

// Log as Error.
func Err(vars ...interface{}) {
	write2log(ERR, vars...)
}

// Log as Warn.
func Warn(vars ...interface{}) {
	write2log(WARN, vars...)
}

// Log as Notice.
func Notice(vars ...interface{}) {
	write2log(NOTICE, vars...)
}

// Log as Fatal, then quit.
func Fatal(vars ...interface{}) {
	write2log(FATAL, vars...)
}

// Log as Debug.
func Debug(vars ...interface{}) {
	if !DebugLogging {
		return
	}
	write2log(DEBUG, vars...)
}

// Log as Trace.
func Trace(vars ...interface{}) {
	if !TraceLogging {
		return
	}
	write2log(TRACE, vars...)
}

func init() {
	internal_sigchan = make(chan os.Signal, 1)
	signal_chan = internal_sigchan
	resetLoggers()
}

// Prepares output text and sends to appropriate logging destinations.
func write2log(flag int, vars ...interface{}) {

	mutex.Lock()

	vlen := len(vars)

	var msg string

	if vlen == 0 {
		return
	}
	if vlen == 1 {
		msg = fmt.Sprintf("%v", vars[0])
	}
	if vlen > 1 {
		str, ok := vars[0].(string)
		if ok {
			msg = fmt.Sprintf(str, vars[1:]...)
		} else {
			var str_arr []string
			for _, item := range vars {
				str_arr = append(str_arr[len(str_arr):], fmt.Sprintf("%v", item))
			}
			msg = strings.Join(str_arr, ", ")
		}
	}

	if len(flush_line) < flush_len {
		for i := len(flush_line); i < flush_len; i++ {
			flush_line = append(flush_line[0:], ' ')
		}
	}

	fmt.Printf("\r%s\r", string(flush_line[0:flush_len]))
	flush_len = utf8.RuneCountInString(msg)

	switch flag {
	case INFO:
		if remote_log != nil {
			remote_log.Info(msg)
		}
		log_map[INFO].Print(msg)
	case ERR:
		if remote_log != nil {
			remote_log.Err(msg)
		}
		log_map[ERR].Print("[ERROR] " + msg)
	case WARN:
		if remote_log != nil {
			remote_log.Warning(msg)
		}
		log_map[WARN].Print("[WARN] " + msg)
	case FATAL:
		if remote_log != nil {
			remote_log.Emerg(msg)
		}
		log_map[FATAL].Print("[FATAL] " + msg)
		mutex.Unlock()
		signal_chan <- os.Kill
		if signal_chan == internal_sigchan {
			select {
			case <-signal_chan:
			default:
			}
		}
		return
	case NOTICE:
		if remote_log != nil {
			remote_log.Notice(msg)
		}
		log_map[NOTICE].Print("[NOTICE] " + msg)
	case DEBUG:
		if remote_log != nil {
			remote_log.Debug(msg)
		}
		log_map[DEBUG].Print("[DEBUG] " + msg)
	case TRACE:
		if remote_log != nil {
			remote_log.Debug(msg)
		}
		log_map[DEBUG].Print("[TRACE] " + msg)
	default:
		fmt.Printf("%s", msg)
	}
	mutex.Unlock()
}
