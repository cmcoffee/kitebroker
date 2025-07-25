package core

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/cmcoffee/snugforge/cfg"
	"github.com/cmcoffee/snugforge/eflag"
	"github.com/cmcoffee/snugforge/nfo"
	"github.com/cmcoffee/snugforge/swapreader"
	"github.com/cmcoffee/snugforge/xsync"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// err_table stores error messages for reporting.
var err_table *Table

// SetErrTable sets the error table.
// It drops any existing data in the provided table.
func SetErrTable(input Table) {
	err_table = &input
	err_table.Drop()
}

// FlagSet encapsulates command-line flags and provides parsing functionality.
type FlagSet struct {
	FlagArgs []string
	*eflag.EFlagSet
}

// KiteBrokerTask encapsulates the components required to execute a Kite broker task.
// It manages flags, database connections, reporting, Kite Web Session, and rate limiting.
type KiteBrokerTask struct {
	Flags   FlagSet
	DB      Database
	Cache   Database
	Report  *TaskReport
	KW      KWSession
	Limiter LimitGroup
}

// Get returns the KiteBrokerTask instance itself.
// It enables method chaining or direct access to the task.
func (T *KiteBrokerTask) Get() *KiteBrokerTask {
	return T
}

// SetLimiter sets the limiter for the KiteBrokerTask with the given limit.
// It creates a new LimitGroup with the specified limit and assigns it to
// the task's Limiter field.
func (T *KiteBrokerTask) SetLimiter(limit int) {
	T.Limiter = NewLimitGroup(limit)
}

// Wait blocks until a permit is available from the limiter.
func (T *KiteBrokerTask) Wait() {
	if T.Limiter == nil {
		return
	}
	T.Limiter.Wait()
}

// Try attempts to acquire a permit from the rate limiter.
// Returns true if a permit was acquired, false otherwise.
func (T *KiteBrokerTask) Try() bool {
	if T.Limiter == nil {
		return false
	}
	return T.Limiter.Try()
}

// Done signals the completion of a task, decrementing the limiter if present.
func (T *KiteBrokerTask) Done() {
	if T.Limiter != nil {
		T.Limiter.Done()
	}
}

// Add increments the limiter by the given input value.
// If the limiter is nil, it initializes it with a default limit of 50.
func (T *KiteBrokerTask) Add(input int) {
	if T.Limiter == nil {
		T.SetLimiter(50)
	}
	T.Limiter.Add(input)
}

// Parse parses the command-line arguments.
func (f *FlagSet) Parse() (err error) {
	if err = f.EFlagSet.Parse(f.FlagArgs[0:]); err != nil {
		return err
	}
	return nil
}

// MyRoot returns the absolute path to the root directory of the executable.
// file. It uses os.Executable() to get the path to the executable, then
// filepath.Abs and filepath.Dir to determine the root directory, and finally
// filepath.ToSlash to convert it to a standardized path.
func MyRoot() string {
	exec, err := os.Executable()
	Critical(err)

	root, err := filepath.Abs(filepath.Dir(exec))
	Critical(err)

	return GetPath(root)
}

// Text returns the underlying command-line arguments.
func (f *FlagSet) Text() (output []string) {
	return f.FlagArgs
}

// Task defines the interface for Kite broker tasks.
// It provides methods for creating, initializing, and running tasks.
type Task interface {
	New() Task
	Get() *KiteBrokerTask
	Name() string
	Desc() string
	Init() error
	Main() error
}

// TaskArgs represents arguments passed to a task.
// It is a map of string keys to interface{} values,
// allowing for flexible task configuration.
type TaskArgs map[string]interface{}

// GetBodyBytes returns a function that returns an io.ReadCloser
// for the given byte slice.
func GetBodyBytes(input []byte) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(input)), nil
	}
}

// RunTask executes the given task with provided database, report, and arguments.
// It initializes the task, sets dependencies, and runs the main logic.
func (K KWSession) RunTask(input Task, db Database, report *TaskReport, args ...map[string]interface{}) (err error) {
	var arg_string []string
	for _, arg := range args {
		for k, v := range arg {
			var dash string
			if len(k) == 1 {
				dash = "-"
			} else {
				dash = "--"
			}
			switch x := v.(type) {
			case string:
				if k == "args" {
					arg_string = append(arg_string, x)
				} else {
					arg_string = append(arg_string, fmt.Sprintf("%s%s=%s", dash, k, x))
				}
			case []string:
				for _, line := range x {
					arg_string = append(arg_string, fmt.Sprintf("%s%s=%s", dash, k, line))
				}
			default:
				arg_string = append(arg_string, fmt.Sprintf("%s%s=%v", dash, k, v))
			}
		}
	}
	task := input.Get()
	task.DB = db
	flags := FlagSet{EFlagSet: eflag.NewFlagSet("SubTask", eflag.ReturnErrorOnly)}
	flags.FlagArgs = arg_string
	task.Flags = flags
	if err = input.Init(); err != nil {
		return err
	}
	task.KW = K
	task.Report = report
	if err = input.Main(); err != nil {
		return err
	}
	return nil
}

