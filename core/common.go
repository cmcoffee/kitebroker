package core

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/cmcoffee/go-snuglib/cfg"
	"github.com/cmcoffee/go-snuglib/eflag"
	"github.com/cmcoffee/go-snuglib/nfo"
	"github.com/cmcoffee/go-snuglib/xsync"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Menu item flags
type FlagSet struct {
	FlagArgs []string
	SubStore
	*eflag.EFlagSet
}

type KiteBrokerTask struct {
	Flags  FlagSet
	DB     SubStore
	Report *TaskReport
	KW     KWSession
}

func (T *KiteBrokerTask) KiteBrokerTask_init_flags(Flags FlagSet) {
	T.Flags = Flags
}

func (T *KiteBrokerTask) KiteBrokerTask_init_db(DB SubStore) {
	T.DB = DB
}

func (T *KiteBrokerTask) KiteBrokerTask_init_report(task_name string, source string) {
	T.Report = NewTaskReport(task_name, source, &T.Flags)
}

func (T *KiteBrokerTask) KiteBrokerTask_init_session(user KWSession) {
	T.KW = user
}

func (T *KiteBrokerTask) KiteBrokerTask_report_summary(errors uint32) {
	if T.Report == nil {
		return
	}
	T.Report.Summary(errors)
}

// Parse flags assocaited with task.
func (f *FlagSet) Parse() (err error) {
	if err = f.EFlagSet.Parse(f.FlagArgs[0:]); err != nil {
		return err
	}
	return nil
}

// Task Interface
type Task interface {
	Init() error
	Main() error
	New() Task
	KiteBrokerTask_init_flags(Flags FlagSet)
	KiteBrokerTask_init_db(DB SubStore)
	KiteBrokerTask_init_report(task_name string, source string)
	KiteBrokerTask_init_session(user KWSession)
	KiteBrokerTask_report_summary(error uint32)
}

const (
	NONE  = ""
	SLASH = string(os.PathSeparator)
)

// Import from go-nfo.
var (
	Log             = nfo.Log
	Fatal           = nfo.Fatal
	Notice          = nfo.Notice
	Flash           = nfo.Flash
	Stdout          = nfo.Stdout
	Warn            = nfo.Warn
	Defer           = nfo.Defer
	Debug           = nfo.Debug
	Exit            = nfo.Exit
	PleaseWait      = nfo.PleaseWait
	Stderr          = nfo.Stderr
	ProgressBar     = nfo.ProgressBar
	Path            = filepath.Clean
	TransferCounter = nfo.TransferCounter
	NewLimitGroup   = xsync.NewLimitGroup
	FormatPath      = filepath.FromSlash
	GetPath         = filepath.ToSlash
	Info            = nfo.Aux
)

var (
	transferMonitor = nfo.TransferMonitor
	leftToRight     = nfo.LeftToRight
	rightToLeft     = nfo.RightToLeft
	noRate          = nfo.NoRate
)

type (
	ReadSeekCloser = nfo.ReadSeekCloser
	BitFlag        = xsync.BitFlag
	LimitGroup     = xsync.LimitGroup
	ConfigStore    = cfg.Store
)

var error_counter uint32

// Returns amount of times Err has been triggered.
func ErrCount() uint32 {
	return atomic.LoadUint32(&error_counter)
}

func Err(input ...interface{}) {
	atomic.AddUint32(&error_counter, 1)
	nfo.Err(input...)
}

// Converts string to date.
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

// Fatal Error Check
func Critical(err error) {
	if err != nil {
		Fatal(err)
	}
}

// Splits path up
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

// Returns please wait prompt back to default setting.
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

// Generates a random byte slice of length specified.
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

// Creates folders.
func MkDir(name ...string) (err error) {
	for _, path := range name {
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
					if err != nil {
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
	return nil
}

// Parse Timestamps from kiteworks
func ReadKWTime(input string) (time.Time, error) {
	input = strings.Replace(input, "+0000", "Z", 1)
	return time.Parse(time.RFC3339, input)
}

// Write timestamps for kiteworks.
func WriteKWTime(input time.Time) string {
	t := input.UTC().Format(time.RFC3339)
	return strings.Replace(t, "Z", "+0000", 1)
}

// Create standard date YY-MM-DD out of time.Time.
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

func CombinePath(name ...string) string {
	if name == nil {
		return NONE
	}
	if len(name) < 2 {
		return name[0]
	}
	return fmt.Sprintf("%s%s%s", name[0], SLASH, strings.Join(name[1:], SLASH))
}

// Provides human readable file sizes.
func HumanSize(bytes int64) string {

	names := []string{
		"Bytes",
		"KB",
		"MB",
		"GB",
	}

	suffix := 0
	size := float64(bytes)

	for size >= 1000 && suffix < len(names)-1 {
		size = size / 1000
		suffix++
	}

	return fmt.Sprintf("%.1f%s", size, names[suffix])
}

func Rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}

func IsBlank(input ...string) bool {
	for _, v := range input {
		if len(v) == 0 {
			return true
		}
	}
	return false
}

func Dequote(input *string) {
	if len(*input) > 0 && (*input)[0] == '"' {
		*input = (*input)[:1]
	}
	if len(*input) > 0 && (*input)[len(*input)-1] == '"' {
		*input = (*input)[:len(*input)-1]
	}
}
