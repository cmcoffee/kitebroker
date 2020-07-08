package kvlite

import (
	"sync"
)

// Memory-Map keystore
type memStore struct {
	mutex   sync.RWMutex
	kv      map[string]map[string][]byte
	encoder encoder
}

// Returns sub of table.
func (K *memStore) Table(table string) Table {
	return focused{table: table, store: K}
}

func (K *memStore) Keys(table string) (keys []string, err error) {
	K.mutex.RLock()
	defer K.mutex.RUnlock()
	if t, ok := K.kv[table]; ok {
		for k, _ := range t {
			keys = append(keys, k)
		}
	}
	if keys == nil {
		keys = append(keys, "")
	}
	return keys, nil
}

func (K *memStore) Tables() (tables []string, err error) {
	K.mutex.RLock()
	defer K.mutex.RUnlock()
	for k, _ := range K.kv {
		tables = append(tables, k)
	}
	if tables == nil {
		tables = append(tables, "")
	}
	return tables, err
}

func (K *memStore) Drop(table string) (err error) {
	K.mutex.Lock()
	defer K.mutex.Unlock()

	delete(K.kv, table)
	return nil
}

func (K *memStore) Unset(table, key string) (err error) {
	K.mutex.Lock()
	defer K.mutex.Unlock()
	if t, ok := K.kv[table]; ok {
		delete(t, key)
	}
	return nil
}

func (K *memStore) Get(table, key string, output interface{}) (found bool, err error) {
	K.mutex.RLock()
	defer K.mutex.RUnlock()
	if t, ok := K.kv[table]; ok {
		if v, ok := t[key]; ok {
			return true, K.encoder.decode(v, output)
		}
	}
	return false, nil
}

// Returns list of keys in table in memory store.
func (K *memStore) CountKeys(table string) (count int, err error) {
	K.mutex.RLock()
	defer K.mutex.RUnlock()
	if t, ok := K.kv[table]; ok {
		count = len(t)
	}
	return count, nil
}

// Set key/value in memory store.
func (K *memStore) Set(table, key string, value interface{}) (err error) {
	return K.set(table, key, value, false)
}

// Encrypt key/value in memory store.
func (K *memStore) CryptSet(table, key string, value interface{}) (err error) {
	return K.set(table, key, value, true)
}

func (K *memStore) set(table, key string, value interface{}, encrypt_value bool) (err error) {
	K.mutex.Lock()
	defer K.mutex.Unlock()

	if _, ok := K.kv[table]; !ok {
		K.kv[table] = make(map[string][]byte)
	}

	v, err := K.encoder.encode(value)
	if err != nil {
		return err
	}

	if encrypt_value {
		v = K.encoder.encrypt(v)
		v = append([]byte{1}, v[0:]...)
	} else {
		v = append([]byte{0}, v[0:]...)
	}

	K.kv[table][key] = v

	return nil

}

// Closed MemStore
func (K *memStore) Close() (err error) {
	K.mutex.Lock()
	defer K.mutex.Unlock()
	for k, _ := range K.kv {
		delete(K.kv, k)
	}
	return nil
}

// Creates a new ephemeral memory based kvliter.Store.
func MemStore() Store {
	return &memStore{kv: make(map[string]map[string][]byte), encoder: hashBytes(randBytes(256))}
}
