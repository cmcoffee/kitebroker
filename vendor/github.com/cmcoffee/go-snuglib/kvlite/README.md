# kvlite
--
    import "github.com/cmcoffee/go-snuglib/kvlite"


## Usage

```go
var ErrBadPadlock = errors.New("Invalid padlock provided, unable to open database.")
```
ErrBadPadlock is returned if kvlite.Open is used with incorrect padlock set on
database.

#### func  CryptReset

```go
func CryptReset(filename string) (err error)
```
Resets encryption key on database, removing all encrypted keys in the process.

#### type Store

```go
type Store interface {
	// Tables provides a list of all tables.
	Tables() (tables []string, err error)
	// Table creats a SubStore of specified table.
	Table(table string) Table
	// Drop drops the specified table.
	Drop(table string) (err error)
	// CountKeys provides a total of keys in table.
	CountKeys(table string) (count int, err error)
	// Keys provides a listing of all keys in table.
	Keys(table string) (keys []string, err error)
	// CryptSet encrypts the value within the key/value pair in table.
	CryptSet(table, key string, value interface{}) (err error)
	// Set sets the key/value pair in table.
	Set(table, key string, value interface{}) (err error)
	// Unset deletes the key/value pair in table.
	Unset(table, key string) (err error)
	// Get retrieves value at key in table.
	Get(table, key string, output interface{}) (found bool, err error)
	// Close closes the kvliter.Store.
	Close() (err error)
}
```

Main Store Interface

#### func  MemStore

```go
func MemStore() Store
```
Creates a new ephemeral memory based kvliter.Store.

#### func  Open

```go
func Open(filename string, padlock ...byte) (Store, error)
```
Opens BoltDB backed kvlite.Store.

#### type Table

```go
type Table interface {
	Keys() (keys []string, err error)
	CountKeys() (count int, err error)
	Set(key string, value interface{}) (err error)
	CryptSet(key string, value interface{}) (err error)
	Get(key string, value interface{}) (found bool, err error)
	Unset(key string) (err error)
	Drop() (err error)
}
```

Table Interface follows the Main Store Interface, but directly to a table.
