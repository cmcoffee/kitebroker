package main

import (
	"bytes"
	"fmt"
	"github.com/cmcoffee/go-logger"
	"io"
	"mime/multipart"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

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

// Returns total number of chunks and last chunk size.
func (s Session) getChunkInfo(sz int64) (chunk_size int64, total_chunks int, last_chunk int64) {

	chunk_sz, err := strconv.Atoi(Config.Get("configuration", "upload_chunk_size"))
	if err != nil {
		logger.Warn("Could not parse upload_chunk_size, defaulting to 32768.")
		chunk_sz = 32768
	}
	chunk_size = int64(chunk_sz * 1000)

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

	size := t.transfered - t.offset

	if size == t.total_size && t.rate != "0.0bps" {
		return t.rate
	}

	names := []string{
		"bps",
		"kbps",
		"mbps",
		"gbps",
	}

	suffix := 0
	sz := float64(size) / time.Since(t.start_time).Seconds()

	for sz >= (1000) && suffix < len(names)-1 {
		sz = sz / 1000
		suffix++
	}

	if sz != 0.0 {
		t.rate = fmt.Sprintf("%.1f%s", sz*8, names[suffix])
	} else {
		t.rate = "0.0bps"
	}
	return t.rate
}

// Produces progress bar for information on update.
func (t *TMonitor) progressBar() string {
	num := int((float64(t.transfered) / float64(t.total_size)) * 100)
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
		last_shown: time.Now(),
	}
}

type TMonitor struct {
	name       string
	total_size int64
	transfered int64
	offset     int64
	rate       string
	start_time time.Time
	done_time  time.Time
	last_shown time.Time
}

func (t *TMonitor) RecordTransfer(current_sz int) {
	atomic.StoreInt64(&t.transfered, atomic.LoadInt64(&t.transfered)+int64(current_sz))
}

func (t *TMonitor) ShowTransfer() {

	transfered := atomic.LoadInt64(&t.transfered)

	if t.total_size > -1 {
		logger.Put(fmt.Sprintf("(%s) %s %s (%s/%s)", t.name, t.showRate(), t.progressBar(), showSize(transfered), showSize(t.total_size)))
	} else {
		logger.Put(fmt.Sprintf("(%s) %s (%s)", t.name, t.showRate(), showSize(transfered)))
	}
	t.last_shown = time.Now()
}

func (t *TMonitor) Offset(current_sz int64) {
	t.transfered = current_sz
	t.offset = t.transfered
}

