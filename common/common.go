
package common

import (
	"github.com/cmcoffee/go-kwlib"
	"github.com/cmcoffee/go-snuglib/eflag"
	"fmt"
	"strings"
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


type Session struct {
	*kwlib.KWSession
}
type Passport struct {
	User Session
	DB   TStore
}

// Task Interface
type Task interface {
	Init(*FlagSet) error
	Main(Passport) error
	New() Task
}


// TStore is a table within the datastore.
type TStore struct {
	prefix string
	db *kwlib.Database
}

// Handoff DB for tasks.
func NewTStore(prefix string, db *kwlib.Database) TStore {
	return TStore{
		prefix: fmt.Sprintf("%s:", prefix),
		db: db,
	}
}

// applies prefix of table to calls.
func (d TStore) apply_prefix(table string) string {
	return fmt.Sprintf("%s%s", d.prefix, table)
}

// Applies additional prefix to table.
func (d TStore) Sub(prefix string) TStore {
	return TStore{
		prefix: fmt.Sprintf("%s%s:", d.prefix, prefix),
		db: d.db,
	}
}

// DB Wrappers to perform fatal error checks on each call.
func (d TStore) Drop(table string) {
	d.db.Drop(d.apply_prefix(table))
}

// Encrypt value to go-kvlie, fatal on error.
func (d TStore) CryptSet(table, key string, value interface{}) {
	d.db.CryptSet(d.apply_prefix(table), key, value)
}

// Save value to go-kvlite.
func (d TStore) Set(table, key string, value interface{}) {
	d.db.Set(d.apply_prefix(table), key, value)
}

// Retrieve value from go-kvlite.
func (d TStore) Get(table, key string, output interface{}) bool {
	found := d.db.Get(d.apply_prefix(table), key, output)
	return found
}

// List keys in go-kvlite.
func (d TStore) Keys(table string) []string {
	keylist := d.db.Keys(d.apply_prefix(table))
	return keylist
}

// Count keys in table.
func (d TStore) CountKeys(table string) int {
	count := d.db.CountKeys(d.apply_prefix(table))
	return count
}

// List Tables in DB
func (d TStore) Tables() []string {
	var tables []string

	for _, t := range d.db.Tables() {
		if strings.HasPrefix(t, d.prefix) {
			tables = append(tables, strings.TrimPrefix(t, d.prefix))
		}
	}
	return tables
}

// Delete value from go-kvlite.
func (d TStore) Unset(table, key string) {
	d.db.Unset(d.apply_prefix(table), key)
}

// Drill in to specific table.
func (d TStore) Table(table string) kwlib.Table {
	return d.db.Table(d.apply_prefix(table))
}


