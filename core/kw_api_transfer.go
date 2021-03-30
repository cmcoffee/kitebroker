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
func (K *APIClient) chunksCalc(total_size int64) (total_chunks int64) {
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


type downloadError struct {
	err error
}

func (d downloadError) Error() string {
	return d.err.Error()
}

// Check if error was produced during download of file.
func IsDownloadError(err error) bool {
	if _, ok := err.(*downloadError); ok {
		return true
	} 
	return false
}

func (W *web_downloader) Read(p []byte) (n int, err error) {
	if !W.flag.Has(wd_started) {
		W.req = W.reqs[0]
		if W.req == nil {
			return 0, downloadError{fmt.Errorf("Webdownloader not initialized.")}
		}
		W.reqs = append(W.reqs[:0], W.reqs[1:]...)
		W.flag.Set(wd_started)
		W.resp, err = W.api.SendRequest(&APISession{NONE, W.req})
		if err != nil {
			return 0, downloadError{fmt.Errorf("Download Error(%s): %s", W.req.URL, err)}
		}

		err = W.api.RespErrorCheck(W.resp)
		if err != nil {
			return 0, downloadError{fmt.Errorf("Download Error(%s): %s", W.req.URL, err)}
		}

		if W.offset > 0 {
			content_range := strings.Split(strings.TrimPrefix(W.resp.Header.Get("Content-Range"), "bytes"), "-")
			if len(content_range) > 1 {
				if strings.TrimSpace(content_range[0]) != strconv.FormatInt(W.offset, 10) {
					return 0, downloadError{fmt.Errorf("Requested byte %v, got %v instead.", W.offset, content_range[0])}
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
		return downloadError{W.resp.Body.Close()}
	}
	return nil
}

// Seek an offset within the download, added Range header to request.
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
	w_buff    *bytes.Buffer
	source    io.ReadCloser
	eof       bool
	f_writer  io.Writer
	mwrite    *multipart.Writer
}

// Dummy close function for requst.Body.
func (s *streamReadCloser) Close() (err error) {
	return nil
}

// Reads bytes from source, pushes through mimewriter to bytes.Buffer, and reads from bytes.Buffer.
func (s *streamReadCloser) Read(p []byte) (n int, err error) {
	
	// If we have stuff in our output buffer, read from there.
	// If not, reset the bytes buffer and read from source.
	if s.w_buff.Len() > 0 {
		return s.w_buff.Read(p)
	} else {
		s.w_buff.Reset()
	}

	// We've reached the EOF, return to process.
	if s.eof {
		return 0, io.EOF
	}

	// Get length of incoming []byte slice.
	p_len := int64(len(p))

	if sz := s.chunkSize-s.size; sz > 0 {
		if sz > p_len {
			sz = p_len
		}

		// Read into the byte slice provided from source.
		n, err := s.source.Read(p[0:sz])
		if err != nil {
			if err == io.EOF {
				s.eof = true
			} else {
				return -1, err
			}
		}
	
		// We're writing to a bytes.Buffer.
		_, err = s.f_writer.Write(p[0:n])
		if err != nil {
			return -1, err
		}

		// Clear out the []byte slice provided.
		for i := 0; i < n; i++ {
			p[i] = 0
		}

		s.size = s.size + int64(n)
	} else {
		s.eof = true
	}

	// Close out the mime stream.
	if s.eof {
		s.mwrite.Close()
	}

	return s.w_buff.Read(p)
}

// Uploads file from specific local path, uploads in chunks, allows resume.
func (s KWSession) uploadFile(filename string, upload_id int, source_reader ReadSeekCloser) (*KiteObject, error) {
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
			w_buff,
			iotimeout.NewReadCloser(src, s.RequestTimeout),
			false,
			f_writer,
			w,
		}

		req.Body = post

		close_req := func(req *APISession) {
			if req.Body != nil {
				req.Body.Close()
			}
		}

		resp, err := s.APIClient.SendRequest(req)
		if err != nil {
			close_req(req)
			return nil, err
		}

		if err := s.DecodeJSON(resp, &resp_data); err != nil {
			close_req(req)
			return nil, err
		}

		close_req(req)
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

// Create a new file version for an existing file.
func (S KWSession) newFileVersion(file_id int, filename string, size int64, mod_time time.Time, params ...interface{}) (int, error) {
	var upload struct {
		ID int `json:"id"`
	}

	if err := S.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/files/%d/actions/initiateUpload", file_id),
		Params: SetParams(PostJSON{"filename": filename, "totalSize": size, "clientModified": WriteKWTime(mod_time.UTC()), "totalChunks": S.chunksCalc(size)}, Query{"returnEntity": true}, params),
		Output: &upload,
	}); err != nil {
		return -1, err
	}
	return upload.ID, nil
}

// Creates a new upload for a folder.
func (S KWSession) newFolderUpload(folder_id int, filename string, size int64, mod_time time.Time, params ...interface{}) (int, error) {
	var upload struct {
		ID int `json:"id"`
	}

	if err := S.Call(APIRequest{
		//Version: 5,
		Method: "POST",
		Path:   SetPath("/rest/folders/%d/actions/initiateUpload", folder_id),
		Params: SetParams(PostJSON{"filename": filename, "totalSize": size, "clientModified": WriteKWTime(mod_time.UTC()), "totalChunks": S.chunksCalc(size)}, Query{"returnEntity": true}, params),
		Output: &upload,
	}); err != nil {
		return -1, err
	}
	return upload.ID, nil
}

// Uploads file from specific local path, uploads in chunks, allows resume.
func (s KWSession) Upload(filename string, size int64, mod_time time.Time, overwrite_newer, auto_version, resume bool, dst KiteObject, src ReadSeekCloser) (file *KiteObject, err error) {
	var flags BitFlag

	const (
		IsFolder = 1 << iota
		IsFile
		OverwriteFile
		VersionFile
	)

	switch dst.Type {
		case "d":
			flags.Set(IsFolder)
		case "f":
			flags.Set(IsFile)
	}

	if overwrite_newer {
		flags.Set(OverwriteFile)
	}

	if auto_version {
		flags.Set(VersionFile)
	}

	var UploadRecord struct {
		Name string
		ID int
		ClientModified time.Time
		Size int64
		Created time.Time
	}

	target := fmt.Sprintf("%d:%s:%d:%d", dst.ID, filename, size, mod_time.UTC().Unix())
	uploads := s.db.Table("uploads")

	delete_upload := func(target string) {
		if uploads.Get(target, &UploadRecord) {
			s.Call(APIRequest{
				Method: "DELETE",
				Path: SetPath("/rest/uploads/%d", UploadRecord.ID),
			})
			uploads.Unset(target)
		}
	}

	if !resume {
		delete_upload(target)
	}

	var uid int

	if uploads.Get(target, &UploadRecord) {
		if output, err := s.uploadFile(filename, UploadRecord.ID, src); err != nil {
			Debug("Error attempting to resume file: %s", err.Error())
			delete_upload(target)
		} else {
			uploads.Unset(target)
			return output, err
		}
	}

	var kw_file_info KiteObject

	if flags.Has(IsFile) {
		kw_file_info = dst
		dst, err = s.Folder(0).Find(dst.Path)
		if err != nil {
			return nil, err
		}
	} else {
		kw_file_info, err = s.Folder(dst.ID).Find(filename)
		if err != nil && err != ErrNotFound {
			return nil, err
		}
	} 

	if kw_file_info.ID > 0 {
		modified, _ := ReadKWTime(kw_file_info.ClientModified)

		if modified.UTC().Unix() > mod_time.UTC().Unix() {
			if flags.Has(OverwriteFile) {
				uid, err = s.newFileVersion(kw_file_info.ID, filename, size, mod_time)
				if err != nil {
					return nil, err
				}
			} else {
				uploads.Unset(target)
				return nil, nil
			}
		} else {
			if kw_file_info.Size == size {
				uploads.Unset(target)
				return nil, nil
			}
		}
	} else {
		uid, err = s.newFolderUpload(dst.ID, filename, size, mod_time)
		if err != nil {
			return nil, err
		}
	}

	UploadRecord.Name = filename
	UploadRecord.ID = uid
	UploadRecord.ClientModified = mod_time
	UploadRecord.Size = size
	uploads.Set(target, &UploadRecord)

	file, err = s.uploadFile(filename, uid, src)
	return
}

