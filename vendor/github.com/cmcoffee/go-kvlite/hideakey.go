package kvlite

import (
	"bytes"
	"errors"
	"sort"
	"strconv"
)

const (
	xSlots   = 6
	keyLen   = 66
	slotSize = 128
)

// ErrLocked is returned if a new Lock is attempted on a database that is currently locked.
var ErrNotUnlocked = errors.New("kvlite: Cannot apply new lock on top of existing lock, must remove old lock first.")

// ErrBadPass is returned if an Unlock is attempted with the incorrect passphrase.
var ErrBadPass = errors.New("kvlite: Invalid passphrase provided, unable to remove lock!")

// ErrBadPadlock is returned if kvlite.Open is used with incorrect padlock set on database.
var ErrBadPadlock = errors.New("kvlite: Invalid padlock provided, unable to open database.")

// Sets a lock on Store database, requires a passphrase (for unlocking in future) and padlock when opening database in future.
func Lock(filepath, passphrase string, padlock []byte) (err error) {
	Stor, err := open(filepath, nil, _reserved)
	if err != nil {
		return err
	}
	defer Stor.Close()
	count, err := Stor.CountKeys("KVLite", "X%%")
	if err != nil {
		return err
	}
	if count == xSlots+4 {
		err = ErrNotUnlocked
	} else {
		Stor.dblocker(hashBytes([]byte(passphrase)), Stor.key, padlock)
	}
	return
}

// Removes lock on Store database, strips the requirement for padlock for opening database, requires passphrase set on initial lock.
func Unlock(filepath, passphrase string) (err error) {
	Stor, err := open(filepath, nil, _reserved)
	if err != nil {
		return err
	}
	defer Stor.Close()
	count, err := Stor.CountKeys("KVLite", "X%%")
	if err != nil {
		return err
	}

	if count < xSlots+4 {
		return nil
	}

	var XMsg [3][]byte

	Stor.Get("KVLite", "X"+strconv.Itoa(xSlots), &XMsg[0])
	Stor.Get("KVLite", "X"+strconv.Itoa(xSlots+1), &XMsg[1])
	Stor.Get("KVLite", "X"+strconv.Itoa(xSlots+2), &XMsg[2])

	vKey := Stor.unscram(XMsg[0])
	vRKey := Stor.unscram(XMsg[1])
	vPass := Stor.unscram(XMsg[2])

	decrypted := decrypt(vPass, hashBytes([]byte(passphrase)))
	decrypted = decrypt(vRKey, decrypted[0:32])
	decrypted2 := decrypt(vKey, decrypted[0:32])

	if bytes.Compare(decrypted, decrypted2) == 0 {
		Stor.dblocker(nil, decrypted, nil)
	} else {
		return ErrBadPass
	}
	return
}

// Sets and randomizes keys in Store table for Store encryption key.
func (s *Store) dblocker(passphrase, key, padlock []byte) []byte {
	// Set passphrase and/or key to random if not specified.
	if passphrase == nil {
		passphrase = hashBytes(randBytes(256))
	}
	if key == nil {
		key = hashBytes(randBytes(256))
	}

	key = append(key, '!')
	passphrase = append(passphrase, '!')

	// Create a random key for unlocking passphrase.
	rKey := randBytes(keyLen)

	// Mix with provided padlock.
	var rSalt []byte
	rSalt = append(append(rSalt, rKey[0:]...), padlock[0:]...)

	// Pad uLock
	rLock := encrypt(passphrase, rSalt)
	lockLen := len(rLock)

	inject := randInt(slotSize)
	if inject > slotSize-lockLen {
		inject = slotSize - lockLen
	}

	paddedLkey := randBytes(slotSize - lockLen)
	paddedLkey = append(paddedLkey[:inject], append(rLock[0:], paddedLkey[inject:]...)...)

	inject = randInt(slotSize) / keyLen * keyLen
	if inject+keyLen > slotSize {
		inject = slotSize - keyLen
	}

	paddedRkey := randBytes(slotSize - len(rKey))
	paddedRkey = append(paddedRkey[:inject], append(rKey[0:], paddedRkey[inject:]...)...)

	lock := make([][]byte, xSlots)

	// Randomize slot order.
	randInject := func(input []byte) bool {
		dest := randInt(xSlots)
		if len(lock[dest]) == 0 {
			lock[dest] = input
			return true
		}
		for n := xSlots; n > 0; n-- {
			if len(lock[n-1]) == 0 {
				lock[n-1] = input
				return true
			}
		}
		return false
	}

	// First inject our randKey and then the encodedrypted db key.
	randInject(paddedLkey)
	randInject(paddedRkey)

	// Add random entries.
	for randInject(randBytes(slotSize)) {
	}

	for i, v := range lock {
		s.set("KVLite_Staging", "X"+strconv.Itoa(i), v, _reserved)
	}

	randKey := hashBytes(randBytes(256))
	randKey = append(randKey, '!')

	encryptedKey1 := encrypt(randKey, passphrase[0:32])
	encryptedKey2 := encrypt(key, randKey[0:32])
	encryptedKey3 := encrypt(key, append(key[32:], randKey[0:32]...))

	//filler := slotSize - len(encryptedKey1)

	scram := func(src []byte) (out []byte) {
		var n int
		out = randBytes(slotSize)
		for i := 22; n < len(src); i++ {
			i++
			out[i] = src[n]
			n++
		}
		return
	}

	encryptedKey1 = scram(encryptedKey1)
	encryptedKey2 = scram(encryptedKey2)
	encryptedKey3 = scram(encryptedKey3)

	// Store verifcation message which is the key encrypted with the key.
	s.set("KVLite_Staging", "X"+strconv.Itoa(xSlots), encryptedKey3, _reserved)
	s.set("KVLite_Staging", "X"+strconv.Itoa(xSlots+1), encryptedKey2, _reserved)
	s.set("KVLite_Staging", "X"+strconv.Itoa(xSlots+2), encryptedKey1, _reserved)

	if padlock != nil {
		s.set("KVLite_Staging", "X"+strconv.Itoa(xSlots+3), randBytes(slotSize), _reserved)
	}

	s.dbCon.Exec("DROP TABLE KVLite")
	if _, err := s.dbCon.Exec("ALTER TABLE KVLite_Staging RENAME TO KVLite"); err == nil {
		s.dbCon.Exec("DROP TABLE KVLite_Staging")
	}
	return key[0:32]
}

