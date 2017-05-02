// Package 'kvlite' provides a Key Value interface upon SQLite.
package kvlite

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/mattn/go-sqlite3"
	"strings"
	"sync"
	"strconv"
)

type Store struct {
	key      []byte
	filePath string
	mutex    sync.RWMutex
	encoder  *json.Encoder
	buffer   *bytes.Buffer
	dbCon    *sql.DB
}

const (
	RESERVED = "KVLite"
	NONE     = ""
)

const (
	_none = (1 << iota)
	_encrypt
	_sort
	_revsort
	_reserved
)

// Checks to see if table name is reserved or invalid.
func chkTable(table *string, flags int) (err error) {
	for _, ch := range *table {
		switch ch {
		case 0x3b:
			fallthrough
		case 0x22:
			fallthrough
		case 0x27:
			fallthrough
		case 0x26:
			fallthrough
		case 0x28:
			return fmt.Errorf("Invalid characters in table name: '%s'", *table)
		}
	}

	if flags&_reserved > 0 {
		return
	}
	if strings.Contains(*table, RESERVED) {
		return fmt.Errorf("Sorry, %s is a reserved name.", *table)
	}
	return
}

// Stores value in Store datastore.
func (s *Store) Set(table string, key interface{}, val interface{}) (err error) {
	return s.set(table, key, val, 0)
}

// Writes encrypted value to Store datastore.
func (s *Store) CryptSet(table string, key interface{}, val interface{}) (err error) {
	return s.set(table, key, val, _encrypt)
}

// Internal function to write to SQLite.
func (s *Store) set(table string, key interface{}, val interface{}, flags int) (err error) {

	s.mutex.Lock()
	defer s.mutex.Unlock()

	var (
		eFlag    int
		encBytes []byte
	)

	// Encode the data.
	switch v := val.(type) {
	case []byte:
		encBytes = v
	default:
		s.buffer.Reset()
		err = s.encoder.Encode(val)
		if err != nil {
			return err
		}
		encBytes = s.buffer.Bytes()
	}

	err = chkTable(&table, flags)
	if err != nil {
		return err
	}

	if flags&_encrypt != 0 {
		encBytes = encrypt(encBytes, s.key)
		eFlag = 1
	}

	var new_table string

	switch key.(type) {
		case int:
			new_table = "key INT PRIMARY KEY, value BLOB, e INT"
		default:
			new_table = "key TEXT PRIMARY KEY, value BLOB, e INT"
	}

	key_str := fmt.Sprintf("%v", key)

	_, err = s.dbCon.Exec("CREATE TABLE IF NOT EXISTS '" + table + "' (" + new_table + ");")
	if err != nil {
		return err
	}

	s.dbCon.Exec("DELETE FROM '"+table+"' WHERE key COLLATE nocase = ?;", key_str)
	if eFlag == 0 {
		encBytes = []byte(base64.RawStdEncoding.EncodeToString(encBytes))
	}

	_, err = s.dbCon.Exec("INSERT OR REPLACE INTO '"+table+"'(key,value,e) VALUES(?, ?, ?);", key_str, encBytes, eFlag)
	if err != nil {
		return err
	}

	return
}

// Unset/remove key in table specified.
func (s *Store) Unset(table string, key interface{}) error {
	return s.unset(table, key, 0)
}