// NONE is an empty string representing no path separator.
// SLASH is the operating system's path separator.
const (
	NONE  = ""
	SLASH = string(os.PathSeparator)
)

var (
	Log             = nfo.Log             // Standard Log Output
	Fatal           = nfo.Fatal           // Fatal Log Output & Exit.
	Notice          = nfo.Notice          // Notice Log Output
	Flash           = nfo.Flash           // Flash to Stderr
	Stdout          = nfo.Stdout          // Send to Stdout
	Warn            = nfo.Warn            // Warn Log Output
	Defer           = nfo.Defer           // Global Application Deffer
	Debug           = nfo.Debug           // Debug Log Output
	Trace           = nfo.Trace           // Trace Log Output
	Exit            = nfo.Exit            // End Application, Run Global Defer.
	PleaseWait      = nfo.PleaseWait      // Set Loading Prompt
	Stderr          = nfo.Stderr          // Send to Stderr
	ProgressBar     = nfo.NewProgressBar  // Set Progress Bar animation
	Path            = filepath.Clean      // Provide clean path
	TransferCounter = nfo.TransferCounter // Tranfer Animation
	NewLimitGroup   = xsync.NewLimitGroup // Limiter Group
	FormatPath      = filepath.FromSlash  // Convert to standard path with *nix style delimiters.
	GetPath         = filepath.ToSlash    // Conver to OS specific path with correct slash delimiters.
	Info            = nfo.Aux             // Log as standrd INFO
	HumanSize       = nfo.HumanSize       // Convert bytes int64 to B/KB/MB/GB/TB.
)

var (
	transferMonitor = nfo.TransferMonitor
	leftToRight     = nfo.LeftToRight // Transfer Monitor Direction
	rightToLeft     = nfo.RightToLeft // Transfer Monitor Direction
	nopSeeker       = nfo.NopSeeker   // Transform ReadCloser to ReadSeekCloser
	noRate          = nfo.NoRate      // Transfer Monitor ProgressBar
)

// NewFlagSet is a function that returns a new flag set.
// ReturnErrorOnly is a function that returns only the error.
var (
	NewFlagSet      = eflag.NewFlagSet
	ReturnErrorOnly = eflag.ReturnErrorOnly
)

// BitFlag is an alias for xsync.BitFlag.
// LimitGroup is an alias for xsync.LimitGroup.
// ConfigStore is an alias for cfg.Store.
// ReadSeekCloser is an alias for nfo.ReadSeekCloser.
// SwapReader is an alias for swapreader.Reader.
type (
	BitFlag        = xsync.BitFlag
	LimitGroup     = xsync.LimitGroup
	ConfigStore    = cfg.Store
	ReadSeekCloser = nfo.ReadSeekCloser
	SwapReader     = swapreader.Reader
)

// error_counter tracks the number of errors encountered.
var error_counter uint32

// ErrCount Returns amount of times Err has been triggered.
func ErrCount() uint32 {
	return atomic.LoadUint32(&error_counter)
}

// Err Log Standard Error, adds counter to ErrCount()
func Err(input ...interface{}) {
	atomic.AddUint32(&error_counter, 1)
	msg := nfo.Stringer(input...)
	nfo.Err(msg)
	if err_table != nil {
		err_table.Set(fmt.Sprintf("%d", atomic.LoadUint32(&error_counter)), fmt.Sprintf("<%v> %s", time.Now().Round(time.Second), msg))
	}
}

// StringDate Converts string to date.
func StringDate(input string) (output time.Time, err error) {
	if input == NONE {
		return
	}
	output, err = time.Parse(time.RFC3339, fmt.Sprintf("%sT00:00:00Z", input))
	if err != nil {
		if strings.Contains(err.Error(), "parse") {
			err = fmt.Errorf("Invalid date specified, should be in format: YYYY-MM-DD")
		} else {
			err_split := strings.Split(err.Error(), ":")
			err = fmt.Errorf("Invalid date specified:%s", err_split[len(err_split)-1])
		}
	}
	return
}

// Critical Fatal Error Check
func Critical(err error) {
	if err != nil {
		Fatal(err)
	}
}

// SplitPath Splits path up
func SplitPath(path string) (folder_path []string) {
	if strings.Contains(path, "/") {
		path = strings.TrimSuffix(path, "/")
		folder_path = strings.Split(path, "/")
	} else {
		path = strings.TrimSuffix(path, "\\")
		folder_path = strings.Split(path, "\\")
	}
	if len(folder_path) == 0 {
		folder_path = append(folder_path, path)
	}
	for i := 0; i < len(folder_path); i++ {
		if folder_path[i] == NONE {
			folder_path = append(folder_path[:i], folder_path[i+1:]...)
		}
	}
	return
}

