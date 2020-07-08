/*
	Package iotimeout provides a configurable timeout for io.Reader and io.ReadCloser.
*/

package iotimeout

import (
	"errors"
	. "github.com/cmcoffee/go-snuglib/xsync"
	"io"
	"sync"
	"time"
)

var ErrTimeout = errors.New("Timeout reached while waiting for bytes.")

const (
	waiting = 1 << iota
	halted
)

// Timer for io tranfer
func start_timer(timeout time.Duration, flag *BitFlag, input chan []byte, expired chan struct{}) {
	timeout_seconds := int64(timeout.Round(time.Second).Seconds())

	var cnt int64

	for {
		time.Sleep(time.Second)
		if flag.Has(halted) {
			input <- nil
			break
		}

		if flag.Has(waiting) {
			cnt++
			if timeout_seconds > 0 && cnt >= timeout_seconds {
				flag.Set(halted)
				expired <- struct{}{}
				input <- nil
				break
			}
		} else {
			cnt = 0
			flag.Set(waiting)
		}
	}
}

type resp struct {
	n   int
	err error
}

// Timeout Reader.
type readCloser struct {
	src     io.ReadCloser
	flag    BitFlag
	input   chan []byte
	output  chan resp
	expired chan struct{}
	mutex   sync.Mutex
}

type reader struct {
	io.Reader
}

func (r reader) Close() (err error) {
	return nil
}

// Timeout Reader: Adds a time to io.Reader
func NewReader(source io.Reader, timeout time.Duration) io.Reader {
	return NewReadCloser(reader{source}, timeout)
}

// Timeout ReadCloser: Adds a timer to io.ReadCloser
func NewReadCloser(source io.ReadCloser, timeout time.Duration) io.ReadCloser {
	t := new(readCloser)
	t.src = source
	t.input = make(chan []byte, 2)
	t.output = make(chan resp, 1)
	t.expired = make(chan struct{}, 1)

	go start_timer(timeout, &t.flag, t.input, t.expired)

	go func() {
		var (
			data resp
			p    []byte
		)
		for {
			p = <-t.input
			if p == nil {
				break
			}
			t.flag.Unset(waiting)
			data.n, data.err = source.Read(p)
			t.output <- data
		}
	}()
	return t
}

// Time Sensitive Read function.
func (t *readCloser) Read(p []byte) (n int, err error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if t.flag.Has(halted) {
		return t.src.Read(p)
	}

	// Set an idle timer.
	defer t.flag.Set(waiting)

	t.input <- p

	select {
	case data := <-t.output:
		n = data.n
		err = data.err
	case <-t.expired:
		t.flag.Set(halted)
		return -1, ErrTimeout
	}
	if err != nil {
		t.flag.Set(halted)
	}
	// Set an idle timer.
	return
}

// Close function for ReadCloser.
func (t *readCloser) Close() (err error) {
	t.flag.Set(halted)
	return t.src.Close()
}
