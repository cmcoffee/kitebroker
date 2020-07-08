// Package 'nfo' is a simple central logging library with file log rotation as well as exporting to syslog.
// Additionally it provides a global defer for cleanly exiting applications and performing last minute tasks before application exits.

package nfo

import (
	"bytes"
	"fmt"
	"github.com/cmcoffee/go-snuglib/wrotate"
	"golang.org/x/crypto/ssh/terminal"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	INFO   = 1 << iota // Log Information
	ERROR              // Log Errors
	WARN               // Log Warning
	NOTICE             // Log Notices
	DEBUG              // Debug Logging
	TRACE              // Trace Logging
	FATAL              // Fatal Logging
	AUX                // Auxilary Log
	AUX2               // Auxilary Log
	AUX3               // Auxilary Log
	AUX4               // Auxilary Log
	_flash_txt
	_print_txt
	_stderr_txt
	_bypass_lock
	_no_logging
)

// Standard Loggers, minus debug and trace.
const (
	STD = INFO | ERROR | WARN | NOTICE | FATAL | AUX | AUX2 | AUX3 | AUX4
	ALL = INFO | ERROR | WARN | NOTICE | FATAL | AUX | AUX2 | AUX3 | AUX4 | DEBUG | TRACE
)

const (
	textWriter = 1 << iota
	fileWriter
	setTimestamp
	setPrefix
)

var (
	FatalOnFileError   = true // Fatal on log file or file rotation errors.
	FatalOnExportError = true // Fatal on export/syslog error.
	flush_line         []rune
	flush_needed       bool
	piped_stdout       bool
	piped_stderr       bool
	fatal_triggered    int32
	msgBuffer          bytes.Buffer
	enabled_exports    = uint32(STD)
	mutex              sync.Mutex
	timezone           = time.Local
	l_map              = map[uint32]*_logger{
		INFO:        {"", os.Stdout, None, true},
		AUX:         {"", os.Stdout, None, true},
		AUX2:        {"", os.Stdout, None, true},
		AUX3:        {"", os.Stdout, None, true},
		AUX4:        {"", os.Stdout, None, true},
		ERROR:       {"[ERROR] ", os.Stdout, None, true},
		WARN:        {"[WARN] ", os.Stdout, None, true},
		NOTICE:      {"[NOTICE] ", os.Stdout, None, true},
		DEBUG:       {"[DEBUG] ", None, None, true},
		TRACE:       {"[TRACE] ", None, None, true},
		FATAL:       {"[FATAL] ", os.Stdout, None, true},
		_flash_txt:  {"", os.Stderr, None, false},
		_print_txt:  {"", os.Stdout, None, false},
		_stderr_txt: {"", os.Stderr, None, false},
	}
)

func init() {
	if !terminal.IsTerminal(int(os.Stdout.Fd())) {
		piped_stdout = true
	}
	if !terminal.IsTerminal(int(os.Stderr.Fd())) {
		piped_stderr = true
	}
	HideTS()
}

type _logger struct {
	prefix  string
	textout io.Writer
	fileout io.Writer
	use_ts  bool
}

// Creates folders.
func mkDir(name ...string) (err error) {
	for _, path := range name {
		subs := strings.Split(path, string(os.PathSeparator))
		for i := 0; i < len(subs); i++ {
			p := strings.Join(subs[0:i], string(os.PathSeparator))
			if p == "" {
				p = "."
			}
			_, err = os.Stat(p)
			if err != nil {
				if os.IsNotExist(err) {
					err = os.Mkdir(p, 0766)
					if err != nil {
						return err
					}
				} else {
					return err
				}
			}
		}
	}
	return nil
}

// Opens a new log file for writing, max_size is threshold for rotation, max_rotation is number of previous logs to hold on to.
// Set max_size_mb to 0 to disable file rotation.
func LogFile(filename string, max_size_mb uint, max_rotation uint) (io.Writer, error) {
	max_size := int64(max_size_mb * 1048576)
	fpath, _ := filepath.Split(filename)

	if err := mkDir(fpath); err != nil {
		return nil, err
	}

	file, err := wrotate.OpenFile(filename, max_size, max_rotation)
	if err == nil {
		Defer(file.Close)
	}
	return file, err
}

// False writer for discarding output.
var None dummyWriter

type dummyWriter struct{}