// DefaultPleaseWait Returns please wait prompt back to default setting.
func DefaultPleaseWait() {
	PleaseWait.Set(func() string { return "Please wait ..." }, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
}

// MD5Sum function for checking files against appliance.
func MD5Sum(filename string) (sum string, err error) {
	checkSum := md5.New()
	file, err := os.Open(filename)
	if err != nil {
		return
	}

	var (
		o int64
		n int
		r int
	)

	for tmp := make([]byte, 16384); ; {
		r, err = file.ReadAt(tmp, o)

		if err != nil && err != io.EOF {
			return NONE, err
		}

		if r == 0 {
			break
		}

		tmp = tmp[0:r]
		n, err = checkSum.Write(tmp)
		if err != nil {
			return NONE, err
		}
		o = o + int64(n)
	}

	if err != nil && err != io.EOF {
		return NONE, err
	}

	md5sum := checkSum.Sum(nil)

	s := make([]byte, hex.EncodedLen(len(md5sum)))
	hex.Encode(s, md5sum)

	return string(s), nil
}

// RandBytes Generates a random byte slice of length specified.
func RandBytes(sz int) []byte {
	if sz <= 0 {
		sz = 16
	}

	ch := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+/"
	chlen := len(ch)

	rand_string := make([]byte, sz)
	rand.Read(rand_string)

	for i, v := range rand_string {
		rand_string[i] = ch[v%byte(chlen)]
	}
	return rand_string
}

// Error handler for const errors.
type Error string

func (e Error) Error() string { return string(e) }

// MkDir Creates folders.
func MkDir(name ...string) (err error) {
	for _, path := range name {
		err = os.MkdirAll(path, 0766)
		if err != nil {
			subs := strings.Split(path, string(os.PathSeparator))
			for i := 0; i < len(subs); i++ {
				p := strings.Join(subs[0:i+1], string(os.PathSeparator))
				if p == "" {
					p = "."
				}
				f, err := os.Stat(p)
				if err != nil {
					if os.IsNotExist(err) {
						err = os.Mkdir(p, 0766)
						if err != nil && !os.IsExist(err) {
							return err
						}
					} else {
						return err
					}
				}
				if f != nil && !f.IsDir() {
					return fmt.Errorf("mkdir: %s: file exists", f.Name())
				}
			}
		}
	}
	return nil
}

// ReadKWTime Parse Timestamps from kiteworks
func ReadKWTime(input string) (time.Time, error) {
	input = strings.Replace(input, "+0000", "Z", 1)
	return time.Parse(time.RFC3339, input)
}

// WriteKWTime Write timestamps for kiteworks.
func WriteKWTime(input time.Time) string {
	t := input.UTC().Format(time.RFC3339)
	return strings.Replace(t, "Z", "+0000", 1)
}

func PadZero(num int) string {
	if num < 10 {
		return fmt.Sprintf("0%d", num)
	} else {
		return fmt.Sprintf("%d", num)
	}
}

// DateString Create standard date YY-MM-DD out of time.Time.
func DateString(input time.Time) string {
	pad := func(num int) string {
		if num < 10 {
			return fmt.Sprintf("0%d", num)
		}
		return fmt.Sprintf("%d", num)
	}

	//due_time := input.AddDate(0, 0, -1)
	return fmt.Sprintf("%s-%s-%s", pad(input.Year()), pad(int(input.Month())), pad(input.Day()))
}

// CombinePath Combines several paths.
func CombinePath(name ...string) string {
	if name == nil {
		return NONE
	}
	if len(name) < 2 {
		return name[0]
	}
	return LocalPath(fmt.Sprintf("%s%s%s", name[0], SLASH, strings.Join(name[1:], SLASH)))
}

// LocalPath Adapts path to whatever local filesystem uses.
func LocalPath(path string) string {
	path = strings.Replace(path, "/", SLASH, -1)
	subs := strings.Split(path, SLASH)
	for i, v := range subs {
		subs[i] = strings.TrimSpace(v)
	}
	return strings.Join(subs, SLASH)
}

// NormalizePath Switches windows based slash to kiteworks compatible.
func NormalizePath(path string) string {
	path = strings.Replace(path, "\\", "/", -1)
	subs := strings.Split(path, "/")
	for i, v := range subs {
		subs[i] = strings.TrimSpace(v)
	}
	return strings.Join(subs, "/")
}

// Rename Path rename.
func Rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}

// IsBlank Confirms all strings handed to it are empty.
func IsBlank(input ...string) bool {
	for _, v := range input {
		if len(v) == 0 {
			return true
		}
	}
	return false
}

// Dequote Remove leading and trailing quotation marks on string.
func Dequote(input string) string {
	var output string
	output = input
	if len(output) > 0 && (output)[0] == '"' {
		output = output[1:]
	}
	if len(output) > 0 && (output)[len(output)-1] == '"' {
		output = output[:len(output)-1]
	}
	return output
}

func Delete(path string) error {
	return os.Remove(LocalPath(path))
}
