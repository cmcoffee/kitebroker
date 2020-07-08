package wrotate

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

type rotaFile struct {
	name         string
	flag         uint32
	file         *os.File
	buffer       bytes.Buffer
	r_error      error
	max_bytes    int64
	bytes_left   int64
	max_rotation uint
	write_lock   sync.Mutex
}

const (
	to_BUFFER = iota
	to_FILE
	_FAILED
	_CLOSED
)

// Write function that switches between file output and buffers to memory when files is being rotated.
func (f *rotaFile) Write(p []byte) (n int, err error) {
	f.write_lock.Lock()
	defer f.write_lock.Unlock()

	switch atomic.LoadUint32(&f.flag) {
	case to_FILE:
		if f.bytes_left < 0 {
			// Rotate files in background while writing to memory.
			atomic.StoreUint32(&f.flag, to_BUFFER)
			go f.rotator()
			return f.buffer.Write(p)
		}
		n, err = f.file.Write(p)
		f.bytes_left = f.bytes_left - int64(n)
		return
	case to_BUFFER:
		return f.buffer.Write(p)
	case _CLOSED:
		return f.file.Write(p)
	case _FAILED:
		return -1, f.r_error
	}
	return
}

// Creates a new log file (or opens an existing one) for writing.
// max_bytes is threshold for rotation, max_rotation is number of previous logs to hold on to.
func OpenFile(name string, max_bytes int64, max_rotations uint) (io.WriteCloser, error) {
	rotator := &rotaFile{
		name:         name,
		flag:         to_FILE,
		r_error:      nil,
		max_bytes:    max_bytes,
		max_rotation: max_rotations,
	}

	var err error

	rotator.file, err = os.OpenFile(name, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		return nil, err
	}

	// Just return the open file if max_bytes <= 0 or max_rotations <= 0.
	if max_bytes <= 0 || max_rotations <= 0 {
		return rotator.file, nil
	}

	finfo, err := rotator.file.Stat()
	if err != nil {
		return nil, err
	}

	rotator.bytes_left = rotator.max_bytes - finfo.Size()

	return rotator, nil
}

// Closes logging file, removes file from all loggers, removes file from open files.
func (R *rotaFile) Close() (err error) {
	atomic.StoreUint32(&R.flag, _CLOSED)
	return R.file.Close()
}

// Closes file, rotates and removes files greater than max rotations allow, opens new file, dumps buffer to disk and switches write function back to disk.
func (R *rotaFile) rotator() {
	fpath, fname := filepath.Split(R.name)
	if fpath == "" {
		fpath = fmt.Sprintf(".%s", string(os.PathSeparator))
	}

	// Check on error, returns true if error triggered, false if not.
	chkErr := func(err error) bool {
		if err != nil {
			R.r_error = err
			atomic.StoreUint32(&R.flag, _FAILED)
			return true
		}
		return false
	}

	err := R.file.Close()
	if chkErr(err) {
		return
	}

	flist, err := ioutil.ReadDir(fpath)
	if chkErr(err) {
		return
	}

	files := make(map[string]os.FileInfo)

	for _, v := range flist {
		if strings.Contains(v.Name(), fname) {
			files[v.Name()] = v
		}
	}

	file_count := uint(len(files))

	// Rename files
	for i := file_count; i > 0; i-- {
		target := fname

		if i > 1 {
			target = fmt.Sprintf("%s.%d", target, i-1)
		}

		if _, ok := files[target]; ok {
			if i > R.max_rotation {
				err = os.Remove(fmt.Sprintf("%s%s", fpath, target))
				if chkErr(err) {
					return
				}
			} else {
				err = os.Rename(fmt.Sprintf("%s%s", fpath, target), fmt.Sprintf("%s%s.%d", fpath, fname, i))
				if chkErr(err) {
					return
				}
			}
		}
	}

	// Open new file.
	R.file, err = os.OpenFile(R.name, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
	if chkErr(err) {
		return
	}

	R.write_lock.Lock()
	defer R.write_lock.Unlock()

	// Set l_files new size to new buffer.
	R.bytes_left = R.max_bytes - int64(R.buffer.Len())

	// Copy buffer to new file.
	_, err = io.Copy(R.file, &R.buffer)
	if chkErr(err) {
		return
	}

	R.buffer.Reset()

	// Switch Write function back to writing to file.
	atomic.StoreUint32(&R.flag, to_FILE)
	return
}
