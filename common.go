package main

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/cmcoffee/go-logger"
	"github.com/howeyc/gopass"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"strconv"
)

const (
	NONE  = ""
	SLASH = string(os.PathSeparator)
)

var loader = []string{
	"[>  ]",
	"[>> ]",
	"[>>>]",
	"[ >>]",
	"[  >]",
	"[   ]",
	"[  <]",
	"[ <<]",
	"[<<<]",
	"[<< ]",
	"[<  ]",
	"[   ]",
}

var show_loader = int32(0)

func init() {
	go func() {
		for {
			for _, str := range loader {
				if atomic.LoadInt32(&show_loader) == 1 {
					if snoop {
						goto Exit
					}
					logger.Put("\r%s Working, Please wait...", str)
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	Exit:
	}()
}

// Displays loader. "[>>>] Working, Please wait."
func ShowLoader() {
	atomic.CompareAndSwapInt32(&show_loader, 0, 1)
}

// Hides display loader.
func HideLoader() {
	atomic.CompareAndSwapInt32(&show_loader, 1, 0)
}

// Scans local path for all folders and files.
func scanPath(root_folder string) (folders []string, files []string) {
	folders = []string{FullPath(root_folder)}

	var n int

	nextFolder := func() (output string) {
		if n < len(folders) {
			output = folders[n]
			n++
			return
		}
		return NONE
	}
	
	files = make([]string, 0)

	for {
		folder := nextFolder()
		if folder == NONE {
			break
		}
		data, err := ioutil.ReadDir(folder)
		if err != nil && !os.IsNotExist(err) {
			logger.Err(err)
			continue
		}
		for _, finfo := range data {
			if finfo.IsDir() {
				if folder == local_path && finfo.Name() == "sent" { continue }
				folders = append(folders, fmt.Sprintf("%s%s%s", folder, SLASH, finfo.Name()))
			} else {
				files = append(files, fmt.Sprintf("%s%s%s", folder, SLASH, finfo.Name()))
			}
		}
	}

	for n, folder := range folders {
		folders[n] = strings.TrimPrefix(folder, local_path + "/")
	}
	for n, file := range files {
		files[n] = strings.TrimPrefix(file, local_path + "/")
	}

	return folders, files
}

func FullPath(path string) string {
	return filepath.Clean(Config.Get("configuration", "local_path") + SLASH + path)
}

// Get confirmation
func getConfirm(name string) bool {
	for {
		resp := get_input(fmt.Sprintf("%s? (y/n): ", name))
		resp = strings.ToLower(resp)
		fmt.Println(NONE)
		if resp == "y" || resp == "yes" {
			return true
		} else if resp == "n" || resp == "no" {
			return false
		}
		fmt.Printf("Err: Unrecognized response: %s\n", resp)
		continue
	}
}

func isEmail(input string) (matched bool) {
	matched, _ = regexp.MatchString("^[a-z0-9.]+@[a-z0-9.-]+\\.[a-z.]+$", input)
	return
}

// Removes newline characters
func cleanInput(input string) (output string) {
	var output_bytes []rune
	for _, v := range input {
		if v == '\n' || v == '\r' {
			continue
		}
		output_bytes = append(output_bytes, v)
	}
	return strings.TrimSpace(string(output_bytes))
}

// Gets user input, used during setup and configuration.
func get_passw(question string) string {

	for {
		fmt.Printf(question)
		resp, err := gopass.GetPasswd()
		if err != nil {
			if err == gopass.ErrInterrupted {
				os.Exit(1)
			}
			fmt.Printf("Err: %s\n", err.Error())
			continue
		}
		response := cleanInput(string(resp))
		if len(response) > 0 {
			return response
		}
	}
}

// Gets user input, used during setup and configuration.
func get_input(question string) string {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Printf(question)
		response, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("Err: %s\n", err.Error())
			continue
		}
		response = cleanInput(response)
		if len(response) > 0 {
			return response
		}
	}
}

// Encrypts data using the hash of key provided.
func encrypt(input []byte, key []byte) []byte {

	var block cipher.Block

	key = hashBytes(key)
	block, _ = aes.NewCipher(key)

	buff := make([]byte, len(input))
	copy(buff, input)

	cipher.NewCFBEncrypter(block, key[0:block.BlockSize()]).XORKeyStream(buff, buff)

	return []byte(base64.RawStdEncoding.EncodeToString(buff))
}

// Decrypts data.
func decrypt(input []byte, key []byte) (decoded []byte) {

	var block cipher.Block

	key = hashBytes(key)

	decoded, _ = base64.RawStdEncoding.DecodeString(string(input))
	block, _ = aes.NewCipher(key)
	cipher.NewCFBDecrypter(block, key[0:block.BlockSize()]).XORKeyStream(decoded, decoded)

	return
}

// Perform sha256.Sum256 against input byte string.
func hashBytes(input ...interface{}) []byte {
	var combine []string
	for _, v := range input {
		if x, ok := v.([]byte); ok {
			v = string(x)
		}
		combine = append(combine, fmt.Sprintf("%v", v))
	}
	sum := sha256.Sum256([]byte(strings.Join(combine[0:], NONE)))
	var output []byte
	output = append(output[0:], sum[0:]...)
	return output
}

// Removes empty slices
func cleanSlice(input []string) (output []string) {

	output = input

	var n int

	for i := 0; i < len(input); i++ {
		if strings.TrimSpace(input[i]) != NONE {
			output[n] = input[i]
			n++
		}
	}

	output = output[:n]
	return
}

func folderDate(input time.Time) (output string) {
	year := input.Year()
	mon := int(input.Month())
	day := input.Day()
	hou := input.Hour()
	min := input.Minute()
	sec := input.Second()

	str_time := func(in_time int) string {
		if in_time > 9 {
			return strconv.Itoa(in_time)
		} else {
			return fmt.Sprintf("0%d", in_time)
		}
	}

	return fmt.Sprintf("%d%s%s_%s%s%s", year, str_time(mon), str_time(day), str_time(hou), str_time(min), str_time(sec))
}

// Splits on the last seperator, for seperating paths and files.
func splitLast(input string, sep string) []string {
	split_str := strings.Split(input, sep)
	return append([]string{strings.Join(split_str[0:len(split_str)-1], sep)}, split_str[len(split_str)-1:]...)
}

// Generates a random byte slice of length specified.
func randBytes(sz int) []byte {
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

// Process string entry to bool value.
func getBoolVal(input string) bool {
	input = strings.ToLower(input)
	if input == "yes" || input == "true" {
		return true
	}
	return false
}

// Parse Timestamps from kiteworks
func read_kw_time(input string) (time.Time, error) {
	input = strings.Replace(input, "+0000", "Z", 1)
	return time.Parse(time.RFC3339, input)
}

func write_kw_time(input time.Time) string {
	t := input.UTC().Format(time.RFC3339)
	return strings.Replace(t, "Z", "+0000", 1)
}

// Move file from one directory to another.
func moveFile(src, dst string) (err error) {
	src = FullPath(src)
	dst = FullPath(dst)

	if err = MkDir(splitLast(dst, SLASH)[0]); err != nil { 
		return err 
	}

	s_file, err := os.Open(src)
	if err != nil {
		return err
	}

	d_file, err := os.Create(dst)
	if err != nil {
		s_file.Close()
		return err
	}

	_, err = io.Copy(d_file, s_file)
	if err != nil && err != io.EOF {
		s_file.Close()
		d_file.Close()
		return err
	}

	s_file.Close()
	d_file.Close()

	for i := 0; i < 10; i++ {
		if err := os.Remove(src); err != nil {
			time.Sleep(time.Second)
			continue
		} else {
			break
		}
	}
	return
}

// Fatal error handler.
func errChk(err error, desc ...string) {
	if err != nil {
		if len(desc) > 0 {
			logger.Fatal("[%s] %s", strings.Join(desc, "->"), err.Error())
		} else {
			logger.Fatal(err)
		}
	}
}

// md5Sum function for checking files against appliance.
func md5Sum(filename string) (sum []byte, err error) {

	filename = FullPath(filename)

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
			return nil, err
		}

		if r == 0 {
			break
		}

		tmp = tmp[0:r]
		n, err = checkSum.Write(tmp)
		if err != nil {
			return nil, err
		}
		o = o + int64(n)
	}

	if err != nil && err != io.EOF {
		return nil, err
	}

	md5sum := checkSum.Sum(nil)

	sum = make([]byte, hex.EncodedLen(len(md5sum)))
	hex.Encode(sum, md5sum)

	return sum, nil
}

