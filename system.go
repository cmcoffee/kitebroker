package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"bufio"
	"github.com/cmcoffee/go-logger"
	"github.com/howeyc/gopass"
	"sync"
)

type Cache struct {
	f_lock *sync.RWMutex
	b_lock *sync.RWMutex
	f_map map[string]map[string]int
	b_map map[string]map[int]string
}

func NewCache() (*Cache) {
	return &Cache{
		new(sync.RWMutex),
		new(sync.RWMutex),
		make(map[string]map[string]int),
		make(map[string]map[int]string),
	}
}

// Add/Set a cache entry
func (c *Cache) Set(section string, key interface{}, value interface{}) error {
	c.f_lock.Lock()
	c.b_lock.Lock()
	defer c.f_lock.Unlock()
	defer c.b_lock.Unlock()

	section = strings.ToLower(section)

	if c.f_map[section] == nil {
		c.f_map[section] = make(map[string]int)
		c.b_map[section] = make(map[int]string)
	}

	switch k := key.(type) {
	case int:
		v, ok := value.(string)
		if !ok {
			return fmt.Errorf("key and value cannot both be a integer.")
		}
		c.b_map[section][k] = v
	case string:
		k = strings.ToLower(k)
		v, ok := value.(int)
		if !ok {
			return fmt.Errorf("key and value cannot both be a string.")
		}
		c.f_map[section][k] = v
	}
	return nil
}

// Returns strings based on integer index.
func (c *Cache) GetName(section string, key int) (string, bool) {
	c.b_lock.RLock()
	defer c.b_lock.RUnlock()
	section = strings.ToLower(section)
	nest, found := c.b_map[section]
	if !found {
		return NONE, false
	}
	v, found := nest[key]
	return v, found
}

// Returns a integer based on string index.
func (c *Cache) GetID(section string, key string) (int, bool) {
	c.f_lock.RLock()
	defer c.f_lock.RUnlock()
	section = strings.ToLower(section)
	nest, found := c.f_map[section]
	if !found {
		return 0, false
	}
	v, found := nest[strings.ToLower(key)]
	return v, found
}

// Removes cahce entry.
func (c *Cache) Unset(section string, key interface{}) {
	c.f_lock.Lock()
	c.b_lock.Lock()
	defer c.f_lock.Unlock()
	defer c.b_lock.Unlock()
	section = strings.ToLower(section)
	switch k := key.(type) {
	case int:
		delete(c.b_map[section], k)
	case string:
		k = strings.ToLower(k)
		delete(c.f_map[section], k)
	}
}

// Provides human readable file sizes.
func showSize(bytes int) string {

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

func getPercentage(t_size, c_size int) int {
	return int((float64(c_size) / float64(t_size)) * 100)
}

// Truncates Cache
func (c *Cache) Flush() {
	c.f_lock.Lock()
	c.b_lock.Lock()
	defer c.f_lock.Unlock()
	defer c.b_lock.Unlock()
	for section, _ := range c.b_map {
		for key, _ := range c.b_map[section] {
			delete(c.b_map[section], key)
		}
		delete(c.b_map, section)
	}
	for section, _ := range c.f_map {
		for key, _ := range c.f_map[section] {
			delete(c.f_map[section], key)
		}
		delete(c.f_map, section)
	}
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
func hashBytes(input []byte) []byte {
	sum := sha256.Sum256(input)
	var output []byte
	output = append(output[0:], sum[0:]...)
	return output
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
func timeParse(input string) (time.Time, error) {
	input = strings.Replace(input, "+0000", "Z", 1)
	return time.Parse(time.RFC3339, input)
}

// Create a local folder
func MkDir(path string) (err error) {
	finfo, err := os.Stat(path)
	if err != nil && os.IsNotExist(err) {
		err = os.Mkdir(path, 0755)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if finfo != nil && !finfo.IsDir() {
		os.Remove(path)
		err = os.Mkdir(path, 0755)
		if err != nil {
			return err
		}
	}
	return
}

// Move file from one directory to another.
func moveFile(src, dst string) (err error) {
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

	return os.Remove(src)
}

// Fatal error handler.
func errChk(err error, desc ...string) {
	if err != nil {
		if len(desc) > 0 {
			logger.Fatal("[%s] %s\n", strings.Join(desc, "->"), err.Error())
		} else {
			logger.Fatal(err)
		}
	}
}

// Provides a clean path, for Windows ... and everyone else.
func cleanPath(input string) string {
	return filepath.Clean(input)
}