// Extracts encryption key from Store table.
func (s *Store) dbunlocker(padlock []byte) (err error) {

	// Run this to clean up a failed shutdown last time.
	c, err := s.CountKeys("KVLite")
	if err != nil {
		return err
	}
	if c == 0 {
		c, err := s.CountKeys("KVLite_Staging")
		if err != nil {
			return err
		}
		if c != 0 {
			s.dbCon.Exec("DROP TABLE KVLite")
			if _, err := s.dbCon.Exec("ALTER TABLE KVLite_Staging RENAME TO KVLite"); err == nil {
				s.dbCon.Exec("DROP TABLE KVLite_Staging")
			}
		}
	}

	slots, err := s.ListKeys("KVLite", "X%%")
	sort.Stable(sort.StringSlice(slots))
	if err != nil {
		return err
	}
	count := len(slots)

	if count == 0 {
		s.key = s.dblocker(nil, nil, padlock)
		return
	}

	if count < xSlots+4 {
		padlock = nil
	}

	XMsg := make([][]byte, count)

	for i := 0; i < count; i++ {
		s.Get("KVLite", "X"+strconv.Itoa(i), &XMsg[i])
	}

	s.mutex.Lock()
	vPass := s.unscram(XMsg[xSlots+2])
	vRKey := s.unscram(XMsg[xSlots+1])
	vKey := s.unscram(XMsg[xSlots])

	// tryKey, try new method first, fall back to old method.
	tryKey := func(rkey []byte) []byte {
		decrypted := decrypt(vRKey, rkey)
		key1 := decrypt(vKey, append(decrypted[32:], rkey...))
		if bytes.Compare(key1[0:32], decrypted[0:32]) == 0 {
			return key1
		} else {
			key1 = decrypt(vKey, decrypted[0:32])
			if bytes.Compare(key1[0:32], decrypted[0:32]) == 0 {
				return key1
			}
		}
		return nil
	}

	tryPass := func(testPass []byte, origin int) (passphrase, key []byte) {
		var padlockedPass []byte
		padlockedPass = append(append(padlockedPass, testPass[0:]...), padlock[0:]...)

		for i := 0; i < xSlots; i++ {
			if i == origin {
				continue
			}
			for n := 0; n <= slotSize-44; n++ {
				passphrase = decrypt(XMsg[i][n:n+44], padlockedPass)
				if len(passphrase) != 33 {
					continue
				}
				deCrypted := decrypt(vPass, passphrase[0:32])
				if key := tryKey(deCrypted[0:32]); key != nil {
					return passphrase[0:32], key[0:32]
				}
				continue
			}
		}
		return nil, nil
	}

	// Sequentially grab text from rows and try keyLen bytes at a time.
	for a := 0; a < xSlots; a++ {
		for b := 0; b < slotSize; b = b + keyLen {
			slotEnd := len(XMsg[a][b:])
			if slotEnd < keyLen {
				b = slotSize - keyLen
			}
			if passphrase, key := tryPass(XMsg[a][b:b+keyLen], a); key != nil {
				s.key = key
				s.mutex.Unlock()
				s.dblocker(passphrase, key, padlock)
				return
			}
		}
	}

	return ErrBadPadlock

}

func (s *Store) unscram(src []byte) (out []byte) {
	var n int
	for i := 22; n < 44; i++ {
		i++
		n++
		out = append(out, src[i])
	}
	return
}