// Perform the file transfer, input stream->output stream.
func Transfer(reader io.Reader, writer io.Writer, tm *TMonitor) (err error) {

	buf := make([]byte, 4096)
	var eof bool

	for {
		rb, err := reader.Read(buf)
		if err != nil && err == io.EOF {
			eof = true
		} else if err != nil {
			return err
		}
		if rb > -1 {
			tm.RecordTransfer(rb)
		}
		n := 0
		for {
			wb, err := writer.Write(buf[n:rb])
			if err != nil {
				return err
			}
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
func (s Session) Download(nfo KiteData, local_dest string) (err error) {
	var record FileRecord
	_, err = DB.Get("downloads", nfo.ID, &record)
	if err != nil {
		return err
	}

	mtime, err := read_kw_time(nfo.Modified)
	if err != nil {
		return err
	}

	if record.ModTime.IsZero() || time.Since(mtime) > time.Since(record.ModTime) || record.TotalSize != nfo.Size {
		DB.Unset("downloads", nfo.ID)
		record.Flag = 0
		record.ModTime = mtime
		record.User = s
		record.TotalSize = nfo.Size
		record.ID = nfo.ID
		DB.Set("downloads", nfo.ID, &record)
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

	fname := fmt.Sprintf("%s/%s", local_dest, nfo.Name)

	fstat, err := Stat(fname)
	if err == nil && !os.IsNotExist(err) {
		if fstat.Size() == nfo.Size {
			md5sum, _ := md5Sum(fname)
			if string(md5sum) == nfo.Fingerprint {
				record.Flag = DONE
				DB.Set("downloads", nfo.ID, record)
				return ErrDownloaded
			}
		}
	}

	var offset int64

	fstat, err = Stat(fname + ".incomplete")
	if err == nil || !os.IsNotExist(err) {
		offset = fstat.Size()
	}

	f, err = OpenFile(fname+".incomplete", os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return
	}

	logger.Log("Downloading %s(%s).\n", nfo.Name, showSize(nfo.Size))

	req, err := s.NewRequest("GET", fmt.Sprintf("/rest/files/%d/content", nfo.ID))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	client := s.NewClient()
	resp, err := client.Do(req)
	if err != nil && resp.StatusCode != 200 {
		return err
	}
	defer resp.Body.Close()

	tm := NewTMonitor("download", nfo.Size)
	tm.Offset(offset)

	HideLoader()

	if snoop {
		logger.Put("--> ACTION: \"GET\" PATH: \"%v\"\n", req.URL.Path)
	}

	show_transfer := uint32(1)
	defer atomic.StoreUint32(&show_transfer, 0)

	go func() {
		for atomic.LoadUint32(&show_transfer) == 1 {
			tm.ShowTransfer()
			time.Sleep(time.Second)
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

	record.ModTime, err = read_kw_time(nfo.Modified)
	if err != nil {
		return
	}

	DB.Set("downloads", nfo.ID, record)

renameFile:
	atomic.StoreUint32(&show_transfer, 0)
	tm.ShowTransfer()
	fmt.Println(NONE)
	logger.Log("Download completed succesfully.")
	ShowLoader()

	// Close the file stream.
	f.Close()

	// Rename file.
	if err = Rename(fname+".incomplete", fname); err != nil {
		return
	}

	record.Flag |= DONE

	DB.Set("downloads", nfo.ID, record)

	if strings.ToLower(Config.Get("configuration", "delete_source_files_on_complete")) == "yes" {
		if err := s.EraseFile(nfo.ID); err != nil {
			logger.Err(err)
		}
	}

	// Set modified and access times on file.
	err = Chtimes(fname, time.Now(), record.ModTime)
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

func checkFile(local_file string, record *UploadRecord) (same bool) {
	fstat, err := Stat(local_file)
	if err != nil {
		return false
	}
	if fstat.Size() != record.TotalSize || fstat.ModTime().UTC() != record.ModTime.UTC() {
		return false
	}
	return true
}

// Uploads file from specific local path, uploads in chunks, allows resume.
func (s Session) Upload(local_file string, folder_id int) (file_id int, err error) {
	fstat, err := Stat(local_file)
	if err != nil {
		return -1, err
	}

	// Skip 0 byte files.
	if fstat.Size() == 0 {
		return -1, ErrZeroByte
	}

	var record *UploadRecord
	_, err = DB.Get("uploads", local_file, &record)
	if err != nil {
		return -1, err
	}

	ChunkSize, TotalChunks, LastChunk := s.getChunkInfo(fstat.Size())

	// Create a record in the database if one does not exist yet or does not appear to be the one we've uploaded.
	if record == nil || record.ModTime.UTC() != fstat.ModTime().UTC() || record.TotalSize != fstat.Size() || record.ChunkSize != ChunkSize && record.Flag&DONE != DONE {
		if record != nil && record.Flag&UPLOAD == UPLOAD {
			s.DeleteUpload(record.ID)
		}

		record = &UploadRecord{
			Flag:            UPLOAD,
			CompletedChunks: 0,
			ChunkSize:       ChunkSize,
			TotalSize:       fstat.Size(),
			ModTime:         fstat.ModTime(),
			Filename:        fstat.Name(),
		}

		if record.ID, record.URI, err = s.NewUpload(folder_id, fstat.Name(), record.ModTime); err != nil {
			return -1, err
		}
		DB.Set("uploads", local_file, &record)
	}

	if record.Flag == DONE {
		return record.ID, ErrUploaded
	}

	// Check if file is already uploaded on
	fileSearch, _ := s.FindFile(folder_id, fstat.Name())
	if len(fileSearch.Data) > 0 {
		md5sum, _ := md5Sum(local_file)
		for _, kw_file := range fileSearch.Data {
			if kw_file.Size == fstat.Size() && kw_file.Fingerprint == string(md5sum) {
				if err := DB.Set("uploads", local_file, &FileRecord{
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

	f, err := Open(local_file)
	if err != nil {
		return -1, err
	}
	defer f.Close()

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

	logger.Log("Uploading %s(%s).", local_file, showSize(record.TotalSize))

	show_transfer := uint32(1)
	defer atomic.StoreUint32(&show_transfer, 0)

	go func() {
		for atomic.LoadUint32(&show_transfer) == 1 {
			tm.ShowTransfer()
			time.Sleep(time.Second)
		}
	}()

	for record.Transfered < record.TotalSize {
		if !checkFile(local_file, record) {
			s.DeleteUpload(record.ID)
			logger.Err("%s: Detected change in file while uploading.")
			return -1, ErrNotReady
		}
		w_buff.Reset()

		offset = record.Transfered
		_, err = f.Seek(offset, 0)
		if err != nil {
			return -1, err
		}

		req, err := s.NewRequest("POST", fmt.Sprintf("/%s", record.URI))
		if err != nil {
			return -1, err
		}

		if snoop {
			logger.Put("--> ACTION: \"POST\" PATH: \"%v\" (CHUNK %d OF %d)\n", req.URL.Path, record.CompletedChunks+1, TotalChunks)
		}

		w := multipart.NewWriter(w_buff)

		req.Header.Set("Content-Type", "multipart/form-data; boundary="+w.Boundary())

		if record.TotalSize-record.Transfered <= ChunkSize {
			q := req.URL.Query()
			q.Set("returnEntity", "true")
			q.Set("mode", "full")
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
			return -1, err
		}

		if err := s.DecodeJSON(resp, &resp_data); err != nil {
			return -1, err
		}

		record.CompletedChunks++
		record.Transfered = record.Transfered + ChunkSize
		if err := DB.Set("uploads", local_file, &record); err != nil {
			return -1, err
		}
	}

	if resp_data == nil {
		s.DeleteUpload(record.ID)
		DB.Unset("uploads", local_file)
		logger.Err("Unexpected result from server, rolling back upload.")
		return -1, ErrNotReady
	}

	if err := DB.Set("uploads", local_file, &FileRecord{
		Flag:      DONE,
		TotalSize: record.TotalSize,
		ModTime:   record.ModTime,
		ID:        resp_data.ID,
		User:      s,
	}); err != nil {
		return resp_data.ID, err
	}
	atomic.StoreUint32(&show_transfer, 0)
	tm.ShowTransfer()
	fmt.Println(NONE)
	logger.Log("Upload completed succesfully.")
	if strings.ToLower(Config.Get("configuration", "delete_source_files_on_complete")) == "yes" {
		logger.Log("Remvoing local file %s.", local_file)
		err = Remove(local_file)
		if err == nil {
			DB.Unset("uploads", local_file)
		}
	}
	ShowLoader()
	return resp_data.ID, nil
}
