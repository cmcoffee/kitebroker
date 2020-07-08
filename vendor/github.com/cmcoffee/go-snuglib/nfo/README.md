# nfo
--
    import "github.com/cmcoffee/go-snuglib/nfo"

Simple package to get user input from terminal.

## Usage

```go
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

)
```

```go
const (
	STD = INFO | ERROR | WARN | NOTICE | FATAL | AUX | AUX2 | AUX3 | AUX4
	ALL = INFO | ERROR | WARN | NOTICE | FATAL | AUX | AUX2 | AUX3 | AUX4 | DEBUG | TRACE
)
```
Standard Loggers, minus debug and trace.

```go
const (
	LeftToRight = 1 << iota // Display progress bar left to right.
	RightToLeft             // Display progress bar right to left.
	NoRate                  // Do not show transfer rate, left to right.

)
```

```go
var (
	FatalOnFileError   = true // Fatal on log file or file rotation errors.
	FatalOnExportError = true // Fatal on export/syslog error.

)
```

```go
var None dummyWriter
```
False writer for discarding output.

```go
var PleaseWait *loading
```
PleaseWait is a wait prompt to display between requests.

```go
var ProgressBar *progressBar
```

#### func  Aux

```go
func Aux(vars ...interface{})
```
Log as Info, as auxilary output.

#### func  Aux2

```go
func Aux2(vars ...interface{})
```
Log as Info, as auxilary output.

#### func  Aux3

```go
func Aux3(vars ...interface{})
```
Log as Info, as auxilary output.

#### func  Aux4

```go
func Aux4(vars ...interface{})
```
Log as Info, as auxilary output.

#### func  BlockShutdown

```go
func BlockShutdown()
```
Global wait group, allows running processes to finish up tasks before app
shutdown

#### func  Debug

```go
func Debug(vars ...interface{})
```
Log as Debug.

#### func  Defer

```go
func Defer(closer interface{}) func() error
```
Adds a function to the global defer, function must take no arguments and either
return nothing or return an error. Returns function to be called by local
keyword defer if you want to run it now and remove it from global defer.

#### func  DisableExport

```go
func DisableExport(flag uint32)
```
Specific which logger to not export.

#### func  DrawProgressBar

```go
func DrawProgressBar(sz int, current, max int64, text string) string
```
Draws a progress bar using sz as the size.

#### func  EnableExport

```go
func EnableExport(flag uint32)
```
Specify which logs to send to syslog.

#### func  Err

```go
func Err(vars ...interface{})
```
Log as Error.

#### func  Exit

```go
func Exit(exit_code int)
```
Intended to be a defer statement at the begining of main, but can be called at
anytime with an exit code. Tries to catch a panic if possible and log it as a
fatal error, then proceeds to send a signal to the global defer/shutdown handler

#### func  Fatal

```go
func Fatal(vars ...interface{})
```
Log as Fatal, then quit.

#### func  Flash

```go
func Flash(vars ...interface{})
```
Don't log, write text to standard error which will be overwritten on the next
output.

#### func  GetConfirm

```go
func GetConfirm(prompt string) bool
```
Get confirmation

#### func  GetFile

```go
func GetFile(flag uint32) io.Writer
```
Returns log file output.

#### func  GetInput

```go
func GetInput(prompt string) string
```
Gets user input, used during setup and configuration.

#### func  GetOutput

```go
func GetOutput(flag uint32) io.Writer
```
Returns log output for text.

#### func  GetSecret

```go
func GetSecret(prompt string) string
```
Get Hidden/Password input, without returning information to the screen.

#### func  HideTS

```go
func HideTS(flag ...uint32)
```
Disable Timestamp on output.

#### func  HookSyslog

```go
func HookSyslog(syslog_writer SyslogWriter)
```
Send messages to syslog

#### func  HumanSize

```go
func HumanSize(bytes int64) string
```
Provides human readable file sizes.

#### func  LTZ

```go
func LTZ()
```
Switches timestamps to local timezone. (Default Setting)

#### func  Log

```go
func Log(vars ...interface{})
```
Log as Info.

#### func  LogFile

```go
func LogFile(filename string, max_size_mb uint, max_rotation uint) (io.Writer, error)
```
Opens a new log file for writing, max_size is threshold for rotation,
max_rotation is number of previous logs to hold on to. Set max_size_mb to 0 to
disable file rotation.

#### func  NeedAnswer

```go
func NeedAnswer(prompt string, request func(prompt string) string) (output string)
```
Loop until a non-blank answer is given

#### func  Notice

```go
func Notice(vars ...interface{})
```
Log as Notice.

#### func  PressEnter

```go
func PressEnter(prompt string)
```
Prompt to press enter.

#### func  SetFile

```go
func SetFile(flag uint32, input io.Writer)
```

#### func  SetOutput

```go
func SetOutput(flag uint32, w io.Writer)
```
Enable a specific logger.

#### func  SetPrefix

```go
func SetPrefix(logger uint32, prefix_str string)
```
Change prefix for specified logger.

#### func  SetSignals

```go
func SetSignals(sig ...os.Signal)
```
Sets the signals that we listen for.

#### func  SetTZ

```go
func SetTZ(location string) (err error)
```

#### func  ShowTS

```go
func ShowTS(flag ...uint32)
```
Enable Timestamp on output.

#### func  SignalCallback

```go
func SignalCallback(signal os.Signal, callback func() (continue_shutdown bool))
```
Set a callback function(no arguments) to run after receiving a specific syscall,
function returns true to continue shutdown process.

#### func  Stderr

```go
func Stderr(vars ...interface{})
```
Don't log, just print text to standard error.

#### func  Stdout

```go
func Stdout(vars ...interface{})
```
Don't log, just print text to standard out.

#### func  Trace

```go
func Trace(vars ...interface{})
```
Log as Trace.

#### func  UTC

```go
func UTC()
```
Switches logger to use UTC instead of local timezone.

#### func  UnblockShutdown

```go
func UnblockShutdown()
```
Task completed, carry on with shutdown.

#### func  UnhookSyslog

```go
func UnhookSyslog()
```
Disconnect form syslog

#### func  Warn

```go
func Warn(vars ...interface{})
```
Log as Warn.

#### type ReadSeekCloser

```go
type ReadSeekCloser interface {
	Seek(offset int64, whence int) (int64, error)
	Read(p []byte) (n int, err error)
	Close() error
}
```


#### func  TransferMonitor

```go
func TransferMonitor(name string, total_size int64, flag int, source ReadSeekCloser) ReadSeekCloser
```
Add Transfer to transferDisplay. Parameters are "name" displayed for file
transfer, "limit_sz" for when to pause transfer (aka between calls/chunks), and
"total_sz" the total size of the transfer.

#### type SyslogWriter

```go
type SyslogWriter interface {
	Alert(string) error
	Crit(string) error
	Debug(string) error
	Emerg(string) error
	Err(string) error
	Info(string) error
	Notice(string) error
	Warning(string) error
}
```

Interface for log/syslog/Writer.
