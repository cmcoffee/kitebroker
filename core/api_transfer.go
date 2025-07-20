package core

import (
	"fmt"
	"github.com/cmcoffee/snugforge/iotimeout"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (

	// wd_started indicates that the web downloader has started.
	wd_started = 1 << iota

	// wd_closed indicates the web downloader has been closed.
	wd_closed

	// wd_no_api_errors indicates that API errors should be ignored.
	wd_no_api_errors
)

// web_downloader downloads content from HTTP requests.
// It implements the ReadSeekCloser interface.
type web_downloader struct {
	flag            BitFlag
	err             error
	api             *APIClient
	req             *http.Request
	reqs            []*http.Request
	resp            *http.Response
	offset          int64
	last_byte       []int64
	request_timeout time.Duration
}

// Read reads data from the web downloader.
// It handles multiple requests and updates the offset accordingly.
func (W *web_downloader) Read(p []byte) (n int, err error) {
	if !W.flag.Has(wd_started) {
		W.req = W.reqs[0]
		if W.req == nil {
			return 0, fmt.Errorf("Webdownloader not initialized.")
		}
		W.reqs = append(W.reqs[:0], W.reqs[1:]...)
		W.flag.Set(wd_started)
		W.resp, err = W.api.SendRequest(NONE, W.req)
		if err != nil {
			if IsAPIError(err) && !W.flag.Has(wd_no_api_errors) {
				err = PrefixAPIError("Download Error", err)
				return 0, err
			} else {
				return 0, fmt.Errorf("Download Error: %s", err)
			}
		}

		if W.offset > 0 {
			content_range := strings.Split(strings.TrimPrefix(W.resp.Header.Get("Content-Range"), "bytes"), "-")
			if len(content_range) > 1 {
				if strings.TrimSpace(content_range[0]) != strconv.FormatInt(W.offset, 10) {
					return 0, fmt.Errorf("Requested byte %v, got %v instead.", W.offset, content_range[0])
				}
			}
		}
		W.resp.Body = iotimeout.NewReadCloser(W.resp.Body, W.request_timeout)
	}

	n, err = W.resp.Body.Read(p)

	// If we have multiple requests, start next request.
	if err == io.EOF {
		if len(W.reqs) > 0 {
			W.offset = 0
			err = nil
			W.flag.Unset(wd_started)
			W.resp.Body.Close()
		}
	}

	return
}

// Close closes the web downloader and releases resources.
// It also limits the number of concurrent requests.
func (W *web_downloader) Close() error {
	if !W.flag.Has(wd_closed) {
		W.flag.Set(wd_closed)
		if W.api.trans_limiter != nil {
			<-W.api.trans_limiter
		}
		if W.resp == nil || W.resp.Body == nil {
			return nil
		}
		return W.resp.Body.Close()
	}
	return nil
}

// Seek sets the offset of the downloader.
// It also updates the Range header of the request.
func (W *web_downloader) Seek(offset int64, whence int) (int64, error) {
	if offset == -500 && whence == -500 {
		W.flag.Set(wd_no_api_errors)
		return 0, nil
	}
	if offset < 0 {
		return 0, fmt.Errorf("Can't read before the start of the file.")
	}
	if offset == 0 {
		return 0, nil
	}
	if len(W.reqs) == 1 {
		W.offset = offset
		W.reqs[0].Header.Set("Range", fmt.Sprintf("bytes=%d-", W.offset))
	} else {
		var real_offset int64
		for i, v := range W.last_byte {
			if offset > v+real_offset {
				real_offset += v
				continue
			} else {
				W.reqs = append(W.reqs[:0], W.reqs[i:]...)
				W.reqs[0].Header.Set("Range", fmt.Sprintf("bytes=%d-", offset-real_offset))
				break
			}
		}
	}
	return offset, nil
}

func (s *APIClient) WebDownload(reqs ...*http.Request) ReadSeekCloser {
	if s.trans_limiter != nil {
		s.trans_limiter <- struct{}{}
	}

	var last_byte []int64

	for _, v := range reqs {
		v.Header.Set("Content-Type", "application/octet-stream")
		if s.AgentString != NONE {
			v.Header.Set("User-Agent", s.AgentString)
		}
		var current_sz int64
		if l := v.Header.Get("Size"); l != NONE {
			if sz, _ := strconv.ParseInt(l, 0, 64); sz > 0 {
				current_sz += sz
				last_byte = append(last_byte, current_sz)
			}
			v.Header.Del("Size")
		}
	}

	return &web_downloader{
		api:             s,
		reqs:            reqs[0:],
		last_byte:       last_byte,
		request_timeout: s.RequestTimeout,
	}
}
