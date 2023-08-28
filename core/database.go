package core

import (
	"github.com/cmcoffee/go-snuglib/kvlite"
)

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

// Wrapper around go-kvlite.
type DBase struct {
	Store kvlite.Store
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

// Open a memory-only go-kvlite store.
func OpenCache() Database {
	return &DBase{kvlite.MemStore()}
}

func (d DBase) Bucket(table string) Database {
	return &DBase{d.Store.Bucket(table)}
}

func (d DBase) Sub(table string) Database {
	return &DBase{d.Store.Sub(table)}
}

// DB Wrappers to perform fatal error checks on each call.
func (d DBase) Drop(table string) {
	Critical(d.Store.Drop(table))
}

// Encrypt value to go-kvlie, fatal on error.
func (d DBase) CryptSet(table, key string, value interface{}) {
	Critical(d.Store.CryptSet(table, key, value))
}

// Save value to go-kvlite.
func (d DBase) Set(table, key string, value interface{}) {
	Critical(d.Store.Set(table, key, value))
}

// Retrieve value from go-kvlite.
func (d DBase) Get(table, key string, output interface{}) bool {
	found, err := d.Store.Get(table, key, output)
	Critical(err)
	return found
}

func (d DBase) Table(table string) Table {
	return Table{table: d.Store.Table(table)}
}

// List keys in go-kvlite.
func (d DBase) Keys(table string) []string {
	keylist, err := d.Store.Keys(table)
	Critical(err)
	return keylist
}

// Count keys in table.
func (d DBase) CountKeys(table string) int {
	count, err := d.Store.CountKeys(table)
	Critical(err)
	return count
}

// List Tables in DB
func (d DBase) Tables() []string {
	tables, err := d.Store.Tables()
	Critical(err)
	return tables
}

// Delete value from go-kvlite.
func (d DBase) Unset(table, key string) {
	Critical(d.Store.Unset(table, key))
}

// Closes go-kvlite database.
func (d DBase) Close() {
	d.Store.Close()
}
