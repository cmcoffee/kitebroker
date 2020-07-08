package kvlite

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"math/big"
)

const (
	xSlots   = 6
	keyLen   = 66
	slotSize = 128
)

type xLock struct {
	Msg [][]byte
}

// Generates a random integer of 0-max.
func randInt(max int) int {
	maxBig := *big.NewInt(int64(max))
	output, _ := rand.Int(rand.Reader, &maxBig)
	return int(output.Int64())
}

// Generates a random byte slice of length specified.
func randBytes(sz int) []byte {
	if sz <= 0 {
		sz = 16
	}

	ch := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+/="
	chlen := len(ch)

	rand_string := make([]byte, sz)
	rand.Read(rand_string)

	for i, v := range rand_string {
		rand_string[i] = ch[v%byte(chlen)]
	}
	return rand_string

}

// ErrBadPadlock is returned if kvlite.Open is used with incorrect padlock set on database.
var ErrBadPadlock = errors.New("Invalid padlock provided, unable to open database.")

func (X *xLock) encrypt(input []byte, key []byte) []byte {

	var (
		block cipher.Block
	)

	key = hashBytes(key)
	block, _ = aes.NewCipher(key)

	buff := make([]byte, len(input))
	copy(buff, input)

	cipher.NewCFBEncrypter(block, key[0:block.BlockSize()]).XORKeyStream(buff, buff)

	return []byte(base64.RawStdEncoding.EncodeToString(buff))
}

func (X *xLock) decrypt(input []byte, key []byte) (decoded []byte) {

	var block cipher.Block

	key = hashBytes(key)

	decoded, _ = base64.RawStdEncoding.DecodeString(string(input))
	block, _ = aes.NewCipher(key)
	cipher.NewCFBDecrypter(block, key[0:block.BlockSize()]).XORKeyStream(decoded, decoded)

	return
}

// Sets and randomizes keys in Store table for Store encryption key.
func (X *xLock) dblocker(key, padlock []byte) []byte {
	passphrase := hashBytes(randBytes(256))
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
	rLock := X.encrypt(passphrase, rSalt)
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

	X.Msg = nil

	for _, v := range lock {
		X.Msg = append(X.Msg, v)
	}

	randKey := hashBytes(randBytes(256))
	randKey = append(randKey, '!')

	encryptedKey1 := X.encrypt(randKey, passphrase[0:32])
	encryptedKey2 := X.encrypt(key, randKey[0:32])
	encryptedKey3 := X.encrypt(key, append(key[32:], randKey[0:32]...))

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
	X.Msg = append(X.Msg, encryptedKey3)
	X.Msg = append(X.Msg, encryptedKey2)
	X.Msg = append(X.Msg, encryptedKey1)

	if padlock != nil {
		X.Msg = append(X.Msg, randBytes(slotSize))
	}

	return key[0:32]
}

// Extracts encryption key from Store table.
func (X *xLock) dbunlocker(padlock []byte) (key []byte, err error) {
	if X.Msg == nil {
		key = X.dblocker(nil, padlock)
		return
	}

	count := len(X.Msg)

	if count == 0 {
		key = X.dblocker(nil, padlock)
		return
	}

	if count < xSlots+4 {
		padlock = nil
	}

	vPass := unscram(X.Msg[xSlots+2])
	vRKey := unscram(X.Msg[xSlots+1])
	vKey := unscram(X.Msg[xSlots])

	// tryKey, try new method first, fall back to old method.
	tryKey := func(rkey []byte) []byte {
		decrypted := X.decrypt(vRKey, rkey)
		key1 := X.decrypt(vKey, append(decrypted[32:], rkey...))
		if bytes.Compare(key1[0:32], decrypted[0:32]) == 0 {
			return key1
		} else {
			key1 = X.decrypt(vKey, decrypted[0:32])
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
				passphrase = X.decrypt(X.Msg[i][n:n+44], padlockedPass)
				if len(passphrase) != 33 {
					continue
				}
				deCrypted := X.decrypt(vPass, passphrase[0:32])
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
			slotEnd := len(X.Msg[a][b:])
			if slotEnd < keyLen {
				b = slotSize - keyLen
			}
			if _, k := tryPass(X.Msg[a][b:b+keyLen], a); k != nil {
				return k, nil
			}
		}
	}

	return nil, ErrBadPadlock

}

func unscram(src []byte) (out []byte) {
	var n int
	for i := 22; n < 44; i++ {
		i++
		n++
		out = append(out, src[i])
	}
	return
}