func compressFolder(input_folder, dest_file string) (err error) {
	_, files := scanPath(input_folder)

	f, err := os.OpenFile(dest_file, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return err
	}

	w := zip.NewWriter(f)
	w.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.NoCompression)
	})

	for _, file := range files {
		logger.Log("Flattening %s ...", file)
		f, err := w.Create(file)
		if err != nil {
			return err
		}
		r, err := Open(file)
		if err != nil {
			return err
		}

		finfo, err := Stat(file)
		if err != nil {
			logger.Err(err)
			continue
		}
		tm := NewTMonitor("processing", finfo.Size())
		show_transfer := uint32(1)

		go func() {
			for atomic.LoadUint32(&show_transfer) == 1 {
				tm.ShowTransfer()
				time.Sleep(time.Second)
			}
		}()
		err = Transfer(r, f, tm)
		atomic.StoreUint32(&show_transfer, 0)
		if err != nil {
			logger.Err(err)
			continue
		}
	}
	w.Close()
	return
}

// Create a local folder
func MkDir(path string) (err error) {

	create := func(path string) (err error) {
		_, err = os.Stat(path)
		if err != nil && os.IsNotExist(err) {
			err = os.Mkdir(path, 0755)
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		return nil
	}

	path = filepath.Clean(path)

	split_path := strings.Split(path, SLASH)
	for i, _ := range split_path {
		err = create(strings.Join(split_path[0:i+1], SLASH))
		if err != nil { return err }
	}

	return
}

func MkPath(path string) (err error) {
	return MkDir(FullPath(path))
}
 
func Chtimes(name string, atime time.Time, mtime time.Time) error {
	return os.Chtimes(FullPath(name), atime, mtime)
}

func Remove(name string) error {
	return os.Remove(FullPath(name))
}

func Create(name string) (*os.File, error) {
	return os.Create(FullPath(name))
}

func Rename(oldpath, newpath string) error {
	return os.Rename(FullPath(oldpath), FullPath(newpath))
}

func OpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(FullPath(name), flag, perm)
}

func Open(name string) (*os.File, error) {
	return os.Open(FullPath(name))
}

func Stat(name string) (os.FileInfo, error) {
	return os.Stat(FullPath(name))
}