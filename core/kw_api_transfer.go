package core

import (
	"bytes"
	"errors"
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

var (
	ErrNoUploadID     = errors.New("Upload ID not found.")
	ErrUploadNoResp   = errors.New("Unexpected empty resposne from server.")
	ErrUploadFinished = errors.New("Upload already marked as complete.")
)

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
			if IsAPIError(err) {
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
func (S *APIClient) WebDownload(reqs ...*http.Request) ReadSeekCloser {
	if S.trans_limiter != nil {
		S.trans_limiter <- struct{}{}
	}

	var last_byte []int64

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

	if sz := s.chunkSize - s.size; sz > 0 {
		if sz > p_len {
			sz = p_len
		}

		// Read into the byte slice provided from source.
		n, err := s.source.Read(p[0:sz])
		if err != nil {
			if err == io.EOF {
				s.eof = true
			} else {
				return n, err
			}
		}

		// We're writing to a bytes.Buffer.
		_, err = s.f_writer.Write(p[0:n])
		if err != nil {
			return n, err
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
func (s KWSession) uploadFile(filename string, upload_id int, source_reader ReadSeekCloser, path ...string) (*KiteObject, error) {
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

	if upload_data.Finished {
		return nil, ErrUploadFinished
	}

	if upload_id != upload_data.ID {
		return nil, ErrNoUploadID
	}

	total_bytes := upload_data.TotalSize

	ChunkSize := upload_data.TotalSize / upload_data.TotalChunks
	if upload_data.TotalChunks > 1 {
		ChunkSize++
	}
	ChunkIndex := upload_data.UploadedChunks

	src := transferMonitor(filename, total_bytes, leftToRight, source_reader, path...)
	defer src.Close()

	if ChunkIndex > 0 {
		if upload_data.UploadedSize > 0 && upload_data.UploadedChunks > 0 {
			if _, err := src.Seek(ChunkSize*ChunkIndex, 0); err != nil {
				return nil, err
			}
		}
	}

	transferred_bytes := upload_data.UploadedSize

	w_buff := new(bytes.Buffer)

	var resp_data *KiteObject

	for transferred_bytes < total_bytes || total_bytes == 0 {
		w_buff.Reset()

		req, err := s.NewRequest("POST", fmt.Sprintf("/%s", upload_data.URI))
		if err != nil {
			return nil, err
		}

		req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", 20))

		Trace("[kiteworks]: %s", s.Username)
		Trace("--> METHOD: \"POST\" PATH: \"%v\" (CHUNK %d OF %d)\n", req.URL.Path, ChunkIndex+1, upload_data.TotalChunks)

		w := multipart.NewWriter(w_buff)

		req.Header.Set("Content-Type", "multipart/form-data; boundary="+w.Boundary())

		if ChunkIndex == upload_data.TotalChunks-1 {
			q := req.URL.Query()
			q.Set("returnEntity", "true")
			q.Set("mode", "full")
			for k, v := range q {
				Trace("\\-> QUERY: %s VALUE: %s", k, v)
			}

			req.URL.RawQuery = q.Encode()
			ChunkSize = total_bytes - transferred_bytes
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

		Trace(w_buff.String())

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

		resp, err := s.APIClient.SendRequest(s.Username, req)
		if req.Body != nil {
			req.Body.Close()
		}

		if err != nil {
			return nil, err
		}

		if err := DecodeJSON(resp, &resp_data); err != nil {
			return nil, err
		}

		ChunkIndex++
		transferred_bytes = transferred_bytes + ChunkSize
		if total_bytes == 0 {
			break
		}
	}

	if resp_data == nil || (IsBlank(resp_data.ID) || resp_data.ID == "0") {
		return nil, ErrUploadNoResp
	}

	return resp_data, nil
}

// Create a new file version for an existing file.
func (S KWSession) newFileVersion(file_id string, filename string, size int64, mod_time time.Time, params ...interface{}) (int, error) {
	var upload struct {
		ID int `json:"id"`
	}

	if err := S.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/files/%s/actions/initiateUpload", file_id),
		Params: SetParams(PostJSON{"filename": filename, "totalSize": size, "clientModified": WriteKWTime(mod_time.UTC()), "totalChunks": S.chunksCalc(size)}, Query{"returnEntity": true}, params),
		Output: &upload,
	}); err != nil {
		return -1, err
	}
	return upload.ID, nil
}

// Creates a new upload for a folder.
func (S KWSession) newFolderUpload(folder_id string, filename string, size int64, mod_time time.Time, params ...interface{}) (int, error) {
	var upload struct {
		ID int `json:"id"`
	}

	if err := S.Call(APIRequest{
		//Version: 5,
		Method: "POST",
		Path:   SetPath("/rest/folders/%s/actions/initiateUpload", folder_id),
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

	dest_path := strings.TrimPrefix(dst.Path, "basedir/")
	dest_path = fmt.Sprintf("%s/", dest_path)

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
		Name           string
		ID             int
		ClientModified time.Time
		Size           int64
		Created        time.Time
	}

	target := fmt.Sprintf("%s:%s:%d:%d", dst.ID, filename, size, mod_time.UTC().Unix())
	uploads := s.db.Table("uploads")

	delete_upload := func(target string) {
		if uploads.Get(target, &UploadRecord) {
			s.Call(APIRequest{
				Method: "DELETE",
				Path:   SetPath("/rest/uploads/%d", UploadRecord.ID),
			})
			uploads.Unset(target)
		}
	}

	if !resume {
		delete_upload(target)
	}

	var uid int

	if uploads.Get(target, &UploadRecord) {
		if output, err := s.uploadFile(filename, UploadRecord.ID, src, dest_path); err != nil {
			Debug("Error attempting to resume file %s: %s", filename, err.Error())
			delete_upload(target)
		} else {
			uploads.Unset(target)
			return output, err
		}
	}

	//	var kw_file_info KiteObject

	//	if flags.Has(IsFile) {
	//		kw_file_info = dst
	//dst, err = s.Folder("0").Find(dst.Path)
	//if err != nil {
	//	return nil, err
	//}
	//} else {
	//	kw_file_info, err = s.Folder(dst.ID).Find(filename)
	//	if err != nil && err != ErrNotFound {
	//		return nil, err
	//	}
	//}

	if flags.Has(IsFile) {
		modified, _ := ReadKWTime(dst.ClientModified)

		if modified.UTC().Unix() > mod_time.UTC().Unix() {
			if flags.Has(OverwriteFile) {
				uid, err = s.newFileVersion(dst.ID, filename, size, mod_time)
				if err != nil {
					return nil, err
				}
			} else {
				uploads.Unset(target)
				return nil, nil
			}
		} else {
			if dst.Size == size {
				uploads.Unset(target)
				return nil, nil
			}
		}
	} else {
		files, err := s.Folder(dst.ID).Files(SetParams(Query{"deleted": false, "name": filename}))
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			uid, err = s.newFolderUpload(dst.ID, filename, size, mod_time)
			if err != nil {
				return nil, err
			}
		} else {
			return s.Upload(filename, size, mod_time, overwrite_newer, auto_version, resume, files[0], src)
		}

	}

	UploadRecord.Name = filename
	UploadRecord.ID = uid
	UploadRecord.ClientModified = mod_time
	UploadRecord.Size = size
	uploads.Set(target, &UploadRecord)

	file, err = s.uploadFile(filename, uid, src, dest_path)
	if err == nil {
		uploads.Unset(target)
	}

	return
}

//Quiet Download
func (s KWSession) QDownload(file *KiteObject) (ReadSeekCloser, error) {
	if file == nil {
		return nil, fmt.Errorf("nil file object provided.")
	}

	req, err := s.NewRequest("GET", SetPath("/rest/files/%s/content", file.ID))
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", 20))

	err = s.SetToken(s.Username, req)

	return s.WebDownload(req), err
}

// Downloads a file to from Kiteworks
func (s KWSession) Download(file *KiteObject) (ReadSeekCloser, error) {
	if file == nil {
		return nil, fmt.Errorf("nil file object provided.")
	}

	req, err := s.NewRequest("GET", SetPath("/rest/files/%s/content", file.ID))
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", 20))

	err = s.SetToken(s.Username, req)

	return transferMonitor(file.Name, file.Size, rightToLeft, s.WebDownload(req), strings.TrimSuffix(file.Path, file.Name)), err
}

// Kiteworks File Download to Local File
func (s KWSession) LocalDownload(file *KiteObject, local_path string, transfer_counter_cb func(c int)) (err error) {
	if file == nil {
		return fmt.Errorf("nil file object provided.")
	}

	if IsBlank(file.ClientModified) {
		if !IsBlank(file.ClientCreated) {
			file.ClientModified = file.ClientCreated
		} else {
			file.ClientModified = file.Created
		}
	}

	mtime, err := ReadKWTime(file.ClientModified)
	if err != nil {
		return err
	}

	dest_file := CombinePath(local_path, file.Name)
	tmp_file_name := fmt.Sprintf("%s.%d.%d.incomplete", dest_file, file.Size, mtime.Unix())

	dstat, err := os.Stat(dest_file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if dstat != nil && dstat.Size() == file.Size && dstat.ModTime().UTC().Unix() == mtime.UTC().Unix() {
		return nil
	}

	f, err := s.Download(file)
	if err != nil {
		return err
	}
	defer f.Close()

	fstat, err := os.Stat(tmp_file_name)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	dst, err := os.OpenFile(tmp_file_name, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return
	}
	defer dst.Close()

	if fstat != nil {
		offset, err := dst.Seek(fstat.Size(), 0)
		if err != nil {
			return err
		}
		_, err = f.Seek(offset, 0)
		if err != nil {
			return err
		}
	}

	if fstat == nil || fstat.Size() != file.Size {
		if transfer_counter_cb != nil {
			f = TransferCounter(f, transfer_counter_cb)
		}

		_, err := io.Copy(dst, f)
		if err != nil {
			if file.AdminQuarantineStatus != "allowed" {
				Notice("%s/%s: Cannot be downloaded, file is under administrator quarantine.", strings.TrimSuffix(local_path, SLASH), file.Name)
				os.Remove(tmp_file_name)
				return nil
			}
			if file.AVStatus != "allowed" {
				Notice("%s/%s: Cannot be downloaded, anti-virus status is currently set to: %s", strings.TrimSuffix(local_path, SLASH), file.Name, file.AVStatus)
				os.Remove(tmp_file_name)
				return nil
			}
			if file.DLPStatus != "allowed" {
				Notice("%s/%s: Cannot be downloaded, dli status is currently set to: %s", strings.TrimSuffix(local_path, SLASH), file.Name, file.DLPStatus)
				os.Remove(tmp_file_name)
				return nil
			}
			return err
		}
	}

	err = Rename(tmp_file_name, dest_file)
	if err != nil {
		return err
	}

	err = os.Chtimes(dest_file, time.Now(), mtime)
	return
}
