package core

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/cmcoffee/go-snuglib/iotimeout"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Max/Min chunk size for kiteworks
const (
	kw_chunk_size_max = 68157440
	kw_chunk_size_min = 1048576
)

var ErrNoUploadID = errors.New("Upload ID not found.")
var ErrUploadNoResp = errors.New("Unexpected empty resposne from server.")

// Returns chunk_size, total number of chunks and last chunk size.
func (K *APIClient) Chunks(total_size int64) (total_chunks int64) {
	chunk_size := K.MaxChunkSize

	if chunk_size == 0 || chunk_size > kw_chunk_size_max {
		chunk_size = kw_chunk_size_max
	}

	if chunk_size <= kw_chunk_size_min {
		chunk_size = kw_chunk_size_min
	}

	if total_size <= chunk_size {
		return 1
	}

	return (total_size / chunk_size) + 1
}

const (
	wd_started = 1 << iota
	wd_closed
)

// Webdownloader for external sources
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

func (W *web_downloader) Read(p []byte) (n int, err error) {
	if !W.flag.Has(wd_started) {
		W.req = W.reqs[0]
		if W.req == nil {
			return 0, fmt.Errorf("Webdownloader not initialized.")
		}
		W.reqs = append(W.reqs[:0], W.reqs[1:]...)
		W.flag.Set(wd_started)
		W.resp, err = W.api.Do(W.req)
		if err != nil {
			return 0, err
		}

		if W.resp.StatusCode < 200 || W.resp.StatusCode >= 300 {
			return 0, fmt.Errorf("GET %s: %s", W.req.URL, W.resp.Status)
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

func (W *web_downloader) Seek(offset int64, whence int) (int64, error) {
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

// Perform External Download from a remote request.
func (S *APIClient) Download(req *http.Request, reqs ...*http.Request) ReadSeekCloser {
	if S.trans_limiter != nil {
		S.trans_limiter <- struct{}{}
	}

	var last_byte []int64

	reqs = append([]*http.Request{req}[0:], reqs[0:]...)

	for _, v := range reqs {
		v.Header.Set("Content-Type", "application/octet-stream")
		if S.AgentString != NONE {
			v.Header.Set("User-Agent", S.AgentString)
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
		api:             S,
		reqs:            reqs[0:],
		last_byte:       last_byte,
		request_timeout: S.RequestTimeout,
	}
}

// Multipart filestreamer
type streamReadCloser struct {
	chunkSize int64
	size      int64
	r_buff    []byte
	w_buff    *bytes.Buffer
	source    io.ReadCloser
	eof       bool
	f_writer  io.Writer
	mwrite    *multipart.Writer
}

func (s *streamReadCloser) Close() (err error) {
	return s.mwrite.Close()
}

// Read function fro streamReadCloser, reads triggers a read from source->writes to bytes buffer via multipart writer->reads from bytes buffer.
func (s *streamReadCloser) Read(p []byte) (n int, err error) {
	buf_len := s.w_buff.Len()

	if buf_len > 0 {
		n, err = s.w_buff.Read(p)
		return
	}

	// Clear our output buffer.
	s.w_buff.Truncate(0)

	if s.eof {
		s.mwrite.Close()
		return 0, io.EOF
	}

	if !s.eof && s.chunkSize-s.size <= 4096 {
		s.r_buff = s.r_buff[0 : s.chunkSize-s.size]
		s.eof = true
	}

	n, err = s.source.Read(s.r_buff)
	if err != nil && err == io.EOF {
		s.eof = true
	} else if err != nil {
		return -1, err
	}

	s.size = s.size + int64(n)
	if n > 0 {
		n, err = s.f_writer.Write(s.r_buff[0:n])
		if err != nil {
			return -1, err
		}
		if s.eof {
			s.mwrite.Close()
		}
		for i := 0; i < len(s.r_buff); i++ {
			s.r_buff[i] = 0
		}
	}
	n, err = s.w_buff.Read(p)
	return
}

// Uploads file from specific local path, uploads in chunks, allows resume.
func (s KWSession) Upload(filename string, upload_id int, source_reader ReadSeekCloser) (*KiteObject, error) {
	if s.trans_limiter != nil {
		s.trans_limiter <- struct{}{}
		defer func() { <-s.trans_limiter }()
	}

	var upload_data struct {
		ID             int    `json:"id"`
		TotalSize      int64  `json:"totalSize"`
		TotalChunks    int64  `json:"totalChunks"`
		UploadedSize   int64  `json:"uploadedSize"`
		UploadedChunks int64  `json:"uploadedChunks"`
		Finished       bool   `json:"finished"`
		URI            string `json:"uri"`
	}

	err := s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/uploads/%d", upload_id),
		Params: SetParams(Query{"with": "(id,totalSize,totalChunks,uploadedChunks,finished,uploadedSize)"}),
		Output: &upload_data,
	})
	if err != nil {
		return nil, err
	}

	if upload_data.ID != upload_data.ID {
		return nil, ErrNoUploadID
	}

	total_bytes := upload_data.TotalSize

	ChunkSize := upload_data.TotalSize / upload_data.TotalChunks
	if upload_data.TotalChunks > 1 {
		ChunkSize++
	}
	ChunkIndex := upload_data.UploadedChunks

	src := transferMonitor(filename, total_bytes, leftToRight, source_reader)
	defer src.Close()

	if ChunkIndex > 0 {
		if upload_data.UploadedSize > 0 && upload_data.UploadedChunks > 0 {
			if _, err := src.Seek(ChunkSize*ChunkIndex, 0); err != nil {
				return nil, err
			}
		}
	}

	transfered_bytes := upload_data.UploadedSize

	w_buff := new(bytes.Buffer)

	var resp_data *KiteObject

	for transfered_bytes < total_bytes || total_bytes == 0 {
		w_buff.Reset()

		req, err := s.NewRequest(s.Username, "POST", fmt.Sprintf("/%s", upload_data.URI))
		if err != nil {
			return nil, err
		}

		req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", 7))

		if s.Snoop {
			Debug("[kiteworks]: %s", s.Username)
			Debug("--> METHOD: \"POST\" PATH: \"%v\" (CHUNK %d OF %d)\n", req.URL.Path, ChunkIndex+1, upload_data.TotalChunks)
		}

		w := multipart.NewWriter(w_buff)

		req.Header.Set("Content-Type", "multipart/form-data; boundary="+w.Boundary())

		if ChunkIndex == upload_data.TotalChunks-1 {
			q := req.URL.Query()
			q.Set("returnEntity", "true")
			q.Set("mode", "full")
			if s.Snoop {
				for k, v := range q {
					Debug("\\-> QUERY: %s VALUE: %s", k, v)
				}
			}
			req.URL.RawQuery = q.Encode()
			ChunkSize = total_bytes - transfered_bytes
		}

		err = w.WriteField("compressionMode", "NORMAL")
		if err != nil {
			return nil, err
		}

		err = w.WriteField("index", fmt.Sprintf("%d", ChunkIndex+1))
		if err != nil {
			return nil, err
		}

		err = w.WriteField("compressionSize", fmt.Sprintf("%d", ChunkSize))
		if err != nil {
			return nil, err
		}

		err = w.WriteField("originalSize", fmt.Sprintf("%d", ChunkSize))
		if err != nil {
			return nil, err
		}

		f_writer, err := w.CreateFormFile("content", filename)
		if err != nil {
			return nil, err
		}

		if s.Snoop {
			Debug(w_buff.String())
		}

		post := &streamReadCloser{
			ChunkSize,
			0,
			make([]byte, 4096),
			w_buff,
			iotimeout.NewReadCloser(src, s.RequestTimeout),
			false,
			f_writer,
			w,
		}

		req.Body = post
		defer req.Body.Close()

		resp, err := s.Do(req)
		if err != nil {
			return nil, err
		}

		if err := s.DecodeJSON(resp, &resp_data); err != nil {
			return nil, err
		}

		ChunkIndex++
		transfered_bytes = transfered_bytes + ChunkSize
		if total_bytes == 0 {
			break
		}
	}

	if resp_data == nil || resp_data.ID == 0 {
		return nil, ErrUploadNoResp
	}

	return resp_data, nil
}
