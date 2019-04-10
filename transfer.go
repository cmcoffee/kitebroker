package main

import (
	"bytes"
	"fmt"
	"github.com/cmcoffee/go-nfo"
	"io"
	"mime/multipart"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var ErrNotReady = fmt.Errorf("Export not ready.")

// KiteBroker Flags
const (
	UPLOAD = 1 << iota
	DONE
)

type UploadRecord struct {
	Flag            int       `json:"flag"`
	CompletedChunks int       `json:"chunks_completed"`
	ChunkSize       int64     `json:"chunk_size"`
	TotalSize       int64     `json:"total_size"`
	Transfered      int64     `json:"transfered"`
	ModTime         time.Time `json:"modified"`
	URI             string    `json:"upload_uri"`
	Filename        string    `json:"filename"`
	ID              int       `json:"id"`
	User            Session   `json:"user"`
}

type FileRecord struct {
	Flag      int       `json:"flag"`
	TotalSize int64     `json:"total_size"`
	ModTime   time.Time `json:"modified"`
	ID        int       `json:"id"`
	User      Session   `json:"user"`
}

const ChunkSizeMax = 68157440

// Returns total number of chunks and last chunk size.
func (s Session) getChunkInfo(sz int64) (chunk_size int64, total_chunks int, last_chunk int64) {

	chunk_size = Config.GetInt("tweaks", "chunk_size_mb")
	chunk_size = chunk_size * 1024000

	if chunk_size < 1024000 {
		chunk_size = ChunkSizeMax
	} else if chunk_size > ChunkSizeMax {
		chunk_size = ChunkSizeMax
	}

	if sz <= chunk_size {
		return chunk_size, 1, sz
	}

	for {
		total_chunks++
		if sz > chunk_size {
			sz = sz - chunk_size
			continue
		} else {
			last_chunk = sz
			return
		}
	}
}

// Provides human readable file sizes.
func showSize(bytes int64) string {

	names := []string{
		"Bytes",
		"KB",
		"MB",
		"GB",
	}

	suffix := 0
	size := float64(bytes)

	for size >= 1000 && suffix < len(names)-1 {
		size = size / 1000
		suffix++
	}

	return fmt.Sprintf("%.1f%s", size, names[suffix])
}

// Provides average rate of transfer.
func (t *TMonitor) showRate() string {

	if t.completed {
		return t.rate
	}

	transfered := atomic.LoadInt64(&t.transfered)
	sz := float64(transfered-t.offset) * 8 / time.Since(t.start_time).Seconds()

	names := []string{
		"bps",
		"kbps",
		"mbps",
		"gbps",
	}

	suffix := 0

	for sz >= 1000 && suffix < len(names)-1 {
		sz = sz / 1000
		suffix++
	}

	if sz != 0.0 {
		t.rate = fmt.Sprintf("%.1f%s", sz, names[suffix])
	} else {
		t.rate = "0.0bps"
	}

	if transfered+t.offset == t.total_size {
		t.completed = true
	}

	return t.rate
}

// Produces progress bar for information on update.
func (t *TMonitor) progressBar() string {
	num := int((float64(atomic.LoadInt64(&t.transfered)) / float64(t.total_size)) * 100)
	if t.total_size == 0 {
		num = 100
	}
	var display [25]rune
	for n := range display {
		if n < num/4 {
			display[n] = '░'
		} else {
			display[n] = '.'
		}
	}
	return fmt.Sprintf("[%s] %d%%", string(display[0:]), int(num))
}

func NewTMonitor(title string, total_sz int64) *TMonitor {
	return &TMonitor{
		name:       title,
		total_size: total_sz,
		transfered: 0,
		offset:     0,
		rate:       "0.0bps",
		start_time: time.Now(),
	}
}

type TMonitor struct {
	name       string
	total_size int64
	transfered int64
	offset     int64
	rate       string
	completed  bool
	start_time time.Time
}

func (t *TMonitor) RecordTransfer(current_sz int) {
	atomic.StoreInt64(&t.transfered, atomic.LoadInt64(&t.transfered)+int64(current_sz))
}

func (t *TMonitor) ShowTransfer(log bool) {

	transfered := atomic.LoadInt64(&t.transfered)

	if t.total_size > -1 {
		if transfered < t.total_size && !log {
			nfo.Flash(fmt.Sprintf("(%s) %s %s (%s/%s)", t.name, t.showRate(), t.progressBar(), showSize(transfered), showSize(t.total_size)))
		}
		if log {
			nfo.Log(fmt.Sprintf("(%s) %s %s (%s/%s)", t.name, t.showRate(), t.progressBar(), showSize(transfered), showSize(t.total_size)))
		}
	} else {
		nfo.Flash(fmt.Sprintf("(%s) %s (%s)", t.name, t.showRate(), showSize(transfered)))
	}
}

func (t *TMonitor) Offset(current_sz int64) {
	t.transfered = current_sz
	t.offset = t.transfered
}

// Perform the file transfer, input stream->output stream.
func Transfer(reader io.Reader, writer io.Writer, tm *TMonitor) (err error) {

	buf := make([]byte, 4096)
	var eof bool
	defer tm.showRate()

	for {
		rb, err := reader.Read(buf)
		if err != nil && err == io.EOF {
			eof = true
		} else if err != nil {
			return err
		}
		n := 0
		for {
			wb, err := writer.Write(buf[n:rb])
			if err != nil {
				return err
			}
			tm.RecordTransfer(wb)
			n = n + wb
			if n == rb {
				break
			}
		}
		if eof {
			return nil
		}
	}
	return nil
}

// Downloads a file to a specific path
func (s Session) Download(kwdl KiteData, local_dest string) (err error) {
	var record FileRecord
	_, err = DB.Get("downloads", kwdl.ID, &record)
	if err != nil {
		return err
	}

	skip_empty_files := func() bool {
		if Config.GetBool("folder_download:opts", "skip_empty_files") {
			return true
		} else {
			return false
		}
	}()

	if kwdl.Size == 0 && skip_empty_files {
		return ErrDownloaded
	}

	mtime, err := read_kw_time(kwdl.Modified)
	if err != nil {
		return err
	}

	if record.ModTime.IsZero() || time.Since(mtime) > time.Since(record.ModTime) || record.TotalSize != kwdl.Size {
		DB.Unset("downloads", kwdl.ID)
		record.Flag = 0
		record.ModTime = mtime
		record.User = s
		record.TotalSize = kwdl.Size
		record.ID = kwdl.ID
		DB.Set("downloads", kwdl.ID, &record)
	}

	if record.Flag&DONE == DONE {
		return ErrDownloaded
	}

	var f *os.File

	split_path := strings.Split(local_dest, SLASH)
	for n, _ := range split_path {
		err = MkPath(strings.Join(split_path[0:n+1], SLASH))
		if err != nil {
			return err
		}
	}

	fname := fmt.Sprintf("%s/%s", local_dest, kwdl.Name)

	fstat, err := Stat(fname)
	if err == nil && !os.IsNotExist(err) {
		if fstat.Size() == kwdl.Size {
			md5sum, _ := md5Sum(fname)
			if string(md5sum) == kwdl.Fingerprint {
				record.Flag = DONE
				DB.Set("downloads", kwdl.ID, record)
				return ErrDownloaded
			}
		}
	}

	var offset int64
	var new_file bool

	fstat, err = Stat(fname + ".incomplete")
	if err != nil && os.IsNotExist(err) {
		new_file = true
	}

	if err == nil || !new_file {
		offset = fstat.Size()
	}

	f, err = OpenFile(fname+".incomplete", os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return
	}

	nfo.Log("Downloading %s(%s).\n", strings.TrimPrefix(fname, Config.Get("configuration", "local_path")), showSize(kwdl.Size))

	var request_path string

	if kwdl.MailID > 0 {
		request_path = fmt.Sprintf("/rest/mail/%d/attachments/%d/content", kwdl.MailID, kwdl.ID)
	} else {
		request_path = fmt.Sprintf("/rest/files/%d/content", kwdl.ID)
	}

	req, err := s.NewRequest("GET", request_path, 7)
	if err != nil {
		if new_file {
			os.Remove(FullPath(fname + ".incomplete"))
		}
		return
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	client := s.NewClient()
	resp, err := client.Do(req)
	if err != nil && resp.StatusCode != 200 {
		if new_file {
			os.Remove(FullPath(fname + ".incomplete"))
		}
		return err
	}
	defer resp.Body.Close()

	tm := NewTMonitor("download", kwdl.Size)
	tm.Offset(offset)

	HideLoader()

	if snoop {
		nfo.Flash("--> ACTION: \"GET\" PATH: \"%v\"\n", req.URL.Path)
	}

	show_transfer := uint32(1)
	defer atomic.StoreUint32(&show_transfer, 0)

	go func() {
		for atomic.LoadUint32(&show_transfer) == 1 {
			tm.ShowTransfer(false)
			time.Sleep(time.Millisecond * 300)
		}
	}()

	// Resume transfer if we've already started downloading a file
	if offset > 0 {
		start := resp.Header.Get("Content-Range")
		if start == NONE {
			goto renameFile
		}
		start = strings.TrimPrefix(start, "bytes")
		byte_range := strings.Split(start, "-")
		start = byte_range[0]
		start = strings.TrimSpace(start)
		offset, err = strconv.ParseInt(start, 10, 64)
		if err != nil {
			return
		}
		_, err = f.Seek(offset, 0)
		if err != nil {
			return
		}
	}

	if err := Transfer(resp.Body, f, tm); err != nil {
		return err
	}
	if err := s.DecodeJSON(resp, nil); err != nil {
		return err
	}

	record.ModTime, err = read_kw_time(kwdl.Modified)
	if err != nil {
		return
	}

	DB.Set("downloads", kwdl.ID, record)

renameFile:
	atomic.StoreUint32(&show_transfer, 0)
	tm.ShowTransfer(true)
	ShowLoader()

	// Close the file stream.
	f.Close()

	// Rename file.
	if err = Rename(fname+".incomplete", fname); err != nil {
		return
	}

	record.Flag |= DONE

	DB.Set("downloads", kwdl.ID, record)

	if Config.GetBool(task+":opts", "supplemental_metadata_info_file") {
		if err := s.MetaData(fname, &kwdl); err != nil {
			nfo.Err(err)
		}
	}

	if Config.GetBool(task+":opts", "delete_source_files_on_complete") {
		nfo.Log("Removing remote file %s from server.", strings.TrimPrefix(fname, Config.Get("configuration", "local_path")))
		if err := s.DeleteFile(kwdl.ID); err != nil {
			nfo.Err(err)
		}
	}

	// Set modified and access times on file.
	err = Chtimes(fname, time.Now(), record.ModTime)
	if err == nil {
		nfo.Log("%s: Download complete.", fname)
	}
	return
}

// Multipart filestreamer
type streamReadCloser struct {
	limit    int64
	size     int64
	r_buff   []byte
	w_buff   *bytes.Buffer
	reader   io.Reader
	eof      bool
	f_writer io.Writer
	tm       *TMonitor
	*multipart.Writer
}

func (s *streamReadCloser) Read(p []byte) (n int, err error) {
	buf_len := s.w_buff.Len()
	if buf_len > 0 {
		return s.w_buff.Read(p)
	}

	// Clear our output buffer.
	s.w_buff.Truncate(0)

	if s.eof {
		return 0, io.EOF
	}

	n, err = s.reader.Read(s.r_buff)
	if err != nil && err == io.EOF {
		s.eof = true
		s.Close()
	} else if err != nil {
		return -1, err
	}
	s.size = s.size + int64(n)

	if n > 0 {
		if s.size > s.limit {
			n = int(int64(n) - (s.size - s.limit))
			s.eof = true
		}
		s.tm.RecordTransfer(n)
		n, err = s.f_writer.Write(s.r_buff[0:n])
		if err != nil {
			return -1, err
		}
		if s.eof {
			s.Close()
		}
		for i := 0; i < len(s.r_buff); i++ {
			s.r_buff[i] = 0
		}
	}
	return s.w_buff.Read(p)
}

func checkFile(local_file FileInfo, record *UploadRecord) (same bool) {
	fstat := local_file.info
	if fstat.Size() != record.TotalSize || fstat.ModTime().UTC() != record.ModTime.UTC() {
		return false
	}
	return true
}

// Uploads file from specific local path, uploads in chunks, allows resume.
func (s Session) Upload(local_file FileInfo, folder_id int) (file_id int, err error) {

	fstat := local_file.info

	var record *UploadRecord
	_, err = DB.Get("uploads", local_file.string, &record)
	if err != nil {
		return -1, err
	}

	ChunkSize, TotalChunks, LastChunk := s.getChunkInfo(fstat.Size())

	// Create a record in the database if one does not exist yet or does not appear to be the one we've uploaded.
	if record == nil || record.ModTime.UTC() != fstat.ModTime().UTC() || record.TotalSize != fstat.Size() || (record.ChunkSize != ChunkSize && record.Flag&DONE != DONE) {
		if record != nil && record.Flag&UPLOAD == UPLOAD {
			s.DeleteUpload(record.ID)
		}

		record = &UploadRecord{
			Flag:            UPLOAD,
			CompletedChunks: 0,
			ChunkSize:       ChunkSize,
			TotalSize:       fstat.Size(),
			ModTime:         fstat.ModTime().UTC(),
			Filename:        fstat.Name(),
		}

		if record.ID, record.URI, err = s.NewUpload(folder_id, fstat.Name(), record.ModTime); err != nil {
			return -1, err
		}
		DB.Set("uploads", local_file.string, &record)
	}

	if record.Flag == DONE {
		return record.ID, ErrUploaded
	}

	// Check if file is already uploaded on
	fileSearch, _ := s.FindFile(folder_id, fstat.Name())
	if len(fileSearch.Data) > 0 {
		md5sum, _ := md5Sum(local_file.string)
		for _, kw_file := range fileSearch.Data {
			if kw_file.Size == fstat.Size() && kw_file.Fingerprint == string(md5sum) {
				if err := DB.Set("uploads", local_file.string, &FileRecord{
					Flag:      DONE,
					TotalSize: kw_file.Size,
					ModTime:   fstat.ModTime(),
					ID:        kw_file.ID,
					User:      s,
				}); err != nil {
					return -1, err
				}
				return kw_file.ID, ErrUploaded
			}
		}
	}

	f, err := Open(local_file.string)
	if err != nil {
		return -1, err
	}

	var limit int64
	var offset int64

	w_buff := new(bytes.Buffer)

	tm := NewTMonitor("upload", record.TotalSize)
	tm.Offset(record.Transfered)

	var resp_data *KiteData

	if !checkFile(local_file, record) {
		s.DeleteUpload(record.ID)
		return -1, ErrNotReady
	}

	HideLoader()

	nfo.Log("Uploading %s(%s).", local_file.string, showSize(record.TotalSize))

	show_transfer := uint32(1)
	defer atomic.StoreUint32(&show_transfer, 0)

	go func() {
		for atomic.LoadUint32(&show_transfer) == 1 {
			tm.ShowTransfer(false)
			time.Sleep(time.Second)
		}
	}()

	for record.Transfered < record.TotalSize || record.TotalSize == 0 {
		if !checkFile(local_file, record) {
			s.DeleteUpload(record.ID)
			nfo.Err("%s: Detected change in file while uploading.")
			return -1, ErrNotReady
		}
		w_buff.Reset()

		offset = record.Transfered
		_, err = f.Seek(offset, 0)
		if err != nil {
			return -1, err
		}

		req, err := s.NewRequest("POST", fmt.Sprintf("/%s", record.URI), 7)
		if err != nil {
			return -1, err
		}

		if snoop {
			nfo.Stdout("--> ACTION: \"POST\" PATH: \"%v\" (CHUNK %d OF %d)\n", req.URL.Path, record.CompletedChunks+1, TotalChunks)
		}

		w := multipart.NewWriter(w_buff)

		req.Header.Set("Content-Type", "multipart/form-data; boundary="+w.Boundary())

		if record.TotalSize-record.Transfered <= ChunkSize {
			q := req.URL.Query()
			q.Set("returnEntity", "true")
			q.Set("mode", "full")
			if snoop {
				for k, v := range q {
					nfo.Stdout("\\-> QUERY: %s VALUE: %s", k, v)
				}
			}
			req.URL.RawQuery = q.Encode()
			limit = LastChunk
			err = w.WriteField("lastChunk", "1")
			if err != nil {
				return -1, err
			}
		} else {
			limit = ChunkSize

		}

		err = w.WriteField("compressionMode", "NORMAL")
		if err != nil {
			return -1, err
		}

		err = w.WriteField("index", fmt.Sprintf("%d", record.CompletedChunks+1))
		if err != nil {
			return -1, err
		}

		err = w.WriteField("compressionSize", fmt.Sprintf("%d", limit))
		if err != nil {
			return -1, err
		}

		err = w.WriteField("originalSize", fmt.Sprintf("%d", limit))
		if err != nil {
			return -1, err
		}

		f_writer, err := w.CreateFormFile("content", record.Filename)
		if err != nil {
			return -1, err
		}

		if snoop {
			nfo.Stdout(w_buff.String())
		}

		post := &streamReadCloser{
			limit,
			0,
			make([]byte, 4096),
			w_buff,
			f,
			false,
			f_writer,
			tm,
			w,
		}

		req.Body = post

		client := s.NewClient()
		resp, err := client.Do(req)
		if err != nil {
			if KiteError(err, ERR_ENTITY_PARENT_FOLDER_DELETED) {
				s.DeleteUpload(record.ID)
				DB.Unset("uploads", local_file.string)
			}
			if KiteError(err, ERR_REQUEST_METHOD_NOT_ALLOWED) {
				DB.Unset("uploads", local_file.string)
			}
			return -1, err
		}

		if err := s.DecodeJSON(resp, &resp_data); err != nil {
			return -1, err
		}

		record.CompletedChunks++
		record.Transfered = record.Transfered + ChunkSize
		if err := DB.Set("uploads", local_file.string, &record); err != nil {
			return -1, err
		}
		if record.TotalSize == 0 {
			break
		}
	}

	if resp_data == nil {
		s.DeleteUpload(record.ID)
		DB.Unset("uploads", local_file.string)
		nfo.Err("Unexpected result from server, rolling back upload.")
		return -1, ErrNotReady
	}

	if err := DB.Set("uploads", local_file.string, &FileRecord{
		Flag:      DONE,
		TotalSize: record.TotalSize,
		ModTime:   record.ModTime,
		ID:        resp_data.ID,
		User:      s,
	}); err != nil {
		return resp_data.ID, err
	}
	atomic.StoreUint32(&show_transfer, 0)
	tm.ShowTransfer(true)
	ShowLoader()
	nfo.Log("%s: Upload complete.", local_file.string)
	if Config.GetBool(task+":opts", "delete_source_files_on_complete") {
		nfo.Log("Remvoing local file %s.", local_file.string)
		err = Remove(local_file.string)
		if err == nil {
			DB.Unset("uploads", local_file.string)
		}
	}
	return resp_data.ID, nil
}
