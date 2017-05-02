package logger

import (
	"bytes"
	"sync"
	"sync/atomic"
	"io"
	"io/ioutil"
	"os"
	"fmt"
	"strings"
	"path/filepath"
	"time"
)

type logFile struct {
	write_lock sync.Mutex
	sem	int32
	buffer bytes.Buffer
	file *os.File
}

const (
	to_BUFFER = iota 
	to_FILE
)

var open_files = make(map[string]*logFile)

// Write function that switches between file output and buffers to memory when files is being rotated.
func (f *logFile) Write(p []byte) (n int, err error) {
	if atomic.LoadInt32(&f.sem) == to_FILE {
		f.write_lock.Lock()
		defer f.write_lock.Unlock()
		return f.file.Write(p)
	} else {
		return f.buffer.Write(p)
	}
}

// Opens a new log file for writing, max_size is threshold for rotation, max_rotation is number of previous logs to hold on to.
func File(logger int, filename string, max_size int64, max_rotation int) (err error) {

	var log_file *logFile

	mutex.Lock()

	if dest, exists := open_files[filename]; exists {
		log_file = dest
	} else {
		log_file, err = open_file(filename, max_size, max_rotation)
		if err != nil { return err }
		open_files[filename] = log_file
	}
	
	for k, v := range out_map {
		if logger&k == k || logger == ALL {
			out_map[k] = [2]io.Writer{v[0], log_file}
		}
	}

	mutex.Unlock()
	resetLoggers()

	return nil
}

// Opens file for writing and starts rotating.
func open_file(filename string, max_size int64, max_rotation int) (logger *logFile, err error) {

	fpath, fname := filepath.Split(filename)

	_, err = os.Stat(fpath)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.Mkdir(fpath, 0766)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	logger = new(logFile)

	logger.file, err = os.OpenFile(filename, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil { 
		return nil, err
	}

	go func(logger *logFile) {
		for {
			// Get information on current file.
			finfo, err := logger.file.Stat()
			if err != nil {
				for k, v := range out_map {
					if v[1] == logger.file {
						err = File(k, filename, max_size, max_rotation)
						if err != nil { panic(err) }
						break
					}
				}
			}			

			// If file size exceeds the max_size specified, begin file rotation.
			if finfo.Size() > max_size {
				atomic.StoreInt32(&logger.sem, to_BUFFER)

				logger.write_lock.Lock()
				logger.file.Close()

				flist, err := ioutil.ReadDir(fpath)
				if err != nil { panic(err) }

				var files []os.FileInfo

				for _, v := range flist {
					if strings.Contains(v.Name(), fname) {
						files = append(files, v)
					}
				}

				// Remove files if larger than max_rotation allows.
				if len(files) > max_rotation && max_rotation > -1 {
					erase := files[max_rotation:]
					files = files[0:max_rotation]
					for _, f := range erase {
						os.Remove(fmt.Sprintf("%s%s", fpath, f.Name()))
					}
				}

				// Rename files
				for i := len(files); i > 0; i-- {
						err = os.Rename(fmt.Sprintf("%s%s", fpath, files[i - 1].Name()), fmt.Sprintf("%s%s.%d", fpath, fname, i))
						if err != nil { panic (err) }
				}


				// Open new file.
				logger.file, err = os.OpenFile(filename, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
				if err != nil { panic(err) }

				// Switch Write function back to writing to file.
				atomic.StoreInt32(&logger.sem, to_FILE)

				// Copy buffer to new file.
				_, err = io.Copy(logger.file, &logger.buffer)
				if err != nil { panic(err) }

				logger.buffer.Reset()

				// Unlock mutex to allow writing to file.
				logger.write_lock.Unlock()
			}
			time.Sleep(time.Minute)
	}

	}(logger)

	atomic.StoreInt32(&logger.sem, to_FILE)
	return logger, nil
}