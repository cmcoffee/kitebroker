package core

import (
	"github.com/cmcoffee/snugforge/kvlite"
)

// Database represents a key-value store with table management capabilities.
type Database interface {
	Sub(prefix string) Database
	Bucket(name string) Database
	Drop(table string)
	CryptSet(table, key string, value interface{})
	Set(table, key string, value interface{})
	Unset(table, key string)
	Get(table, key string, output interface{}) bool
	Keys(table string) []string
	CountKeys(table string) int
	Tables() []string
	Table(table string) Table
	Close()
}

var (

	// ResetDB is a function that resets the database.
	ResetDB = kvlite.CryptReset

	// ErrBadPadlock indicates an error when a padlock is invalid.
	ErrBadPadlock = kvlite.ErrBadPadlock
)

// OpenDB opens a database from the given filename.
// It accepts an optional padlock byte slice for encryption.
func OpenDB(filename string, padlock ...byte) (Database, error) {
	db, err := kvlite.Open(filename, padlock[0:]...)
	if err != nil {
		return nil, err
	}
	return &DBase{db}, nil
}

// DBase is a database wrapper around kvlite.Store.
type DBase struct {
	Store kvlite.Store
}

// Table represents a table within the database.
type Table struct {
	table kvlite.Table
}

// Drop deletes the underlying table.
func (t Table) Drop() {
	Critical(t.table.Drop())
}

// Get retrieves a value from the table by key. It returns true if the value
// was found, and false otherwise. Critical errors are handled internally.
func (t Table) Get(key string, value interface{}) bool {
	found, err := t.table.Get(key, value)
	Critical(err)
	return found
}

// Set sets the value for the given key in the table.
// Critical is called to handle any errors that occur during the set operation.
func (t Table) Set(key string, value interface{}) {
	Critical(t.table.Set(key, value))
}

// CryptSet encrypts and sets the given value for the given key.
func (t Table) CryptSet(key string, value interface{}) {
	Critical(t.table.CryptSet(key, value))
}

// Unset removes the key from the table.
func (t Table) Unset(key string) {
	Critical(t.table.Unset(key))
}

// Keys returns a slice of strings representing the keys in the table.
func (t Table) Keys() []string {
	keys, err := t.table.Keys()
	Critical(err)
	return keys
}

// CountKeys returns the number of keys in the table.
func (t Table) CountKeys() int {
	count, err := t.table.CountKeys()
	Critical(err)
	return count
}

// OpenCache Open a memory-only go-kvlite store.
func OpenCache() Database {
	return &DBase{kvlite.MemStore()}
}

// Bucket returns a new Database instance representing the given table.
func (d DBase) Bucket(table string) Database {
	return &DBase{d.Store.Bucket(table)}
}

// Sub returns a sub-database with the given prefix.
func (d DBase) Sub(table string) Database {
	return &DBase{d.Store.Sub(table)}
}

// Drop DB Wrappers to perform fatal error checks on each call.
func (d DBase) Drop(table string) {
	Critical(d.Store.Drop(table))
}

// CryptSet saves an encrypted value to the specified table and key.
func (d DBase) CryptSet(table, key string, value interface{}) {
	Critical(d.Store.CryptSet(table, key, value))
}

// Set saves a value to the specified table and key.
func (d DBase) Set(table, key string, value interface{}) {
	Critical(d.Store.Set(table, key, value))
}

// Get retrieves a value from the specified table by key.
// Returns true if the key exists, false otherwise.
// Errors are handled internally by the Critical function.
func (d DBase) Get(table, key string, output interface{}) bool {
	found, err := d.Store.Get(table, key, output)
	Critical(err)
	return found
}

// Table returns a table object for the given table name.
func (d DBase) Table(table string) Table {
	return Table{table: d.Store.Table(table)}
}

// Keys returns a list of keys for the specified table.
func (d DBase) Keys(table string) []string {
	keylist, err := d.Store.Keys(table)
	Critical(err)
	return keylist
}

// CountKeys returns the number of keys in the specified table.
func (d DBase) CountKeys(table string) int {
	count, err := d.Store.CountKeys(table)
	Critical(err)
	return count
}

// Tables returns a list of all table names in the database.
func (d DBase) Tables() []string {
	tables, err := d.Store.Tables()
	Critical(err)
	return tables
}

// Unset removes the value associated with the given key from the table.
func (d DBase) Unset(table, key string) {
	Critical(d.Store.Unset(table, key))
}

// Close closes the underlying store.
func (d DBase) Close() {
	d.Store.Close()
}