func (dummyWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

// Retrieve first matching logger.
func getLogger(flag uint32) *_logger {
	mutex.Lock()
	defer mutex.Unlock()
	for k, v := range l_map {
		if flag&k == k {
			return v
		}
	}
	return nil
}

// Updates logger.
func updateLogger(flag uint32, field uint32, input interface{}) {
	mutex.Lock()
	defer mutex.Unlock()
	for k, v := range l_map {
		if flag&k == k {
			switch field {
			case textWriter:
				if x, ok := input.(io.Writer); ok {
					v.textout = x
				} else {
					return
				}
			case fileWriter:
				if x, ok := input.(io.WriteCloser); ok {
					v.fileout = x
				} else {
					return
				}
			case setTimestamp:
				if x, ok := input.(bool); ok {
					v.use_ts = x
				} else {
					return
				}
			case setPrefix:
				if x, ok := input.(string); ok {
					v.prefix = x
				} else {
					return
				}
			default:
				return
			}
		}
	}
}

// Returns log output for text.
func GetOutput(flag uint32) io.Writer {
	t := getLogger(flag)
	return t.textout
}

// Returns log file output.
func GetFile(flag uint32) io.Writer {
	t := getLogger(flag)
	return t.fileout
}

// Enable Timestamp on output.
func ShowTS(flag ...uint32) {
	if len(flag) == 0 {
		flag = append(flag, ALL)
	}
	updateLogger(flag[0], setTimestamp, true)
}

// Disable Timestamp on output.
func HideTS(flag ...uint32) {
	if len(flag) == 0 {
		flag = append(flag, ALL)
	}
	updateLogger(flag[0], setTimestamp, false)
}

// Enable a specific logger.
func SetOutput(flag uint32, w io.Writer) {
	updateLogger(flag, textWriter, w)
}

func SetFile(flag uint32, input io.Writer) {
	updateLogger(flag, fileWriter, input)
}

// Specify which logs to send to syslog.
func EnableExport(flag uint32) {
	mutex.Lock()
	defer mutex.Unlock()
	enabled_exports = enabled_exports | flag
}

// Specific which logger to not export.
func DisableExport(flag uint32) {
	mutex.Lock()
	defer mutex.Unlock()
	enabled_exports = enabled_exports & ^flag
}

func SetTZ(location string) (err error) {
	mutex.Lock()
	defer mutex.Unlock()
	tz := timezone
	timezone, err = time.LoadLocation(location)
	if err != nil {
		timezone = tz
	}
	return
}

// Switches timestamps to local timezone. (Default Setting)
func LTZ() {
	mutex.Lock()
	defer mutex.Unlock()
	timezone = time.Local
}

// Switches logger to use UTC instead of local timezone.
func UTC() {
	mutex.Lock()
	defer mutex.Unlock()
	timezone = time.UTC
}

// Generate TS Bytes
func genTS(in *[]byte) {
	CT := time.Now().In(timezone)

	year, mon, day := CT.Date()
	hour, min, sec := CT.Clock()

	ts := in

	*ts = append(*ts, '[')
	Itoa(ts, year, 4)
	*ts = append(*ts, '/')
	Itoa(ts, int(mon), 2)
	*ts = append(*ts, '/')
	Itoa(ts, day, 2)
	*ts = append(*ts, ' ')
	Itoa(ts, hour, 2)
	*ts = append(*ts, ':')
	Itoa(ts, min, 2)
	*ts = append(*ts, ':')
	Itoa(ts, sec, 2)
	*ts = append(*ts, ' ')

	zone, _ := CT.Zone()
	*ts = append(*ts, []byte(zone)[0:]...)
	*ts = append(*ts, []byte("] ")[0:]...)
}

// Change prefix for specified logger.
func SetPrefix(logger uint32, prefix_str string) {
	updateLogger(logger, setPrefix, prefix_str)
}

// Don't log, write text to standard error which will be overwritten on the next output.
func Flash(vars ...interface{}) {
	write2log(_flash_txt|_no_logging, vars...)
}

// Don't log, just print text to standard out.
func Stdout(vars ...interface{}) {
	write2log(_print_txt|_no_logging, vars...)
}

// Don't log, just print text to standard error.
func Stderr(vars ...interface{}) {
	write2log(_stderr_txt|_no_logging, vars...)
}

// Log as Info.
func Log(vars ...interface{}) {
	write2log(INFO, vars...)
}

// Log as Error.
func Err(vars ...interface{}) {
	write2log(ERROR, vars...)
}

// Log as Warn.
func Warn(vars ...interface{}) {
	write2log(WARN, vars...)
}

// Log as Notice.
func Notice(vars ...interface{}) {
	write2log(NOTICE, vars...)
}

// Log as Info, as auxilary output.
func Aux(vars ...interface{}) {
	write2log(AUX, vars...)
}

// Log as Info, as auxilary output.
func Aux2(vars ...interface{}) {
	write2log(AUX2, vars...)
}

// Log as Info, as auxilary output.
func Aux3(vars ...interface{}) {
	write2log(AUX3, vars...)
}

// Log as Info, as auxilary output.
func Aux4(vars ...interface{}) {
	write2log(AUX4, vars...)
}

// Log as Fatal, then quit.
func Fatal(vars ...interface{}) {
	if atomic.CompareAndSwapInt32(&fatal_triggered, 0, 1) {
		// Defer fatal output, so it is the last log entry displayed.
		write2log(FATAL|_bypass_lock, vars...)
		signalChan <- os.Kill
		<-exit_lock
		os.Exit(1)
	}
}

// Log as Debug.
func Debug(vars ...interface{}) {
	write2log(DEBUG, vars...)
}

// Log as Trace.
func Trace(vars ...interface{}) {
	write2log(TRACE, vars...)
}

// fprintf
func fprintf(buffer io.Writer, vars ...interface{}) {
	vlen := len(vars)

	if vlen == 0 {
		fmt.Fprintf(buffer, "")
		vlen = 1
	} else if vlen == 1 {
		if o, ok := vars[0].([]byte); ok {
			buffer.Write(o)
		} else {
			fmt.Fprintf(buffer, "%v", vars[0])
		}
	} else {
		str, ok := vars[0].(string)
		if ok {
			fmt.Fprintf(buffer, str, vars[1:]...)
		} else {
			for n, item := range vars {
				if n == 0 || n == vlen-1 {
					fmt.Fprintf(buffer, "%v", item)
				} else {
					fmt.Fprintf(buffer, "%v, ", item)
				}
			}
		}
	}
}

// Prepares output text and sends to appropriate logging destinations.
func write2log(flag uint32, vars ...interface{}) {

	if atomic.LoadInt32(&fatal_triggered) == 1 {
		if flag&_bypass_lock != 0 {
			flag ^= _bypass_lock
		} else {
			return
		}
	}

	flag = flag &^ _bypass_lock

	mutex.Lock()
	defer mutex.Unlock()

	logger := l_map[flag&^_no_logging]

	var pre []byte

	if flag&_no_logging != _no_logging {
		if logger.use_ts {
			genTS(&pre)
		}
		pre = append(pre, []byte(logger.prefix)[0:]...)
	}

	// Reset buffer.
	msgBuffer.Reset()

	// Create output string.
	fprintf(&msgBuffer, vars...)

	// Copy original output for export.
	msg := msgBuffer.String()

	output := msgBuffer.Bytes()
	output = append(pre, output[0:]...)
	bufferLen := len(output)

	if bufferLen > 0 {
		if output[len(output)-1] != '\n' && flag&_flash_txt != _flash_txt {
			output = append(output, '\n')
			bufferLen++
		}
	} else if flag&_flash_txt != _flash_txt {
		output = append(output, '\n')
		bufferLen++
	}

	// Clear out last flash text.
	if flush_needed && !piped_stderr && ((logger.textout == os.Stdout && !piped_stdout) || logger.textout == os.Stderr) {
		width := termWidth()
		for i := len(flush_line); i < width; i++ {
			flush_line = append(flush_line[0:], ' ')
		}
		fmt.Fprintf(os.Stderr, "\r%s\r", string(flush_line[0:width-1]))
		flush_needed = false
	}

	// Flash text handler, make a line of text available to remove remnents of this text.
	if flag&_flash_txt != 0 {
		if !piped_stderr {
			width := termWidth()
			if utf8.RuneCount(output) > width {
				output = output[0:width]
			}
			io.Copy(os.Stderr, bytes.NewReader(output))
			flush_needed = true
			return
		}
		return
	}

	io.Copy(logger.textout, bytes.NewReader(output))
	if flag&_no_logging != 0 {
		return
	}

	// Preprend timestamp for file.
	if !logger.use_ts {
		out_len := len(output)
		genTS(&output)
		out := output[out_len:]
		out = append(out, output[0:out_len]...)
		output = out
	}

	// Write to file.
	_, err := io.Copy(logger.fileout, bytes.NewReader(output))
	// Launch fatal in a go routine, as the mutex is currently locked.
	if err != nil && FatalOnFileError {
		go Fatal(err)
	}

	if export_syslog != nil && enabled_exports&flag == flag {
		switch flag {
		case INFO:
			fallthrough
		case AUX:
			fallthrough
		case AUX2:
			fallthrough
		case AUX3:
			fallthrough
		case AUX4:
			err = export_syslog.Info(msg)
		case ERROR:
			err = export_syslog.Err(msg)
		case WARN:
			err = export_syslog.Warning(msg)
		case FATAL:
			err = export_syslog.Emerg(msg)
		case NOTICE:
			err = export_syslog.Notice(msg)
		case DEBUG:
			err = export_syslog.Debug(msg)
		case TRACE:
			err = export_syslog.Debug(msg)
		}
		if err != nil && FatalOnExportError {
			go Fatal(err)
		}
	}

}
