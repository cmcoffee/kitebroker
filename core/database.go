package core

import (
	"fmt"
	"github.com/cmcoffee/go-snuglib/kvlite"
	"strings"
)

// SubStore is a TaskStore, a database with a prefix in the table.
type SubStore struct {
	prefix string
	db     DBase
}

type Database interface {
	Sub(prefix string) SubStore
	Shared(table string) SubStore
	Drop(table string)
	CryptSet(table, key string, value interface{})
	Set(table, key string, value interface{})
	Unset(table, key string)
	Get(table, key string, output interface{}) bool
	Keys(table string) []string
	CountKeys(table string) int
	Tables() []string
	Table(table string) Table
}

// Spins off a new shared table.
func (d *DBase) Shared(table string) SubStore {
	return SubStore{
		prefix: fmt.Sprintf("-shared.%s.", table),
		db:     *d,
	}
}
func (d *DBase) Sub(prefix string) SubStore {
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
func OpenCache() *DBase {
	return &DBase{kvlite.MemStore()}
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
	Critical(d.Store.Close())
}
