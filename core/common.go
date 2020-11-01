package core

import (
	"archive/zip"
	"compress/flate"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/cmcoffee/go-snuglib/cfg"
	"github.com/cmcoffee/go-snuglib/eflag"
	"github.com/cmcoffee/go-snuglib/nfo"
	"github.com/cmcoffee/go-snuglib/xsync"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Menu item flags
type FlagSet struct {
	FlagArgs []string
	*eflag.EFlagSet
}

// Allows overriding of the Filename.
func (F FileInfo) Name() string {
	if F.string != NONE {
		return F.string
	} else {
		return F.FileInfo.Name()
	}
}

// Parse flags assocaited with task.
func (f *FlagSet) Parse() (err error) {
	if err = f.EFlagSet.Parse(f.FlagArgs[0:]); err != nil {
		return err
	}
	return nil
}

// Passport is the handoff to the task including a task report, a session and a database.
type Passport struct {
	*TaskReport
	KWSession
	SubStore
}

// Task Interface
type Task interface {
	Init(*FlagSet) error
	Main(Passport) error
	New() Task
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

type FileInfo struct {
	string
	os.FileInfo
}

// Returns please wait prompt back to default setting.
func DefaultPleaseWait() {
	PleaseWait.Set(func() string { return "Please wait ..." }, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
}

// Scans parent_folder for all subfolders and files.
func ScanPath(parent_folder string) (folders []string, files []FileInfo) {
	parent_folder, _ = filepath.Abs(parent_folder)
	folders = []string{parent_folder}

	var n int
	nextFolder := func() (output string) {
		if n < len(folders) {
			output = folders[n]
			n++
			return
		}
		return NONE
	}

	files = make([]FileInfo, 0)

	for {
		folder := nextFolder()
		if folder == NONE {
			break
		}
		data, err := ioutil.ReadDir(folder)
		if err != nil && !os.IsNotExist(err) {
			Err(err)
			continue
		}
		for _, finfo := range data {
			if finfo.IsDir() {
				folders = append(folders, fmt.Sprintf("%s%s%s", folder, SLASH, finfo.Name()))
			} else {
				files = append(files, FileInfo{fmt.Sprintf("%s%s%s", folder, SLASH, finfo.Name()), finfo})
			}
		}
	}

	return folders, files
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

// Compresses Folder to File
func CompressFolder(input_folder, dest_file string) (err error) {
	input_folder, err = filepath.Abs(input_folder)
	if err != nil {
		return err
	}
	_, files := ScanPath(input_folder)

	f, err := os.OpenFile(dest_file, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return err
	}

	w := zip.NewWriter(f)
	w.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.NoCompression)
	})

	buf := make([]byte, 4096)

	for _, file := range files {
		Log("Flattening %s -> %s ...", file.string, dest_file)

		z, err := w.Create(file.string)
		if err != nil {
			return err
		}

		r, err := os.Open(file.string)
		if err != nil {
			return err
		}

		tm := transferMonitor(fmt.Sprintf("%s", file.Name()), file.Size(), noRate, r)
		_, err = io.CopyBuffer(z, tm, buf)
		tm.Close()

		if err != nil {
			nfo.Err(err)
			continue
		}
	}
	w.Close()
	return
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

func IsBlank(input string) bool {
	return len(input) == 0
}

func Dequote(input *string) {
	if len(*input) > 0 && (*input)[0] == '"' {
		*input = (*input)[:1]
	}
	if len(*input) > 0 && (*input)[len(*input)-1] == '"' {
		*input = (*input)[:len(*input)-1]
	}
}
