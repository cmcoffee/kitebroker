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

// kw_chunk_size_max defines the maximum chunk size.
// kw_chunk_size_min defines the minimum chunk size.
const (
	kw_chunk_size_max = 68157440
	kw_chunk_size_min = 1048576
)

// ErrNoUploadID indicates that the upload ID was not found.
// ErrUploadNoResp indicates an empty response from the server.
// ErrUploadFinished indicates the upload is already completed.
var (
	ErrNoUploadID     = errors.New("Upload ID not found.")
	ErrUploadNoResp   = errors.New("Unexpected empty resposne from server.")
	ErrUploadFinished = errors.New("Upload already marked as complete.")
)

// chunksCalc calculates the total number of chunks required for a given size.
// It considers the maximum and minimum chunk sizes to determine the optimal
// number of chunks. It returns the total number of chunks.
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

// uploadFile uploads a file to the KiteWorks server.
// It handles chunking, progress tracking, and error handling.
func (K KWSession) uploadFile(filename string, upload_id int, source_reader io.ReadSeekCloser, path ...string) (*KiteObject, error) {
	if K.trans_limiter != nil {
		K.trans_limiter <- struct{}{}
		defer func() { <-K.trans_limiter }()
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

	err = K.Call(APIRequest{
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
		req, err := K.NewRequest("POST", fmt.Sprintf("/%s", upload_data.URI))
		if err != nil {
			return nil, err
		}

		req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", DEFAULT_KWAPI_VERSION))

		Trace("[kiteworks]: %s", K.Username)
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

		req.Body = iotimeout.NewReadCloser(src, K.RequestTimeout)
		mimebody.ConvertFormFile(req, "content", filename, fields, ChunkSize)

		for k, v := range fields {
			Trace("\\-> FORM FIELD: %s=%s", k, v)
		}

		Trace("\\-> FORM DATA: name=\"content\"; filename=\"%s\"", filename)

		resp, err := K.APIClient.SendRequest(K.Username, req)
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

// newFileVersion initiates a new file version upload.
// It returns the ID of the new file version or an error.
func (K KWSession) newFileVersion(file_id string, filename string, size int64, mod_time time.Time, params ...interface{}) (int, error) {
	var upload struct {
		ID int `json:"id"`
	}

	if err := K.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/files/%s/actions/initiateUpload", file_id),
		Params: SetParams(PostJSON{"filename": filename, "totalSize": size, "clientModified": WriteKWTime(mod_time.UTC()), "totalChunks": K.chunksCalc(size)}, Query{"returnEntity": true}, params),
		Output: &upload,
	}); err != nil {
		return -1, err
	}
	return upload.ID, nil
}

// newFolderUpload initiates a new upload for a given folder.
// It returns the upload ID or an error if the request fails.
func (K KWSession) newFolderUpload(folder_id string, filename string, size int64, mod_time time.Time, params ...interface{}) (int, error) {
	var upload struct {
		ID int `json:"id"`
	}

	if err := K.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/folders/%s/actions/initiateUpload", folder_id),
		Params: SetParams(PostJSON{"filename": filename, "totalSize": size, "clientModified": WriteKWTime(mod_time.UTC()), "totalChunks": K.chunksCalc(size)}, Query{"returnEntity": true}, params),
		Output: &upload,
	}); err != nil {
		return -1, err
	}
	return upload.ID, nil
}

// Upload Uploads file from specific local path, uploads in chunks, allows resume.
// Will assume source will be closed, it is on caller to reinitiate upload request open source upon failure.
func (K KWSession) Upload(filename string, size int64, mod_time time.Time, overwrite_newer, auto_version, resume bool, dst KiteObject, src ReadSeekCloser) (file *KiteObject, err error) {
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
	uploads := K.db.Table("uploads")

	delete_upload := func(target string) {
		if uploads.Get(target, &UploadRecord) {
			K.Call(APIRequest{
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
		if output, err := K.uploadFile(filename, UploadRecord.ID, src, dest_path); err != nil {
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
				uid, err = K.newFileVersion(dst.ID, filename, size, mod_time)
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
				uid, err = K.newFileVersion(dst.ID, filename, size, mod_time)
				if err != nil {
					return nil, err
				}
			} else {
				return nil, nil
			}
		}
	} else {
		files, err := K.Folder(dst.ID).Files(SetParams(Query{"deleted": false, "name": filename}))
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			uid, err = K.newFolderUpload(dst.ID, filename, size, mod_time)
			if err != nil {
				return nil, err
			}
		} else {
			files[0].Path = dest_path
			return K.Upload(filename, size, mod_time, overwrite_newer, auto_version, resume, files[0], src)
		}

	}

	UploadRecord.Name = filename
	UploadRecord.ID = uid
	UploadRecord.ClientModified = mod_time
	UploadRecord.Size = size
	uploads.Set(target, &UploadRecord)

	file, err = K.uploadFile(filename, uid, src, dest_path)
	if err == nil {
		uploads.Unset(target)
	}

	return
}

// QDownload downloads the content of a KiteObject.
// It returns a ReadSeekCloser and an error.
func (K KWSession) QDownload(file *KiteObject) (ReadSeekCloser, error) {
	if file == nil {
		return nil, fmt.Errorf("nil file object provided.")
	}

	req, err := K.NewRequest("GET", SetPath("/rest/files/%s/content", file.ID))
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", DEFAULT_KWAPI_VERSION))

	err = K.SetToken(K.Username, req)

	downloader := K.WebDownload(req)
	downloader.Seek(-500, -500)

	return downloader, err
}

// Download retrieves the content of a KiteObject as a ReadSeekCloser.
// It handles request creation, token setting, and transfer monitoring.
// Returns the ReadSeekCloser and an error if any occurred.
func (K KWSession) Download(file *KiteObject) (ReadSeekCloser, error) {
	if file == nil {
		return nil, fmt.Errorf("nil file object provided.")
	}

	req, err := K.NewRequest("GET", SetPath("/rest/files/%s/content", file.ID))
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Accellion-Version", fmt.Sprintf("%d", DEFAULT_KWAPI_VERSION))

	err = K.SetToken(K.Username, req)

	return transferMonitor(file.Name, file.Size, rightToLeft, K.WebDownload(req), strings.TrimSuffix(file.Path, file.Name)), err
}

// LocalDownload downloads a file to a local path.
// It handles existing files, modification times, and potential errors.
func (K KWSession) LocalDownload(file *KiteObject, local_path string, transfer_counter_cb func(c int)) (err error) {
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

	f, err := K.Download(file)
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
