package core

import (
	"archive/zip"
	"compress/flate"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/cmcoffee/go-snuglib/eflag"
	"github.com/cmcoffee/go-snuglib/kvlite"
	"github.com/cmcoffee/go-snuglib/nfo"
	"github.com/cmcoffee/go-snuglib/xsync"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Menu item flags
type FlagSet struct {
	Args []string
	*eflag.EFlagSet
}

// Prase flags assocaited with task.
func (f *FlagSet) Parse() (err error) {
	if err = f.EFlagSet.Parse(f.Args[0:]); err != nil {
		return err
	}
	return nil
}

// Creates passport to send to task module.
func NewPassport(task_name string, source string, user KWSession, db SubStore) Passport {
	return Passport{
		&TaskReport{
			task_name,
			source,
			time.Now().Round(time.Millisecond),
			make([]*Tally, 0),
		},
		user,
		db,
	}
}

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

// SubStore is a TaskStore, a database with a prefix in the table.
type SubStore struct {
	prefix string
	db     Database
}

func (d *Database) Sub(prefix string) SubStore {
	return SubStore{fmt.Sprintf("%s.", prefix), *d}
}

// applies prefix of table to calls.
func (d SubStore) apply_prefix(table string) string {
	return fmt.Sprintf("%s%s", d.prefix, table)
}

// Applies additional prefix to table.
func (d SubStore) Sub(prefix string) SubStore {
	return SubStore{
		prefix: fmt.Sprintf("%s%s.", d.prefix, prefix),
		db:     d.db,
	}
}

// Spins off a new shared table.
func (d SubStore) Shared(table string) SubStore {
	return SubStore{
		prefix: fmt.Sprintf("-shared.%s.", table),
		db:     d.db,
	}
}

// DB Wrappers to perform fatal error checks on each call.
func (d SubStore) Drop(table string) {
	d.db.Drop(d.apply_prefix(table))
}

// Encrypt value to go-kvlie, fatal on error.
func (d SubStore) CryptSet(table, key string, value interface{}) {
	d.db.CryptSet(d.apply_prefix(table), key, value)
}

// Save value to go-kvlite.
func (d SubStore) Set(table, key string, value interface{}) {
	d.db.Set(d.apply_prefix(table), key, value)
}

// Retrieve value from go-kvlite.
func (d SubStore) Get(table, key string, output interface{}) bool {
	found := d.db.Get(d.apply_prefix(table), key, output)
	return found
}

// List keys in go-kvlite.
func (d SubStore) Keys(table string) []string {
	keylist := d.db.Keys(d.apply_prefix(table))
	return keylist
}

// Count keys in table.
func (d SubStore) CountKeys(table string) int {
	count := d.db.CountKeys(d.apply_prefix(table))
	return count
}

// List Tables in DB
func (d SubStore) Tables() []string {
	var tables []string

	for _, t := range d.db.Tables() {
		if strings.HasPrefix(t, d.prefix) {
			tables = append(tables, strings.TrimPrefix(t, d.prefix))
		}
	}
	return tables
}

// Delete value from go-kvlite.
func (d SubStore) Unset(table, key string) {
	d.db.Unset(d.apply_prefix(table), key)
}

// Drill in to specific table.
func (d SubStore) Table(table string) Table {
	return d.db.Table(d.apply_prefix(table))
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
	GetSecret       = nfo.GetSecret
	GetInput        = nfo.GetInput
	Exit            = nfo.Exit
	PleaseWait      = nfo.PleaseWait
	Stderr          = nfo.Stderr
	GetConfirm      = nfo.GetConfirm
	HideTS          = nfo.HideTS
	ShowTS          = nfo.ShowTS
	ProgressBar     = nfo.ProgressBar
	TransferMonitor = nfo.TransferMonitor
	Path            = filepath.Clean
	LeftToRight     = nfo.LeftToRight
	RightToLeft     = nfo.RightToLeft
	NoRate          = nfo.NoRate
	NeedAnswer      = nfo.NeedAnswer
	PressEnter      = nfo.PressEnter
	NewLimitGroup   = xsync.NewLimitGroup
	NewFlagSet      = eflag.NewFlagSet
	ReturnErrorOnly = eflag.ReturnErrorOnly
)

type (
	ReadSeekCloser = nfo.ReadSeekCloser
	BitFlag        = xsync.BitFlag
	LimitGroup     = xsync.LimitGroup
)

// Enable Debug Logging Output
func EnableDebug() {
	nfo.SetOutput(nfo.DEBUG, os.Stdout)
	nfo.SetFile(nfo.DEBUG, nfo.GetFile(nfo.ERROR))
}

// Disables Flash from being displayed.
func Quiet() {
	Flash = func(vars ...interface{}) { return }
}

var error_counter uint32

func ResetErrorCount() {
	atomic.StoreUint32(&error_counter, 0)
}

// Returns amount of times Err has been triggered.
func ErrCount() uint32 {
	return atomic.LoadUint32(&error_counter)
}

func Err(input ...interface{}) {
	atomic.AddUint32(&error_counter, 1)
	nfo.Err(input...)
}

// Wrapper around go-kvlite.
type Database struct {
	db kvlite.Store
}

type Table struct {
	table kvlite.Table
}

func (t Table) Drop() {
	Critical(t.table.Drop())
}

func (t Table) Get(key string, value interface{}) bool {
	found, err := t.table.Get(key, value)
	Critical(err)
	return found
}

func (t Table) Set(key string, value interface{}) {
	Critical(t.table.Set(key, value))
}

func (t Table) CryptSet(key string, value interface{}) {
	Critical(t.table.CryptSet(key, value))
}

func (t Table) Unset(key string) {
	Critical(t.table.Unset(key))
}

func (t Table) Keys() []string {
	keys, err := t.table.Keys()
	Critical(err)
	return keys
}

func (t Table) CountKeys() int {
	count, err := t.table.CountKeys()
	Critical(err)
	return count
}

// Opens go-kvlite sqlite database.
func OpenDatabase(file string, padlock ...byte) (*Database, error) {
	db, err := kvlite.Open(file, padlock...)
	if err != nil {
		return nil, err
	}
	return &Database{db}, nil
}

// Opens go-kvlite database using mac address for lock.
func SecureDatabase(file string) (*Database, error) {
	// Provides us the mac address of the first interface.
	get_mac_addr := func() []byte {
		ifaces, err := net.Interfaces()
		Critical(err)

		for _, v := range ifaces {
			if len(v.HardwareAddr) == 0 {
				continue
			}
			return v.HardwareAddr
		}
		return nil
	}

	db, err := kvlite.Open(file, get_mac_addr()[0:]...)
	if err != nil {
		if err == kvlite.ErrBadPadlock {
			Notice("Hardware changes detected, you will need to reauthenticate.")
			if err := kvlite.CryptReset(file); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
		db, err = kvlite.Open(file, get_mac_addr()[0:]...)
		if err != nil {
			return nil, err
		}
	}
	return &Database{db}, nil
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

// Open a memory-only go-kvlite store.
func OpenCache() *Database {
	return &Database{kvlite.MemStore()}
}

// DB Wrappers to perform fatal error checks on each call.
func (d Database) Drop(table string) {
	Critical(d.db.Drop(table))
}

// Encrypt value to go-kvlie, fatal on error.
func (d Database) CryptSet(table, key string, value interface{}) {
	Critical(d.db.CryptSet(table, key, value))
}

// Save value to go-kvlite.
func (d Database) Set(table, key string, value interface{}) {
	Critical(d.db.Set(table, key, value))
}

// Retrieve value from go-kvlite.
func (d Database) Get(table, key string, output interface{}) bool {
	found, err := d.db.Get(table, key, output)
	Critical(err)
	return found
}

func (d Database) Table(table string) Table {
	return Table{table: d.db.Table(table)}
}

// List keys in go-kvlite.
func (d Database) Keys(table string) []string {
	keylist, err := d.db.Keys(table)
	Critical(err)
	return keylist
}

// Count keys in table.
func (d Database) CountKeys(table string) int {
	count, err := d.db.CountKeys(table)
	Critical(err)
	return count
}

// List Tables in DB
func (d Database) Tables() []string {
	tables, err := d.db.Tables()
	Critical(err)
	return tables
}

// Delete value from go-kvlite.
func (d Database) Unset(table, key string) {
	Critical(d.db.Unset(table, key))
}

// Closes go-kvlite database.
func (d Database) Close() {
	Critical(d.db.Close())
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
		folder_path = strings.Split(path, "/")
	} else {
		folder_path = strings.Split(path, "\\")
	}
	if len(folder_path) == 0 {
		folder_path = append(folder_path, path)
	}
	return
}

type FileInfo struct {
	Info os.FileInfo
	string
}

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
				files = append(files, FileInfo{finfo, fmt.Sprintf("%s%s%s", folder, SLASH, finfo.Name())})
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

		tm := TransferMonitor(fmt.Sprintf("%s", file.Info.Name()), file.Info.Size(), NoRate, r)
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