func (s *Store) unset(table string, key interface{}, flags int) (err error) {

	s.mutex.Lock()
	defer s.mutex.Unlock()

	err = chkTable(&table, flags)
	if err != nil {
		return err
	}

	key_str := fmt.Sprintf("%v", key)

	if _, err := s.dbCon.Exec("DELETE FROM '"+table+"' WHERE key COLLATE nocase = ?;", key_str); err != nil {
		if strings.Contains(err.Error(), "no such table") == true {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return
}

// Truncates the KVLite table to reset the encryption keys for database.
func (s *Store) CryptReset() error {
	// Truncate KVLite table.
	err := s.Truncate(RESERVED)
	if err != nil {
		return err
	}
	tables, err := s.ListTables()
	if err != nil {
		return err
	}

	// Erase any encrypted entries.
	for _, table := range tables {
		if _, err := s.dbCon.Exec("DELETE FROM '"+table+"' WHERE e = ?;", 1); err != nil {
			return err
		}
	}
	return nil
}

// Truncates a table in Store datastore.
func (s *Store) Truncate(table string) (err error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, err = s.dbCon.Exec("DROP TABLE '" + table + "';"); err != nil {
		if strings.Contains(err.Error(), "no such table") == true {
			return nil
		}
	}
	return
}

// Retrieves a value as string at key in table specified.
func (s *Store) SGet(table string, key interface{}) (output string) {
	s.Get(table, key, &output)
	return output
}

// Retreive a value at key in table specified.
func (s *Store) Get(table string, key interface{}, output interface{}) (found bool, err error) {

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var eFlag int
	var data []byte

	err = chkTable(&table, _reserved)
	if err != nil {
		return false, err
	}

	key_str := fmt.Sprintf("%v", key)

	err = s.dbCon.QueryRow("SELECT value FROM '"+table+"' WHERE key COLLATE nocase = ?", key_str).Scan(&data)

	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		if strings.Contains(err.Error(), "no such table") == true {
			return false, nil
		} else {
			return false, err
		}
	default:
		err = s.dbCon.QueryRow("SELECT e FROM '"+table+"' WHERE key COLLATE nocase = ?;", key_str).Scan(&eFlag)
		if err != nil {
			return false, err
		}

		if eFlag != 0 {
			data = decrypt(data, s.key)
		} else {
			data, _ = base64.RawStdEncoding.DecodeString(string(data))
		}
	}

	switch o := output.(type) {
	case *[]byte:
		*o = append(*o, data[0:]...)
	default:
		if output == nil {
			return true, nil
		}
		var dec *json.Decoder
		dec = json.NewDecoder(bytes.NewReader(data))
		if dec != nil {
			return true, dec.Decode(output)
		}
	}

	return true, nil
}

// Uses VACUUM command to shrink sqlite database.
func (s *Store) Shrink() (err error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	_, err = s.dbCon.Exec("VACUUM;")
	return err
}

// List all tables, if filter specified only tables that match filter.
func (s *Store) ListTables(filters ...string) (cList []string, err error) {

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if len(filters) == 0 {
		filters = append(filters, NONE)
	}

	for _, filter := range filters {

		var rows *sql.Rows

		if filter == NONE {
			rows, err = s.dbCon.Query("SELECT name FROM sqlite_master WHERE type='table';")
			if err != nil {
				return nil, err
			}
		} else {
			rows, err = s.dbCon.Query("SELECT name FROM sqlite_master WHERE type='table' and name like ?;", filter)
			if err != nil {
				return nil, err
			}
		}

		defer rows.Close()

		for rows.Next() {
			var table string
			err = rows.Scan(&table)
			if err != nil {
				rows.Close()
				return nil, err
			}

			if !strings.Contains(table, RESERVED) {
				cList = append(cList, table)
			}
		}

		err = rows.Err()
		if err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	return
}

// List all keys in table, only those matching filter if specified.
func (s *Store) CountKeys(table string, filters ...string) (count uint32, err error) {

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if len(filters) == 0 {
		filters = append(filters, NONE)
	}

	for _, filter := range filters {
		var rows *sql.Rows

		err = chkTable(&table, _reserved)
		if err != nil {
			return 0, err
		}

		if filter != NONE {
			rows, err = s.dbCon.Query("SELECT COUNT(key) FROM '"+table+"' where key like ?;", filter)
		} else {
			rows, err = s.dbCon.Query("SELECT COUNT(key) FROM '" + table + "';")
		}

		// Prevent table does not exist errors.
		if err != nil {
			if strings.Contains(err.Error(), "no such table") == true {
				return 0, nil
			} else {
				return 0, err
			}
		}

		for rows.Next() {
			err = rows.Scan(&count)
			if err != nil {
				rows.Close()
				return 0, err
			}
		}
		err = rows.Err()
		if err != nil {
			rows.Close()
			return 0, err
		}
		rows.Close()
	}
	return
}

// List all numeric keys in table, matching filter if specified.
func (s *Store) ListNKeys(table string, filters ...string) (keyList []int, err error) {
	var keys []string
	keys, err = s.ListKeys(table, filters...)
	for _, k := range keys {
		i, err := strconv.Atoi(k)
		if err != nil { return nil, err }
		keyList = append(keyList, i)
	}
	return
}

// List all keys in table, only those matching filter if specified.
func (s *Store) ListKeys(table string, filters ...string) (keyList []string, err error) {

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if len(filters) == 0 {
		filters = append(filters, NONE)
	}

	for _, filter := range filters {
		var rows *sql.Rows

		err = chkTable(&table, _reserved)
		if err != nil {
			return nil, err
		}

		if filter != NONE {
			rows, err = s.dbCon.Query("SELECT key FROM '"+table+"' where key like ?;", filter)
		} else {
			rows, err = s.dbCon.Query("SELECT key FROM '" + table + "';")
		}

		// Prevent table does not exist errors.
		if err != nil {
			if strings.Contains(err.Error(), "no such table") == true {
				return nil, nil
			} else {
				return nil, err
			}
		}

		for rows.Next() {
			var key string
			err = rows.Scan(&key)
			if err != nil {
				rows.Close()
				return nil, err
			}
			keyList = append(keyList, key)
		}
		err = rows.Err()
		if err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return
}

// Close Store.
func (s *Store) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.dbCon.Close()
}

// Manually override encryption key used with CryptSet.
func (s *Store) CryptKey(key []byte) {
	s.key = key
}

var _Store_DRIVER string

func init() {
	sql.Register(_Store_DRIVER, &sqlite3.SQLiteDriver{})
}

// Open or Creates a new *Store will use auto-created encryption key.
func Open(filePath string, padlock ...[]byte) (*Store, error) {
	if filePath == NONE {
		return nil, fmt.Errorf("kvlite: Missing filename parameter.")
	}
	if len(padlock) == 0 {
		return open(filePath, nil, 0)
	} else {
		for i, pad := range padlock {
			if i == 0 {
				continue
			}
			padlock[0] = append(padlock[0], pad[0:]...)
			padlock[i] = nil
		}
		return open(filePath, padlock[0], 0)
	}
}

// Open Memory-Only Database with random key.
func MemStore() (*Store, error) {
	return FastOpen(":memory:", NONE)
}

// Open Database without auto-generated encryption key, instead specify key, if no key specific will be random.
func FastOpen(filePath string, key string) (*Store, error) {

	if key == NONE {
		key = string(randBytes(32))
	}

	db, err := open(filePath, nil, _reserved)
	if err != nil {
		return nil, err
	}
	db.key = []byte(key)
	return db, nil
}

func open(filePath string, padlock []byte, flags int) (openStore *Store, err error) {

	dbCon, err := sql.Open(_Store_DRIVER, filePath)
	if err != nil {
		return nil, err
	}

	var buff bytes.Buffer

	openStore = &Store{
		dbCon:    dbCon,
		filePath: filePath,
		buffer:   &buff,
		encoder:  json.NewEncoder(&buff),
	}

	if err = dbCon.Ping(); err != nil {
		dbCon.Close()
		fmt.Errorf("%s: %s", filePath, err.Error())
		return nil, err
	}

	setPragma := func(input ...string) (err error) {
		for _, val := range input {
			_, err = dbCon.Exec(fmt.Sprintf("PRAGMA %s;", val))
			if err != nil {
				return err
			}
		}
		return
	}

	if err := setPragma("case_sensitive_like=OFF",
		"encoding='UTF-8'",
		"synchronous=NORMAL",
		"journal_mode=DELETE",
	); err != nil {
		return nil, err
	}

	if flags&_reserved == 0 {
		err = openStore.dbunlocker(padlock)
		if err != nil {
			return nil, err
		}
	}

	return
}
