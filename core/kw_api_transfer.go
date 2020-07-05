package core

import (
	"bytes"
	"fmt"
	"github.com/cmcoffee/go-snuglib/iotimeout"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Max/Min chunk size for kiteworks
const (
	kw_chunk_size_max = 68157440
	kw_chunk_size_min = 1048576
)

var ErrNoUploadID = fmt.Errorf("Upload ID not found.")
var ErrUploadNoResp = fmt.Errorf("Unexpected empty resposne from server.")

// Returns chunk_size, total number of chunks and last chunk size.
func (K *KWAPI) Chunks(total_size int64) (total_chunks int64) {
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

	/*
	for total_size%chunk_size > 0 {
		chunk_size--
	}*/

	return (total_size / chunk_size) + 1


	//return total_size / chunk_size
}

const (
	wd_started = 1 << iota
)

// Webdownloader for external sources
type web_downloader struct {
	flag            BitFlag
	err             error
	req             *http.Request
	client          *http.Client
	resp            *http.Response
	offset          int64
	trans_limiter   *chan struct{}
	request_timeout time.Duration
}

func (W *web_downloader) Read(p []byte) (n int, err error) {
	if !W.flag.Has(wd_started) {
		if W.req == nil || W.client == nil {
			return 0, fmt.Errorf("Webdownloader not initialized.")
		} else {
			W.flag.Set(wd_started)
			W.client.Timeout = 0
			W.resp, err = W.client.Do(W.req)
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
	}
	n, err = W.resp.Body.Read(p)
	return
}

func (W *web_downloader) Close() error {
	if W.trans_limiter != nil {
		<-*W.trans_limiter
	}
	if W.resp == nil || W.resp.Body == nil {
		return nil
	}
	return W.resp.Body.Close()
}

func (W *web_downloader) Seek(offset int64, whence int) (int64, error) {
	if offset < 0 {
		return 0, fmt.Errorf("Can't read before the start of the file.")
	}
	W.req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	W.offset = offset
	return offset, nil
}

// Perform External Download from a remote request.
func (S *KWSession) Download(req *http.Request) ReadSeekCloser {
	if S.trans_limiter != nil {
		S.trans_limiter <- struct{}{}
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	if S.AgentString == NONE {
		req.Header.Set("User-Agent", "kwlib/1.0")
	} else {
		req.Header.Set("User-Agent", S.AgentString)
	}

	client := S.NewClient()
	return &web_downloader{
		req:             req,
		client:          client.Client,
		request_timeout: S.RequestTimeout,
		trans_limiter:   &S.trans_limiter,
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

// Creates a new upload for a folder.
func (S *KWSession) NewUpload(folder_id int, file os.FileInfo) (int, error) {
	var upload struct {
		ID int `json:"id"`
	}

	if err := S.Call(APIRequest{
		Version: 5,
		Method:  "POST",
		Path:    SetPath("/rest/folders/%d/actions/initiateUpload", folder_id),
		Params:  SetParams(PostJSON{"filename": file.Name(), "totalSize": file.Size(), "clientModified": WriteKWTime(file.ModTime().UTC()), "totalChunks": S.Chunks(file.Size())}, Query{"returnEntity": true}),
		Output:  &upload,
	}); err != nil {
		return -1, err
	}
	return upload.ID, nil
}

// Create a new file version for an existing file.
func (S *KWSession) NewVersion(file_id int, file os.FileInfo) (int, error) {
	var upload struct {
		ID int `json:"id"`
	}

	if err := S.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/files/%d/actions/initiateUpload", file_id),
		Params: SetParams(PostJSON{"filename": file.Name(), "totalSize": file.Size(), "clientModified": WriteKWTime(file.ModTime().UTC()), "totalChunks": S.Chunks(file.Size())}, Query{"returnEntity": true}),
		Output: &upload,
	}); err != nil {
		return -1, err
	}
	return upload.ID, nil
}

// Uploads file from specific local path, uploads in chunks, allows resume.
func (s KWSession) Upload(filename string, upload_id int, source_reader ReadSeekCloser) (*KiteObject, error) {
	if s.trans_limiter != nil {
		s.trans_limiter <- struct{}{}
		defer func() { <-s.trans_limiter }()
	}

	type upload_data struct {
		ID             int    `json:"id"`
		TotalSize      int64  `json:"totalSize"`
		TotalChunks    int64  `json:"totalChunks"`
		UploadedSize   int64  `json:"uploadedSize"`
		UploadedChunks int64  `json:"uploadedChunks"`
		Finished       bool   `json:"finished"`
		URI            string `json:"uri"`
	}

	var upload struct {
		Data []upload_data `json:"data"`
	}

	err := s.Call(APIRequest{
		Method: "GET",
		Path:   "/rest/uploads",
		Params: SetParams(Query{"locate_id": upload_id, "limit": 1, "with": "(id,totalSize,totalChunks,uploadedChunks,finished,uploadedSize)"}),
		Output: &upload,
	})
	if err != nil {
		return nil, err
	}

	var upload_record upload_data

	if upload.Data != nil && len(upload.Data) > 0 {
		for i, v := range upload.Data {
			if upload_id == v.ID {
				upload_record = upload.Data[i]
				break
			}
		}
	}

	if upload_id != upload_record.ID {
		return nil, ErrNoUploadID
	}

	total_bytes := upload_record.TotalSize

	ChunkSize := upload_record.TotalSize / upload_record.TotalChunks
	if upload_record.TotalChunks > 1 {
		ChunkSize++
	}
	ChunkIndex := upload_record.UploadedChunks

	src := TransferMonitor(filename, total_bytes, LeftToRight, source_reader)
	defer src.Close()

	if ChunkIndex > 0 {
		if upload_record.UploadedSize > 0 && upload_record.UploadedChunks > 0 {
			if _, err = src.Seek(ChunkSize*ChunkIndex, 0); err != nil {
				return nil, err
			}
		}
	}

	transfered_bytes := upload_record.UploadedSize

	w_buff := new(bytes.Buffer)

	var resp_data *KiteObject

	for transfered_bytes < total_bytes || total_bytes == 0 {
		w_buff.Reset()

		req, err := s.NewRequest("POST", fmt.Sprintf("/%s", upload_record.URI), 7)
		if err != nil {
			return nil, err
		}

		if s.Debug {
			Debug("\n[kiteworks]: %s", s.Username)
			Debug("--> METHOD: \"POST\" PATH: \"%v\" (CHUNK %d OF %d)\n", req.URL.Path, ChunkIndex+1, upload_record.TotalChunks)
		}

		w := multipart.NewWriter(w_buff)

		req.Header.Set("Content-Type", "multipart/form-data; boundary="+w.Boundary())

		if ChunkIndex == upload_record.TotalChunks-1 {
			q := req.URL.Query()
			q.Set("returnEntity", "true")
			q.Set("mode", "full")
			if s.Debug {
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

		if s.Debug {
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
		client := s.NewClient()
		client.Timeout = 0

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		if err := s.decodeJSON(resp, &resp_data); err != nil {
			return nil, err
		}

		ChunkIndex++
		transfered_bytes = transfered_bytes + ChunkSize
		if total_bytes == 0 {
			break
		}
	}

	if resp_data.ID == 0 {
		return nil, ErrUploadNoResp
	}

	return resp_data, nil
}
