package core

import (
	"errors"
	"fmt"
	"github.com/cmcoffee/snugforge/iotimeout"
	"github.com/cmcoffee/snugforge/mimebody"
	"io"
	"os"
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
func (K *KWSession) chunksCalc(total_size int64) (total_chunks int64) {
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

// Uploads file from specific local path, uploads in chunks, allows resume.
func (s KWSession) uploadFile(filename string, upload_id int, source_reader io.ReadSeekCloser, path ...string) (*KiteObject, error) {
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

	defer source_reader.Close()

	_, err := source_reader.Seek(0, 0)
	if err != nil {
		return nil, err
	}

	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/uploads/%d", upload_id),
		Params: SetParams(Query{"with": "(id,totalSize,totalChunks,uploadedChunks,finished,uploadedSize)"}),
		Output: &upload_data,
	})
	if err != nil {
		return nil, ErrNoUploadID
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

	var resp_data *KiteObject

	for transferred_bytes < total_bytes || total_bytes == 0 {
		req, err := s.NewRequest("POST", fmt.Sprintf("/%s", upload_data.URI))
		if err != nil {
			return nil, err
		}

		req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", DEFAULT_KWAPI_VERSION))

		Trace("[kiteworks]: %s", s.Username)
		Trace("--> METHOD: \"POST\" PATH: \"%v\" (CHUNK %d OF %d)\n", req.URL.Path, ChunkIndex+1, upload_data.TotalChunks)
		Trace("--> HEADER: Content-Type: [multipart/form-data]")

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

		fields := make(map[string]string)
		fields["compressionMode"] = "NORMAL"
		fields["index"] = fmt.Sprintf("%d", ChunkIndex+1)
		fields["compressionSize"] = fmt.Sprintf("%d", ChunkSize)
		fields["originalSize"] = fmt.Sprintf("%d", ChunkSize)

		req.Body = iotimeout.NewReadCloser(src, s.RequestTimeout)
		mimebody.ConvertFormFile(req, "content", filename, fields, ChunkSize)

		for k, v := range fields {
			Trace("\\-> FORM FIELD: %s=%s", k, v)
		}

		Trace("\\-> FORM DATA: name=\"content\"; filename=\"%s\"", filename)

		resp, err := s.APIClient.SendRequest(s.Username, req)
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
// Will assume source will be closed, it is on caller to reinitiate upload request open source upon failure.
func (s KWSession) Upload(filename string, size int64, mod_time time.Time, overwrite_newer, auto_version, resume bool, dst KiteObject, src ReadSeekCloser) (file *KiteObject, err error) {
	var flags BitFlag

	const (
		IsFolder = 1 << iota
		IsFile
		OverwriteFile
		VersionFile
	)

	dest_path := strings.TrimPrefix(dst.Path, "basedir/")
	if len(dest_path) > 0 && !strings.HasSuffix(dest_path, "/") {
		dest_path = fmt.Sprintf("%s/", dest_path)
	}

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
			return nil, err
		} else {
			uploads.Unset(target)
			return output, err
		}
	}

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
			} else if flags.Has(VersionFile) {
				uid, err = s.newFileVersion(dst.ID, filename, size, mod_time)
				if err != nil {
					return nil, err
				}
			} else {
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
			files[0].Path = dest_path
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

// Quiet Download
func (s KWSession) QDownload(file *KiteObject) (ReadSeekCloser, error) {
	if file == nil {
		return nil, fmt.Errorf("nil file object provided.")
	}

	req, err := s.NewRequest("GET", SetPath("/rest/files/%s/content", file.ID))
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", DEFAULT_KWAPI_VERSION))

	err = s.SetToken(s.Username, req)

	downloader := s.WebDownload(req)
	downloader.Seek(-500, -500)

	return downloader, err
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

	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", DEFAULT_KWAPI_VERSION))

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

	if fstat != nil {
		offset, err := dst.Seek(fstat.Size(), 0)
		if err != nil {
			dst.Close()
			return err
		}
		_, err = f.Seek(offset, 0)
		if err != nil {
			dst.Close()
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
				dst.Close()
				os.Remove(tmp_file_name)
				return nil
			}
			if file.AVStatus != "allowed" {
				Notice("%s/%s: Cannot be downloaded, anti-virus status is currently set to: %s", strings.TrimSuffix(local_path, SLASH), file.Name, file.AVStatus)
				dst.Close()
				os.Remove(tmp_file_name)
				return nil
			}
			if file.DLPStatus != "allowed" {
				Notice("%s/%s: Cannot be downloaded, dli status is currently set to: %s", strings.TrimSuffix(local_path, SLASH), file.Name, file.DLPStatus)
				dst.Close()
				os.Remove(tmp_file_name)
				return nil
			}
			return err
		}
	}

	dst.Close()
	err = Rename(tmp_file_name, dest_file)
	if err != nil {
		return err
	}

	err = os.Chtimes(dest_file, time.Now(), mtime)
	return
}
